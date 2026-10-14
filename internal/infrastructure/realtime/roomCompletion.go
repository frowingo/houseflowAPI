package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/infrastructure/cqrs"
)

// completionState is owned by the room's serialized lifecycle loop: the frozen
// proposed result, cancel deliveries waiting for its commit and the start of
// the finalization budget.
type completionState struct {
	result        *gameDomain.MatchResult
	cancellations []coordinationAbstract.CommandDelivery
	startedAt     time.Time
}

func (room *managedRoom) captureResult() {
	if room.completion.result != nil || room.manager.options.MatchRepository == nil {
		return
	}
	room.tick.mutex.RLock()
	defer room.tick.mutex.RUnlock()
	if room.tick.runtime == nil {
		return
	}
	result, proposed, err := room.tick.runtime.TakeResult()
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		room.stop()
		return
	}
	if proposed {
		room.completion.result = &result
		room.completion.startedAt = time.Now()
	}
}

// holdDuringFinalization keeps a retried cancel for the pending commit and
// rejects every other command until the result is durable.
func (room *managedRoom) holdDuringFinalization(delivery coordinationAbstract.CommandDelivery) {
	envelope := delivery.Envelope()
	held := len(room.completion.cancellations) < room.manager.options.CommandQueueSize
	if trigger := room.runtimeOwner.CompletionTrigger; trigger != nil && envelope.MessageID == trigger.CommandID && envelope.ActorID == trigger.ActorID && envelope.Type == trigger.CommandType {
		if held {
			room.completion.cancellations = append(room.completion.cancellations, delivery)
		}
		return
	}
	if len(room.completion.cancellations) > 0 {
		first := room.completion.cancellations[0].Envelope()
		if envelope.MessageID == first.MessageID && envelope.ActorID == first.ActorID && envelope.Type == first.Type {
			if held {
				room.completion.cancellations = append(room.completion.cancellations, delivery)
			}
			return
		}
	}
	_ = room.publishCommandRejected(envelope, room.finalizingError())
	_ = delivery.Ack(room.ctx)
}

// cancelRunningMatch turns an authorized cancel into a runtime cancellation;
// the delivery is acknowledged only after the cancelled result commits.
func (room *managedRoom) cancelRunningMatch(delivery coordinationAbstract.CommandDelivery) {
	envelope := delivery.Envelope()
	if err := room.requestMatchCancellation(envelope); err != nil {
		_ = room.publishCommandRejected(envelope, err)
		if !isRetryableCommandError(err) {
			_ = delivery.Ack(room.ctx)
		}
		return
	}
	room.completion.cancellations = append(room.completion.cancellations, delivery)
	room.captureResult()
}

func (room *managedRoom) requestMatchCancellation(envelope coordinationAbstract.MessageEnvelope) error {
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	if _, err := cqrs.Send[gameDomain.SessionSnapshot](ctx, room.manager.sender, gameQueries.AuthorizeCancellationQuery{SessionID: room.lease.RoomID, UserID: envelope.ActorID, CommandID: envelope.MessageID}); err != nil {
		return err
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	room.tick.mutex.RLock()
	defer room.tick.mutex.RUnlock()
	if room.tick.runtime == nil {
		return ErrRoomRuntimeNotStarted
	}
	payload, _ := json.Marshal(struct{ SessionID string }{room.lease.RoomID})
	digest := sha256.Sum256(payload)
	trigger := gameAbstract.CommandDescriptor{CommandID: envelope.MessageID, ActorID: envelope.ActorID, CommandType: envelope.Type, PayloadHash: hex.EncodeToString(digest[:])}
	if err := room.manager.owners.SaveCompletionTrigger(ctx, room.runtimeOwner, trigger); err != nil {
		return err
	}
	room.runtimeOwner.CompletionTrigger = &trigger
	err := room.tick.runtime.Cancel(module.EndCancelledByUser)
	if errors.Is(err, module.ErrFinished) {
		return room.finalizingError()
	}
	return err
}

// completeMatch commits the frozen result and returns true when the room is
// done. Storage is retried without restarting the simulation, advancing its
// result or releasing the active slot; lease renewal runs independently.
func (room *managedRoom) completeMatch() bool {
	if !room.leaseValid() {
		return true
	}
	if room.completion.startedAt.IsZero() {
		room.completion.startedAt = time.Now()
	}
	if time.Since(room.completion.startedAt) > room.manager.options.FinalizationTimeout {
		room.manager.report(room.lease.RoomID, context.DeadlineExceeded)
		return true
	}
	if err := room.protectFinalCheckpoint(); err != nil {
		room.manager.report(room.lease.RoomID, err)
		return true
	}
	command := gameCommands.CompleteMatchCommand{Result: *room.completion.result, Owner: room.runtimeOwner}
	command.CancelCommandID, command.CancelUserID = room.cancellationCommand(command.Result)
	ctx, cancel := context.WithTimeout(room.ctx, min(room.manager.options.CommandTimeout, time.Until(*room.leaseDeadline.Load())))
	result, err := cqrs.Send[gameDomain.MatchResult](ctx, room.manager.sender, command)
	cancel()
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		return errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) || errors.Is(err, gameAbstract.ErrMatchResultConflict) || (!isRetryableCommandError(err) && !errors.Is(err, context.DeadlineExceeded))
	}
	if err := room.releaseCheckpoint(); err != nil {
		room.manager.report(room.lease.RoomID, err)
	}
	// ACK means durable completion, not delivery to every currently connected socket.
	for _, delivery := range room.completion.cancellations {
		_ = delivery.Ack(room.ctx)
	}
	if !room.leaseValid() {
		return true
	}
	return room.announceCompletion(result)
}

// The proposal must survive this owner's crash before it is committed.
func (room *managedRoom) protectFinalCheckpoint() error {
	room.tick.mutex.RLock()
	defer room.tick.mutex.RUnlock()
	if room.tick.runtime == nil {
		return nil
	}
	checkpoint := room.tick.runtime.Checkpoint()
	if err := room.protectCheckpoint(&checkpoint, true); err != nil && !errors.Is(err, coordinationAbstract.ErrCheckpointOrder) {
		return err
	}
	return nil
}

// cancellationCommand names the user cancel that produced a cancelled result,
// preferring the delivery still waiting for this commit.
func (room *managedRoom) cancellationCommand(result gameDomain.MatchResult) (string, string) {
	if len(room.completion.cancellations) > 0 {
		envelope := room.completion.cancellations[0].Envelope()
		return envelope.MessageID, envelope.ActorID
	}
	if trigger := room.runtimeOwner.CompletionTrigger; trigger != nil && result.EndReason == module.EndCancelledByUser {
		return trigger.CommandID, trigger.ActorID
	}
	return "", ""
}

// announceCompletion publishes the terminal session, the committed frame and
// the result, in that order. A lost publication is recovered over HTTP.
func (room *managedRoom) announceCompletion(result gameDomain.MatchResult) bool {
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	session, err := room.manager.repository.FindByID(ctx, room.lease.RoomID)
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		return false
	}
	snapshot := session.Snapshot()
	room.snapshot.Store(&snapshot)
	messageID, _ := room.cancellationCommand(result)
	if messageID == "" {
		messageID = "complete:" + room.lease.RoomID
	}
	if err = room.publishSnapshot(messageID, snapshot); err != nil {
		room.manager.report(room.lease.RoomID, err)
		return true
	}
	gameKey := room.tick.game.Definition().GameKey
	events := []coordinationAbstract.MessageEnvelope{}
	room.tick.mutex.RLock()
	if room.tick.runtime != nil {
		frame, err := room.tick.runtime.CommittedFrame(result)
		if err != nil {
			room.tick.mutex.RUnlock()
			room.manager.report(room.lease.RoomID, err)
			return true
		}
		events = append(events, coordinationAbstract.MessageEnvelope{MessageID: gameKey + ".ended:" + room.lease.RoomID, Type: GameRuntimeSnapshotEventType, Sequence: frame.Sequence, Payload: frame.Payload})
	}
	room.tick.mutex.RUnlock()
	events = append(events, coordinationAbstract.MessageEnvelope{MessageID: module.ResultMessageType(gameKey) + ":" + room.lease.RoomID, Type: module.ResultMessageType(gameKey), Payload: result.Payload})
	for _, event := range events {
		if !room.leaseValid() {
			return true
		}
		if err = room.publish(ctx, event); err != nil {
			room.manager.report(room.lease.RoomID, err)
			return true
		}
	}
	return true
}
