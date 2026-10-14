package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
)

// managedRoom is one locally owned room. Its lifecycle loop serializes durable
// commands, deadlines, reconciliation and completion; lease renewal, the tick
// runtime and its publisher run on their own goroutines.
type managedRoom struct {
	manager       *RoomManager
	ctx           context.Context
	cancel        context.CancelFunc
	lease         coordinationAbstract.RoomLease
	queue         chan coordinationAbstract.CommandDelivery
	stopOnce      sync.Once
	leaseDeadline atomic.Pointer[time.Time]
	runtimeOwner  gameAbstract.RuntimeOwner
	snapshot      atomic.Pointer[gameDomain.SessionSnapshot]
	lobby         lobbyState
	tick          tickState
	completion    completionState
	publishMutex  sync.Mutex
	audience      audienceState
}

type roomActivation struct {
	done chan struct{}
}

func (room *managedRoom) ownership() RoomOwnership {
	ownership := ownershipFromLease(room.lease, true)
	ownership.RuntimeEpoch = room.runtimeOwner.Generation
	return ownership
}

func (room *managedRoom) stop() {
	room.stopOnce.Do(room.cancel)
}

// managesPlayers reports whether the room tracks membership, lobby heartbeats
// and a frozen roster. Only games with a runtime have realtime players.
func (room *managedRoom) managesPlayers() bool { return room.tick.game != nil }

func (room *managedRoom) leaseValid() bool {
	deadline := room.leaseDeadline.Load()
	return room.ctx.Err() == nil && deadline != nil && time.Now().Before(*deadline)
}

// Lease maintenance never shares the database command loop or physics scheduler.
func (room *managedRoom) renewLoop() {
	defer room.manager.workers.Done()
	ticker := time.NewTicker(room.manager.options.RenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-room.ctx.Done():
			return
		case <-ticker.C:
			if !room.leaseValid() {
				room.manager.report(room.lease.RoomID, coordinationAbstract.ErrLeaseLost)
				room.stop()
				return
			}
			startedAt := time.Now()
			remaining := time.Until(*room.leaseDeadline.Load())
			timeout := min(remaining, room.manager.options.CommandTimeout)
			ctx, cancel := context.WithTimeout(room.ctx, timeout)
			renewed, err := room.manager.coordinator.RenewRoom(ctx, room.lease, room.manager.options.LeaseTTL)
			cancel()
			if err != nil || !renewed || !room.leaseValid() {
				room.manager.report(room.lease.RoomID, errors.Join(coordinationAbstract.ErrLeaseLost, err))
				room.stop()
				return
			}
			// Request start is a conservative local expiry, not response arrival.
			deadline := startedAt.Add(room.manager.options.LeaseTTL)
			room.leaseDeadline.Store(&deadline)
		}
	}
}

// syncSession applies a new durable snapshot to the room's realtime players
// and runtime. Lobby-only games keep no realtime state.
func (room *managedRoom) syncSession(snapshot gameDomain.SessionSnapshot, refreshDirectory bool) error {
	if !room.managesPlayers() {
		return nil
	}
	room.snapshot.Store(&snapshot)
	if refreshDirectory {
		if err := room.refreshParticipants(snapshot); err != nil {
			return err
		}
	}
	return room.syncTick(snapshot)
}

// roomLoop is the serialized lifecycle of one owned room. Each handler returns
// false when the room must stop.
type roomLoop struct {
	room      *managedRoom
	snapshot  gameDomain.SessionSnapshot
	deadline  *time.Timer
	idleSince time.Time
}

func (room *managedRoom) run(snapshot gameDomain.SessionSnapshot) {
	defer room.manager.workers.Done()
	defer room.manager.removeRoom(room)
	defer room.manager.releaseLease(room.lease)
	defer room.stop()
	options := room.manager.options
	loop := &roomLoop{room: room, snapshot: snapshot}
	loop.resetDeadline()
	defer loop.stopDeadline()
	idleTicker := time.NewTicker(min(options.IdleRoomTimeout/2, options.LeaseTTL))
	defer idleTicker.Stop()
	completionTicks, stopCompletion := optionalTicker(options.MatchRepository != nil && room.managesPlayers(), 250*time.Millisecond)
	defer stopCompletion()
	reconcileInterval := options.ReconcileInterval
	if reconcileInterval <= 0 {
		reconcileInterval = 2 * time.Second
	}
	reconcileTicks, stopReconcile := optionalTicker(options.ParticipantDirectory != nil && room.managesPlayers(), reconcileInterval)
	defer stopReconcile()

	for {
		room.captureResult()
		select {
		case <-room.ctx.Done():
			return
		case <-idleTicker.C:
			if loop.idleExpired() {
				return
			}
		case <-completionTicks:
			room.captureResult()
			if room.completion.result != nil && room.completeMatch() {
				return
			}
		case <-reconcileTicks:
			if !loop.reconcile() {
				return
			}
		case delivery := <-room.queue:
			if !loop.handleDelivery(delivery) {
				return
			}
		case <-loop.deadlineChannel():
			if !loop.handleDeadline() {
				return
			}
		}
	}
}

func optionalTicker(enabled bool, interval time.Duration) (<-chan time.Time, func()) {
	if !enabled {
		return nil, func() {}
	}
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

// apply makes a newer durable snapshot current for the room and its runtime.
func (loop *roomLoop) apply(updated gameDomain.SessionSnapshot, refreshDirectory bool) bool {
	loop.snapshot = updated
	if err := loop.room.syncSession(updated, refreshDirectory); err != nil {
		loop.room.manager.report(loop.room.lease.RoomID, err)
		return false
	}
	loop.resetDeadline()
	return true
}

func (loop *roomLoop) terminal() bool {
	return loop.snapshot.State == gameDomain.SessionFinished || loop.snapshot.State == gameDomain.SessionCancelled
}

// idleExpired releases an empty lobby so its memory and lease do not outlive
// every connection; a lobby's durable state is reopened on the next socket.
func (loop *roomLoop) idleExpired() bool {
	room := loop.room
	if loop.snapshot.State != gameDomain.SessionLobby || !room.managesPlayers() {
		loop.idleSince = time.Time{}
		return false
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	presence, err := room.manager.coordinator.ListPresence(ctx, room.lease.RoomID)
	cancel()
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		return true
	}
	if len(presence) > 0 {
		loop.idleSince = time.Time{}
		return false
	}
	if loop.idleSince.IsZero() {
		loop.idleSince = time.Now()
	}
	return time.Since(loop.idleSince) >= room.manager.options.IdleRoomTimeout
}

func (loop *roomLoop) reconcile() bool {
	room := loop.room
	room.captureResult()
	if room.completion.result != nil {
		return true
	}
	updated, err := room.reconcilePlayers(loop.snapshot, time.Now().UTC())
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		room.stop()
		return false
	}
	changed := updated.Version != loop.snapshot.Version
	if !loop.apply(updated, false) {
		return false
	}
	if changed {
		if err := room.publishSnapshot("", loop.snapshot); err != nil {
			room.manager.report(room.lease.RoomID, err)
			return false
		}
	}
	return !loop.terminal()
}

func (loop *roomLoop) handleDelivery(delivery coordinationAbstract.CommandDelivery) bool {
	room := loop.room
	envelope := delivery.Envelope()
	room.captureResult()
	if room.completion.result != nil {
		room.holdDuringFinalization(delivery)
		return true
	}
	if loop.snapshot.State == gameDomain.SessionRunning && envelope.Type == gameCommands.CancelGameSessionCommandType && room.tick.game != nil && room.manager.options.MatchRepository != nil {
		room.cancelRunningMatch(delivery)
		return true
	}
	updated, err := room.processCommand(envelope)
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		if isRetryableCommandError(err) {
			return true
		}
		if rejectionErr := room.publishCommandRejected(envelope, err); rejectionErr != nil {
			room.manager.report(room.lease.RoomID, rejectionErr)
			if errors.Is(rejectionErr, coordinationAbstract.ErrLeaseLost) {
				return false
			}
		}
		_ = delivery.Ack(room.ctx)
		return true
	}
	if !loop.apply(updated, true) {
		return false
	}
	publishErr := room.publishSnapshot(envelope.MessageID, updated)
	if publishErr != nil {
		room.manager.report(room.lease.RoomID, publishErr)
	}
	if err := delivery.Ack(room.ctx); err != nil {
		room.manager.report(room.lease.RoomID, err)
	}
	if errors.Is(publishErr, coordinationAbstract.ErrLeaseLost) || errors.Is(publishErr, coordinationAbstract.ErrUnavailable) {
		return false
	}
	return !loop.terminal()
}

// handleDeadline first excludes unavailable players so an overdue transition
// cannot start a match with them, then advances the session.
func (loop *roomLoop) handleDeadline() bool {
	room := loop.room
	deadline, exists := sessionDeadline(loop.snapshot)
	if !exists {
		loop.resetDeadline()
		return true
	}
	current, err := room.reconcilePlayers(loop.snapshot, deadline)
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		return false
	}
	if current.Version != loop.snapshot.Version {
		if !loop.apply(current, false) {
			return false
		}
		if err := room.publishSnapshot("", loop.snapshot); err != nil {
			room.manager.report(room.lease.RoomID, err)
			return false
		}
		return loop.snapshot.State != gameDomain.SessionCancelled
	}
	updated, err := room.advance(deadline)
	if err != nil {
		room.manager.report(room.lease.RoomID, err)
		stored, loadErr := room.manager.repository.FindByID(room.ctx, room.lease.RoomID)
		if loadErr != nil {
			room.manager.report(room.lease.RoomID, loadErr)
			return false
		}
		return loop.apply(stored.Snapshot(), false)
	}
	messageID := advanceCommandID(loop.snapshot, deadline)
	if !loop.apply(updated, false) {
		return false
	}
	if err := room.publishSnapshot(messageID, updated); err != nil {
		room.manager.report(room.lease.RoomID, err)
		if errors.Is(err, coordinationAbstract.ErrLeaseLost) || errors.Is(err, coordinationAbstract.ErrUnavailable) {
			return false
		}
	}
	return !loop.terminal()
}

func (loop *roomLoop) resetDeadline() {
	loop.stopDeadline()
	deadline, exists := sessionDeadline(loop.snapshot)
	if !exists {
		return
	}
	loop.deadline = time.NewTimer(max(time.Until(deadline), 0))
}

func (loop *roomLoop) stopDeadline() {
	if loop.deadline == nil {
		return
	}
	if !loop.deadline.Stop() {
		select {
		case <-loop.deadline.C:
		default:
		}
	}
	loop.deadline = nil
}

func (loop *roomLoop) deadlineChannel() <-chan time.Time {
	if loop.deadline == nil {
		return nil
	}
	return loop.deadline.C
}

func (room *managedRoom) processCommand(
	envelope coordinationAbstract.MessageEnvelope,
) (gameDomain.SessionSnapshot, error) {
	var snapshot gameDomain.SessionSnapshot
	var err error
	for attempt := 1; attempt <= room.manager.options.MaxCommandAttempts; attempt++ {
		commandCtx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		snapshot, err = room.manager.processor.Process(commandCtx, envelope)
		cancel()
		if err == nil || !isRetryableCommandError(err) || attempt == room.manager.options.MaxCommandAttempts {
			return snapshot, err
		}
		select {
		case <-time.After(room.manager.options.RetryDelay):
		case <-room.ctx.Done():
			return gameDomain.SessionSnapshot{}, room.ctx.Err()
		}
	}
	return snapshot, err
}

func (room *managedRoom) advance(deadline time.Time) (gameDomain.SessionSnapshot, error) {
	commandID := advanceCommandIDForRoom(room.lease.RoomID, deadline)
	commandCtx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	return cqrs.Send[gameDomain.SessionSnapshot](commandCtx, room.manager.sender, gameCommands.AdvanceGameSessionCommand{
		CommandID: commandID,
		SessionID: room.lease.RoomID,
		AdvanceAt: deadline,
	})
}

func (room *managedRoom) publishSnapshot(messageID string, snapshot gameDomain.SessionSnapshot) error {
	if messageID == "" {
		messageID = uuid.NewString()
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return room.publish(room.ctx, coordinationAbstract.MessageEnvelope{
		MessageID: messageID,
		Type:      GameSessionSnapshotEventType,
		Sequence:  snapshot.Version,
		Payload:   payload,
	})
}

func (room *managedRoom) publishCommandRejected(
	envelope coordinationAbstract.MessageEnvelope,
	commandErr error,
) error {
	payload, err := json.Marshal(protocolErrorFrom(commandErr))
	if err != nil {
		return err
	}
	return room.publish(room.ctx, coordinationAbstract.MessageEnvelope{
		MessageID:    envelope.MessageID,
		Type:         CommandRejectedMessageType,
		ConnectionID: envelope.ConnectionID,
		Payload:      payload,
	})
}

// finalizingError rejects lobby commands while a match result is being saved.
func (room *managedRoom) finalizingError() error {
	return helpers.NewConflictError(module.ErrorCode(room.tick.game.Definition().GameKey, "finalizing"))
}

func sessionDeadline(snapshot gameDomain.SessionSnapshot) (time.Time, bool) {
	switch snapshot.State {
	case gameDomain.SessionReadyWindow:
		return snapshot.ReadyWindowEndsAt, !snapshot.ReadyWindowEndsAt.IsZero()
	case gameDomain.SessionCountdown:
		return snapshot.CountdownEndsAt, !snapshot.CountdownEndsAt.IsZero()
	default:
		return time.Time{}, false
	}
}

func advanceCommandID(snapshot gameDomain.SessionSnapshot, deadline time.Time) string {
	return advanceCommandIDForRoom(snapshot.SessionID, deadline)
}

func advanceCommandIDForRoom(roomID string, deadline time.Time) string {
	return fmt.Sprintf("advance:%s:%d", roomID, deadline.UTC().UnixNano())
}

func isRetryableCommandError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, coordinationAbstract.ErrUnavailable) {
		return true
	}
	var applicationError *helpers.ApplicationError
	if errors.As(err, &applicationError) {
		return applicationError.Kind == helpers.ErrorKindUnavailable ||
			applicationError.Key == "game.error.session_conflict"
	}
	if errors.Is(err, ErrUnsupportedRoomCommand) || errors.Is(err, ErrRoomCommandPayloadRequired) {
		return false
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) {
		return false
	}
	return true
}
