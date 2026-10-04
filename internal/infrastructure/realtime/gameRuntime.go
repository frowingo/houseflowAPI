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

	"github.com/google/uuid"
)

const GameplayInputMessageType = "runtime.input"
const GameRuntimeSnapshotEventType = "gameRuntime.snapshot"

// The shared room lifecycle knows only these adapter operations, not rocket
// physics, controls or snapshots. Additional games get their own factory case.
type gameRuntime struct {
	submit         func(coordinationAbstract.MessageEnvelope) error
	cancel         func() error
	results        <-chan houseRockets.HouseRocketsResultModel
	committedFrame func(houseRockets.HouseRocketsResultModel) houseRockets.RuntimeFrame
	reconcile      func(gameDomain.SessionSnapshot)
	reject         func(string, string, error)
	checkpoint     func() houseRockets.RuntimeCheckpoint
}

func supportsGameRuntime(gameKey string) bool { return gameKey == houseRockets.GameKey }

// DispatchGameplay uses memory for a local owner and an ephemeral channel for a
// remote owner. It never creates/restarts a running game or replays old steering.
// actorID/connectionID must be supplied by the authenticated gateway (Paket 4).
func (manager *RoomManager) DispatchGameplay(ctx context.Context, roomID, actorID, connectionID string, input houseRockets.RuntimeInput, messageIDs ...string) error {
	if _, err := manager.runningContext(); err != nil {
		return err
	}
	if roomID == "" || actorID == "" || connectionID == "" {
		return houseRockets.ErrInvalidInput
	}
	input.PlayerID, input.ConnectionID = actorID, connectionID
	input.CreatedAt = time.Now().UTC()
	if len(messageIDs) > 0 {
		input.MessageID = messageIDs[0]
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	envelope := coordinationAbstract.MessageEnvelope{MessageID: uuid.NewString(), RoomID: roomID, Type: GameplayInputMessageType, ActorID: actorID, ConnectionID: connectionID, CreatedAt: input.CreatedAt, Payload: payload}
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
	gameplay, ok := manager.coordinator.(coordinationAbstract.GameplayCoordinator)
	if !ok {
		return coordinationAbstract.ErrUnavailable
	}
	envelope.FencingToken = owner.FencingToken
	return gameplay.PublishGameplay(ctx, owner.OwnerInstanceID, envelope)
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
				var input houseRockets.RuntimeInput
				if json.Unmarshal(envelope.Payload, &input) == nil {
					room.gameMutex.RLock()
					if room.game != nil {
						room.game.reject(envelope.ConnectionID, input.MessageID, err)
					}
					room.gameMutex.RUnlock()
				}
			}
		}
	}
}

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

func (room *managedRoom) submitGameplay(envelope coordinationAbstract.MessageEnvelope) error {
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	var input houseRockets.RuntimeInput
	if json.Unmarshal(envelope.Payload, &input) != nil || input.PlayerID != envelope.ActorID || input.ConnectionID != envelope.ConnectionID || !input.CreatedAt.Equal(envelope.CreatedAt) {
		return houseRockets.ErrInvalidInput
	}
	if input.Kind == lobbyHeartbeatInput {
		if input.RuntimeEpoch != room.runtimeOwner.Generation {
			return houseRockets.ErrStaleInput
		}
		if time.Since(input.CreatedAt) > houseRockets.InputLifetime {
			return houseRockets.ErrInputExpired
		}
		room.lobbyMutex.Lock()
		defer room.lobbyMutex.Unlock()
		if room.lobbyLastSeen == nil {
			room.lobbyLastSeen = make(map[string]time.Time)
		}
		if room.manager.options.ParticipantDirectory != nil {
			if _, member := room.members[input.PlayerID]; !member {
				return houseRockets.ErrControlRequired
			}
		}
		if snapshot := room.snapshot.Load(); snapshot != nil {
			for _, player := range snapshot.Players {
				if player.PlayerID == input.PlayerID {
					room.lobbyLastSeen[input.PlayerID] = time.Now()
					return nil
				}
			}
		}
		return nil
	}
	room.gameMutex.RLock()
	runtime := room.game
	room.gameMutex.RUnlock()
	if runtime == nil {
		if input.Kind == houseRockets.InputSnapshot {
			return room.publishPreviewSnapshot(input.ConnectionID, input.MessageID)
		}
		return ErrRoomRuntimeNotStarted
	}
	return runtime.submit(envelope)
}

// Small factory/lifecycle boundary: no game catalog activation or wire mapping
// happens here. Other game kinds keep their existing session-only runtime.
func (room *managedRoom) syncGameRuntime(snapshot gameDomain.SessionSnapshot, refreshDirectory bool) error {
	if snapshot.GameKey != houseRockets.GameKey {
		return nil
	}
	room.snapshot.Store(&snapshot)
	if refreshDirectory {
		if err := room.refreshParticipants(snapshot); err != nil {
			return err
		}
	}
	room.gameMutex.Lock()
	defer room.gameMutex.Unlock()
	if snapshot.State == gameDomain.SessionCancelled {
		if room.game != nil {
			room.game.cancel()
		}
		return nil
	}
	if room.game != nil {
		room.game.reconcile(snapshot)
		return nil
	}
	if snapshot.State == gameDomain.SessionCountdown {
		if len(room.frozenPlayers) == 0 {
			room.freezePlayers(snapshot)
		}
		return room.publishPreviewLocked("", "")
	}
	if snapshot.State != gameDomain.SessionRunning {
		return nil
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	if len(room.frozenPlayers) == 0 {
		room.freezePlayers(snapshot)
	}
	var runtime *houseRockets.Runtime
	var err error
	recovered := false
	if room.runtimeOwner.Started {
		runtime, recovered, err = room.restoreGameRuntime(snapshot)
	} else {
		if time.Since(snapshot.StartedAt) > room.manager.options.RecoveryMaxAge && room.manager.options.MatchRepository != nil {
			ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
			err := room.manager.repository.(gameAbstract.RuntimeOwnerRepository).MarkRuntimeStarted(ctx, room.runtimeOwner)
			cancel()
			if err != nil {
				return err
			}
			result, err := houseRockets.RecoveryCancellationResult(snapshot, houseRockets.EndRecoveryFailed, time.Now())
			if err != nil {
				return err
			}
			room.pendingResult = &result
			return nil
		}
		runtime, err = houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: room.frozenPlayers}, room.runtimeOwner.Generation, time.Now(), room.previewSequence)
	}
	if err != nil {
		return err
	}
	if runtime == nil {
		return nil
	}
	checkpoint := runtime.Checkpoint()
	if recovered {
		checkpoint = *runtime.RecoveryFrame().Checkpoint
	}
	if err := room.protectCheckpoint(&checkpoint, true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	if !room.runtimeOwner.Started {
		err = room.manager.repository.(gameAbstract.RuntimeOwnerRepository).MarkRuntimeStarted(ctx, room.runtimeOwner)
	}
	cancel()
	if err != nil {
		return err
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	room.game = &gameRuntime{
		results:        runtime.Results(),
		committedFrame: runtime.CommittedFrame,
		checkpoint:     runtime.Checkpoint,
		reject:         runtime.NotifyRejected,
		cancel:         func() error { return runtime.Cancel(houseRockets.EndCancelledByUser) },
		reconcile: func(current gameDomain.SessionSnapshot) {
			playing, members := room.rosterIDs(current)
			runtime.Reconcile(playing, members)
		},
		submit: func(envelope coordinationAbstract.MessageEnvelope) error {
			var input houseRockets.RuntimeInput
			if len(envelope.Payload) > 2048 || json.Unmarshal(envelope.Payload, &input) != nil {
				return houseRockets.ErrInvalidInput
			}
			if input.PlayerID != envelope.ActorID || input.ConnectionID != envelope.ConnectionID || !input.CreatedAt.Equal(envelope.CreatedAt) {
				return houseRockets.ErrInvalidInput
			}
			return runtime.Submit(input, time.Now())
		},
	}
	room.game.reconcile(snapshot)
	if recovered {
		// Apply durable leave/membership changes before announcing recovered
		// physics. Do not spend ownership interruption time advancing ticks.
		if err := runtime.Advance(runtime.Checkpoint().CapturedAt); err != nil {
			return err
		}
		frame := runtime.RecoveryFrame()
		if err := room.protectCheckpoint(frame.Checkpoint, true); err != nil {
			return err
		}
		payload, _ := json.Marshal(frame)
		ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		err := room.manager.coordinator.PublishRoomEvent(ctx, room.lease, coordinationAbstract.MessageEnvelope{MessageID: uuid.NewString(), Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, Payload: payload})
		cancel()
		if err != nil {
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
	go room.publishGameUpdates(runtime)
	return nil
}

func (room *managedRoom) publishGameUpdates(runtime *houseRockets.Runtime) {
	defer room.manager.workers.Done()
	for {
		var event coordinationAbstract.MessageEnvelope
		var checkpoint *houseRockets.RuntimeCheckpoint
		select {
		case <-room.ctx.Done():
			return
		case frame := <-runtime.Frames():
			checkpoint = frame.Checkpoint
			payload, err := json.Marshal(frame)
			if err != nil {
				room.manager.report(room.lease.RoomID, err)
				room.stop()
				return
			}
			event = coordinationAbstract.MessageEnvelope{Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, Payload: payload}
		case update := <-runtime.Events():
			checkpoint = update.Checkpoint
			event.MessageID = update.MessageID
			event.ConnectionID = update.ConnectionID
			if update.Frame != nil {
				checkpoint = update.Frame.Checkpoint
				event.Type = GameRuntimeSnapshotEventType
				event.Sequence = update.Frame.StateSequence
				event.Payload, _ = json.Marshal(update.Frame)
			} else if update.Grant != nil {
				event.Type = houseRockets.ControlGrantedMessageType
				event.Payload, _ = json.Marshal(update.Grant)
			} else {
				event.Type = CommandRejectedMessageType
				event.Payload, _ = json.Marshal(protocolErrorFromV2(update.Err))
			}
		}
		if !room.leaseValid() {
			room.stop()
			return
		}
		if checkpoint != nil {
			if err := room.protectCheckpoint(checkpoint, event.Type == houseRockets.ControlGrantedMessageType); err != nil {
				if errors.Is(err, coordinationAbstract.ErrCheckpointOrder) {
					continue
				}
				room.manager.report(room.lease.RoomID, err)
				room.stop()
				return
			}
		}
		if event.MessageID == "" {
			event.MessageID = uuid.NewString()
		}
		ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
		err := room.manager.coordinator.PublishRoomEvent(ctx, room.lease, event)
		cancel()
		if err != nil {
			room.manager.report(room.lease.RoomID, err)
			room.stop()
			return
		}
	}
}
