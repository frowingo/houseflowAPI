package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
)

const (
	GameplayInputMessageType     = "runtime.input"
	GameRuntimeSnapshotEventType = "gameRuntime.snapshot"
	maximumRuntimeInputBytes     = 2048
)

// tickState is a room's runtime side: the game module, the running match (nil
// until the session runs) and the checkpoint barrier protecting it.
type tickState struct {
	game            module.TickModule
	mutex           sync.RWMutex
	runtime         module.TickRuntime
	previewSequence int64
	checkpoint      checkpointState
}

// DispatchGameplay uses memory for a local owner and the ephemeral gameplay bus
// for a remote one. It never creates or restarts a running game and never
// replays old input. control must come from the authenticated gateway; intent
// is nil for the core's own control inputs.
func (manager *RoomManager) DispatchGameplay(ctx context.Context, roomID string, control module.ControlInput, intent module.Intent) error {
	if _, err := manager.runningContext(); err != nil {
		return err
	}
	if roomID == "" || control.PlayerID == "" || control.ConnectionID == "" {
		return module.ErrInvalidInput
	}
	control.CreatedAt = time.Now().UTC()
	var input any = control
	if intent != nil {
		input = intent.RuntimeInput(control)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	envelope := coordinationAbstract.MessageEnvelope{MessageID: uuid.NewString(), RoomID: roomID, Type: GameplayInputMessageType, ActorID: control.PlayerID, ConnectionID: control.ConnectionID, CreatedAt: control.CreatedAt, Payload: payload}
	if room := manager.localRoom(roomID); room != nil {
		return room.submitGameplay(envelope)
	}
	owner, exists, err := manager.coordinator.CurrentRoomOwner(ctx, roomID)
	if err != nil {
		return err
	}
	if !exists {
		return coordinationAbstract.ErrLeaseLost
	}
	if manager.gameplay == nil {
		return coordinationAbstract.ErrUnavailable
	}
	envelope.FencingToken = owner.FencingToken
	return manager.gameplay.PublishGameplay(ctx, owner.OwnerInstanceID, envelope)
}

func (manager *RoomManager) gameplayLoop(ctx context.Context, subscription coordinationAbstract.RoomEventSubscription) {
	defer manager.workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case err, open := <-subscription.Errors():
			if !open {
				return
			}
			if err != nil {
				manager.report("", err)
			}
		case envelope, open := <-subscription.Messages():
			if !open {
				return
			}
			room := manager.localRoom(envelope.RoomID)
			if room == nil || envelope.Type != GameplayInputMessageType || envelope.FencingToken != room.lease.FencingToken {
				continue
			}
			if err := room.submitGameplay(envelope); err != nil {
				manager.report(envelope.RoomID, err)
				room.rejectRemoteInput(envelope, err)
			}
		}
	}
}

// rejectRemoteInput sends a remote connection's rejection through the
// runtime's bounded event channel, so misuse cannot cancel a healthy match.
func (room *managedRoom) rejectRemoteInput(envelope coordinationAbstract.MessageEnvelope, err error) {
	var control module.ControlInput
	if json.Unmarshal(envelope.Payload, &control) != nil {
		return
	}
	room.tick.mutex.RLock()
	defer room.tick.mutex.RUnlock()
	if room.tick.runtime != nil {
		room.tick.runtime.NotifyRejected(envelope.ConnectionID, control.MessageID, err)
	}
}

func (room *managedRoom) submitGameplay(envelope coordinationAbstract.MessageEnvelope) error {
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	var control module.ControlInput
	if len(envelope.Payload) > maximumRuntimeInputBytes || json.Unmarshal(envelope.Payload, &control) != nil ||
		control.PlayerID != envelope.ActorID || control.ConnectionID != envelope.ConnectionID || !control.CreatedAt.Equal(envelope.CreatedAt) {
		return module.ErrInvalidInput
	}
	if room.tick.game == nil {
		return ErrRoomRuntimeNotStarted
	}
	if control.Kind == lobbyHeartbeatInput {
		return room.recordLobbyHeartbeat(control)
	}
	room.tick.mutex.RLock()
	runtime := room.tick.runtime
	room.tick.mutex.RUnlock()
	if runtime != nil {
		return runtime.Submit(envelope.Payload, time.Now())
	}
	if control.Kind == module.InputSnapshot {
		return room.publishPreviewSnapshot(control.ConnectionID, control.MessageID)
	}
	return ErrRoomRuntimeNotStarted
}

// syncTick moves the runtime with the durable session: preview during the
// countdown, spawn or recover once running, roster changes while running.
func (room *managedRoom) syncTick(snapshot gameDomain.SessionSnapshot) error {
	room.tick.mutex.Lock()
	defer room.tick.mutex.Unlock()
	switch {
	case snapshot.State == gameDomain.SessionCancelled:
		if room.tick.runtime != nil {
			_ = room.tick.runtime.Cancel(module.EndCancelledByUser)
		}
		return nil
	case room.tick.runtime != nil:
		room.reconcileRuntime(snapshot)
		return nil
	case snapshot.State == gameDomain.SessionCountdown:
		if len(room.lobby.frozen) == 0 {
			room.freezePlayers(snapshot)
		}
		return room.publishPreviewLocked("", "")
	case snapshot.State != gameDomain.SessionRunning:
		return nil
	}
	return room.startRuntimeLocked(snapshot)
}

func (room *managedRoom) reconcileRuntime(snapshot gameDomain.SessionSnapshot) {
	playing, members := room.rosterIDs(snapshot)
	room.tick.runtime.Reconcile(playing, members)
}

// startRuntimeLocked spawns a match once, or recovers one a previous owner
// started. The first checkpoint is protected before the start is recorded.
func (room *managedRoom) startRuntimeLocked(snapshot gameDomain.SessionSnapshot) error {
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	if len(room.lobby.frozen) == 0 {
		room.freezePlayers(snapshot)
	}
	var runtime module.TickRuntime
	var err error
	recovered := false
	if room.runtimeOwner.Started {
		runtime, recovered, err = room.restoreRuntime(snapshot)
	} else if time.Since(snapshot.StartedAt) > room.manager.options.RecoveryMaxAge && room.manager.options.MatchRepository != nil {
		return room.abandonUnstartedMatch(snapshot)
	} else {
		runtime, err = room.tick.game.NewRuntime(module.RuntimeParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: room.lobby.frozen, Epoch: room.runtimeOwner.Generation, Now: time.Now(), InitialSequence: room.tick.previewSequence, Settings: snapshot.Settings})
	}
	if err != nil || runtime == nil {
		return err
	}
	runtime.SetObserver(room.manager.options.Metrics)
	checkpoint := runtime.Checkpoint()
	if recovered {
		frame, err := runtime.RecoveryFrame()
		if err != nil {
			return err
		}
		checkpoint = *frame.Checkpoint
	}
	if err := room.protectCheckpoint(&checkpoint, true); err != nil {
		return err
	}
	if !room.runtimeOwner.Started {
		ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		err := room.manager.owners.MarkRuntimeStarted(ctx, room.runtimeOwner)
		cancel()
		if err != nil {
			return err
		}
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	room.tick.runtime = runtime
	room.reconcileRuntime(snapshot)
	if recovered {
		if err := room.announceRecovery(runtime); err != nil {
			return err
		}
	}
	room.manager.mutex.Lock()
	if room.manager.closed || room.ctx.Err() != nil {
		room.manager.mutex.Unlock()
		return ErrRoomRuntimeStopped
	}
	room.manager.workers.Add(2)
	room.manager.mutex.Unlock()
	go func() { defer room.manager.workers.Done(); runtime.Run(room.ctx, room.leaseValid) }()
	go room.publishTickUpdates(runtime)
	return nil
}

// abandonUnstartedMatch never spawns physics long after the session started:
// the start barrier is recorded and the match ends as a recovery failure.
func (room *managedRoom) abandonUnstartedMatch(snapshot gameDomain.SessionSnapshot) error {
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	err := room.manager.owners.MarkRuntimeStarted(ctx, room.runtimeOwner)
	cancel()
	if err != nil {
		return err
	}
	result, err := room.tick.game.CancellationResult(snapshot, module.EndRecoveryFailed, time.Now())
	if err != nil {
		return err
	}
	room.completion.result = &result
	return nil
}

// announceRecovery applies durable leave/membership changes before showing
// recovered physics, without spending the ownership gap advancing ticks.
func (room *managedRoom) announceRecovery(runtime module.TickRuntime) error {
	if err := runtime.Advance(runtime.Checkpoint().CapturedAt); err != nil {
		return err
	}
	frame, err := runtime.RecoveryFrame()
	if err != nil {
		return err
	}
	if err := room.protectCheckpoint(frame.Checkpoint, true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	err = room.manager.coordinator.PublishRoomEvent(ctx, room.lease, coordinationAbstract.MessageEnvelope{MessageID: uuid.NewString(), Type: GameRuntimeSnapshotEventType, Sequence: frame.Sequence, Payload: frame.Payload})
	if err == nil {
		room.manager.options.Metrics.roomPublishedBytes.Add(uint64(len(frame.Payload)))
	}
	return err
}

// publishTickUpdates fans runtime publications out to every instance. A
// critical checkpoint is protected before the change it covers is announced.
func (room *managedRoom) publishTickUpdates(runtime module.TickRuntime) {
	defer room.manager.workers.Done()
	for {
		update, err := runtime.NextUpdate(room.ctx)
		if err != nil {
			if room.ctx.Err() == nil {
				room.manager.report(room.lease.RoomID, err)
				room.stop()
			}
			return
		}
		event, err := room.tickEvent(update)
		if err != nil {
			room.manager.report(room.lease.RoomID, err)
			room.stop()
			return
		}
		if !room.leaseValid() {
			room.stop()
			return
		}
		if update.Checkpoint != nil {
			if err := room.protectCheckpoint(update.Checkpoint, update.Grant != nil); err != nil {
				if errors.Is(err, coordinationAbstract.ErrCheckpointOrder) {
					continue
				}
				room.manager.report(room.lease.RoomID, err)
				room.stop()
				return
			}
		}
		ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		err = room.manager.coordinator.PublishRoomEvent(ctx, room.lease, event)
		if err == nil {
			room.manager.options.Metrics.roomPublishedBytes.Add(uint64(len(event.Payload)))
		}
		cancel()
		if err != nil {
			room.manager.report(room.lease.RoomID, err)
			room.stop()
			return
		}
	}
}

func (room *managedRoom) tickEvent(update module.Update) (coordinationAbstract.MessageEnvelope, error) {
	event := coordinationAbstract.MessageEnvelope{MessageID: update.MessageID, ConnectionID: update.ConnectionID}
	if event.MessageID == "" {
		event.MessageID = uuid.NewString()
	}
	var err error
	switch {
	case update.Frame != nil:
		event.Type, event.Sequence, event.Payload = GameRuntimeSnapshotEventType, update.Frame.Sequence, update.Frame.Payload
	case update.Grant != nil:
		event.Type = module.ControlGrantedMessageType(room.tick.game.Definition().GameKey)
		event.Payload, err = json.Marshal(update.Grant)
	default:
		event.Type = CommandRejectedMessageType
		event.Payload, err = json.Marshal(tickProtocolError(room.tick.game.Definition().GameKey, update.Err))
	}
	return event, err
}

func (room *managedRoom) publishPreviewSnapshot(connectionID, messageID string) error {
	room.tick.mutex.Lock()
	defer room.tick.mutex.Unlock()
	return room.publishPreviewLocked(connectionID, messageID)
}

// publishPreviewLocked shows the frozen roster at spawn during the countdown.
func (room *managedRoom) publishPreviewLocked(connectionID, messageID string) error {
	snapshot := room.snapshot.Load()
	if snapshot == nil || snapshot.State != gameDomain.SessionCountdown {
		return nil
	}
	if len(room.lobby.frozen) == 0 {
		room.freezePlayers(*snapshot)
	}
	room.tick.previewSequence++
	frame, err := room.tick.game.PreviewFrame(module.PreviewParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, CountdownEndsAt: snapshot.CountdownEndsAt, Players: room.lobby.frozen, Epoch: room.runtimeOwner.Generation, Sequence: room.tick.previewSequence})
	if err != nil {
		return err
	}
	if messageID == "" {
		messageID = uuid.NewString()
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	defer cancel()
	return room.manager.coordinator.PublishRoomEvent(ctx, room.lease, coordinationAbstract.MessageEnvelope{MessageID: messageID, Type: GameRuntimeSnapshotEventType, ConnectionID: connectionID, Sequence: frame.Sequence, Payload: frame.Payload})
}
