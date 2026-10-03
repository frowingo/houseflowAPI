package realtime

import (
	"context"
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
	command := rocketsCommands.CompleteMatchCommand{Result: *room.pendingResult, Owner: room.runtimeOwner}
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
	if len(room.pendingCancellations) > 0 {
		messageID = room.pendingCancellations[0].Envelope().MessageID
	}
	if err = room.publishSnapshot(messageID, snapshot); err != nil {
		room.manager.report(room.lease.RoomID, err)
		return true
	}
	room.gameMutex.RLock()
	frame := room.game.committedFrame(result)
	room.gameMutex.RUnlock()
	framePayload, _ := json.Marshal(frame)
	resultPayload, _ := json.Marshal(result)
	for _, event := range []coordinationAbstract.MessageEnvelope{
		{MessageID: "houseRockets.ended:" + room.lease.RoomID, Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, Payload: framePayload},
		{MessageID: "houseRockets.result:" + room.lease.RoomID, Type: houseRockets.ResultMessageType, Payload: resultPayload},
	} {
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
