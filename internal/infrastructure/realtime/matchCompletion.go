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
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	rocketsCommands "houseflowApi/internal/application/game/gameSpesific/houseRockets/commands"
	gameQueries "houseflowApi/internal/application/game/queries"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

// Only the room's serialized lifecycle loop owns pending completion state.
func (room *managedRoom) captureResult() {
	if room.pendingResult != nil || room.manager.options.MatchRepository == nil {
		return
	}
	room.gameMutex.RLock()
	defer room.gameMutex.RUnlock()
	if room.game == nil {
		return
	}
	select {
	case result := <-room.game.results:
		room.pendingResult = &result
		room.completionStarted = time.Now()
	default:
	}
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
	room.gameMutex.RLock()
	defer room.gameMutex.RUnlock()
	if room.game == nil {
		return ErrRoomRuntimeNotStarted
	}
	payload, _ := json.Marshal(struct{ SessionID string }{room.lease.RoomID})
	digest := sha256.Sum256(payload)
	trigger := gameAbstract.CommandDescriptor{CommandID: envelope.MessageID, ActorID: envelope.ActorID, CommandType: envelope.Type, PayloadHash: hex.EncodeToString(digest[:])}
	if err := room.manager.repository.(gameAbstract.RuntimeOwnerRepository).SaveCompletionTrigger(ctx, room.runtimeOwner, trigger); err != nil {
		return err
	}
	room.runtimeOwner.CompletionTrigger = &trigger
	err := room.game.cancel()
	if errors.Is(err, houseRockets.ErrSimulationFinished) {
		return helpers.NewConflictError("houseRockets.error.finalizing")
	}
	return err
}

// Retry storage without restarting simulation, advancing its frozen result, or
// releasing the active slot. Lease renewal runs independently during DB I/O.
func (room *managedRoom) completeMatch() bool {
	if !room.leaseValid() {
		return true
	}
	if room.completionStarted.IsZero() {
		room.completionStarted = time.Now()
	}
	if time.Since(room.completionStarted) > room.manager.options.FinalizationTimeout {
		room.manager.report(room.lease.RoomID, context.DeadlineExceeded)
		return true
	}
	room.gameMutex.RLock()
	if room.game != nil {
		checkpoint := room.game.checkpoint()
		if err := room.protectCheckpoint(&checkpoint, true); err != nil && !errors.Is(err, coordinationAbstract.ErrCheckpointOrder) {
			room.gameMutex.RUnlock()
			room.manager.report(room.lease.RoomID, err)
			return true
		}
	}
	room.gameMutex.RUnlock()
	command := rocketsCommands.CompleteMatchCommand{Result: *room.pendingResult, Owner: room.runtimeOwner}
	if trigger := room.runtimeOwner.CompletionTrigger; trigger != nil && command.Result.EndReason == houseRockets.EndCancelledByUser {
		command.CancelCommandID, command.CancelUserID = trigger.CommandID, trigger.ActorID
	}
	if len(room.pendingCancellations) > 0 {
		envelope := room.pendingCancellations[0].Envelope()
		command.CancelCommandID, command.CancelUserID = envelope.MessageID, envelope.ActorID
	}
	ctx, cancel := context.WithTimeout(room.ctx, min(room.manager.options.CommandTimeout, time.Until(*room.leaseDeadline.Load())))
	result, err := cqrs.Send[houseRockets.HouseRocketsResultModel](ctx, room.manager.sender, command)
	cancel()
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		return errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) || errors.Is(err, gameAbstract.ErrMatchResultConflict) || (!isRetryableCommandError(err) && !errors.Is(err, context.DeadlineExceeded))
	}
	if store, ok := room.manager.coordinator.(coordinationAbstract.RuntimeCheckpointStore); ok {
		ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		if err := store.DeleteRuntimeCheckpoint(ctx, room.lease); err != nil {
			room.manager.report(room.lease.RoomID, err)
		}
		cancel()
	}
	// ACK means durable completion, not delivery to every currently connected socket.
	for _, delivery := range room.pendingCancellations {
		_ = delivery.Ack(room.ctx)
	}
	if !room.leaseValid() {
		return true
	}
	ctx, cancel = context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	session, err := room.manager.repository.FindByID(ctx, room.lease.RoomID)
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		return false
	}
	snapshot := session.Snapshot()
	room.snapshot.Store(&snapshot)
	messageID := "complete:" + room.lease.RoomID
	if trigger := room.runtimeOwner.CompletionTrigger; trigger != nil && result.EndReason == houseRockets.EndCancelledByUser {
		messageID = trigger.CommandID
	}
	if len(room.pendingCancellations) > 0 {
		messageID = room.pendingCancellations[0].Envelope().MessageID
	}
	if err = room.publishSnapshot(messageID, snapshot); err != nil {
		room.manager.report(room.lease.RoomID, err)
		return true
	}
	resultPayload, _ := json.Marshal(result)
	events := []coordinationAbstract.MessageEnvelope{}
	room.gameMutex.RLock()
	if room.game != nil {
		frame := room.game.committedFrame(result)
		framePayload, _ := json.Marshal(frame)
		events = append(events, coordinationAbstract.MessageEnvelope{MessageID: "houseRockets.ended:" + room.lease.RoomID, Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, Payload: framePayload})
	}
	room.gameMutex.RUnlock()
	events = append(events, coordinationAbstract.MessageEnvelope{MessageID: "houseRockets.result:" + room.lease.RoomID, Type: houseRockets.ResultMessageType, Payload: resultPayload})
	for _, event := range events {
		if !room.leaseValid() {
			return true
		}
		if err = room.manager.coordinator.PublishRoomEvent(ctx, room.lease, event); err != nil {
			room.manager.report(room.lease.RoomID, err)
			return true
		}
	}
	if err = room.manager.options.MatchRepository.MarkPublished(ctx, room.lease.RoomID); err != nil {
		room.manager.report(room.lease.RoomID, err)
	}
	return true
}
