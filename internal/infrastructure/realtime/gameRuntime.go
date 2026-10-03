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
	submit func(coordinationAbstract.MessageEnvelope) error
	cancel func()
}

func supportsGameRuntime(gameKey string) bool { return gameKey == houseRockets.GameKey }

// DispatchGameplay uses memory for a local owner and an ephemeral channel for a
// remote owner. It never creates/restarts a running game or replays old steering.
// actorID/connectionID must be supplied by the authenticated gateway (Paket 4).
func (manager *RoomManager) DispatchGameplay(ctx context.Context, roomID, actorID, connectionID string, input houseRockets.RuntimeInput) error {
	if _, err := manager.runningContext(); err != nil {
		return err
	}
	if roomID == "" || actorID == "" || connectionID == "" {
		return houseRockets.ErrInvalidInput
	}
	input.PlayerID, input.ConnectionID = actorID, connectionID
	input.CreatedAt = time.Now().UTC()
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
	room.gameMutex.RLock()
	runtime := room.game
	room.gameMutex.RUnlock()
	if runtime == nil {
		return ErrRoomRuntimeNotStarted
	}
	return runtime.submit(envelope)
}

// Small factory/lifecycle boundary: no game catalog activation or wire mapping
// happens here. Other game kinds keep their existing session-only runtime.
func (room *managedRoom) syncGameRuntime(snapshot gameDomain.SessionSnapshot) error {
	if snapshot.GameKey != houseRockets.GameKey {
		return nil
	}
	room.gameMutex.Lock()
	defer room.gameMutex.Unlock()
	if snapshot.State == gameDomain.SessionCancelled {
		if room.game != nil {
			room.game.cancel()
		}
		return nil
	}
	if snapshot.State != gameDomain.SessionRunning || room.game != nil {
		return nil
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	players := make([]houseRockets.PlayerIdentity, 0, len(snapshot.Players))
	for _, player := range snapshot.Players {
		if player.State == gameDomain.PlayerPlaying {
			// Frozen display names will be resolved by the lobby integration in P4.
			players = append(players, houseRockets.PlayerIdentity{PlayerID: player.PlayerID, DisplayName: player.PlayerID})
		}
	}
	runtime, err := houseRockets.NewRuntime(houseRockets.NewSimulationParams{SessionID: snapshot.SessionID, HouseID: snapshot.HouseID, StartedAt: snapshot.StartedAt, Players: players}, room.runtimeOwner.Generation, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(room.ctx, room.manager.options.CommandTimeout)
	err = room.manager.repository.(gameAbstract.RuntimeOwnerRepository).MarkRuntimeStarted(ctx, room.runtimeOwner)
	cancel()
	if err != nil {
		return err
	}
	if !room.leaseValid() {
		return coordinationAbstract.ErrLeaseLost
	}
	room.manager.mutex.Lock()
	if room.manager.closed || room.ctx.Err() != nil {
		room.manager.mutex.Unlock()
		return ErrRoomRuntimeStopped
	}
	room.manager.workers.Add(2)
	room.manager.mutex.Unlock()
	room.game = &gameRuntime{
		cancel: func() { _ = runtime.Cancel(houseRockets.EndCancelledByUser) },
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
	go func() { defer room.manager.workers.Done(); runtime.Run(room.ctx, room.leaseValid) }()
	go room.publishGameUpdates(runtime)
	return nil
}

func (room *managedRoom) publishGameUpdates(runtime *houseRockets.Runtime) {
	defer room.manager.workers.Done()
	for {
		var event coordinationAbstract.MessageEnvelope
		select {
		case <-room.ctx.Done():
			return
		case frame := <-runtime.Frames():
			payload, err := json.Marshal(frame)
			if err != nil {
				room.manager.report(room.lease.RoomID, err)
				room.stop()
				return
			}
			event = coordinationAbstract.MessageEnvelope{Type: GameRuntimeSnapshotEventType, Sequence: frame.StateSequence, Payload: payload}
		case update := <-runtime.Events():
			event.ConnectionID = update.ConnectionID
			if update.Grant != nil {
				event.Type = houseRockets.ControlGrantedMessageType
				event.Payload, _ = json.Marshal(update.Grant)
			} else {
				event.Type = CommandRejectedMessageType
				code := houseRockets.InvalidInputErrorCode
				if errors.Is(update.Err, houseRockets.ErrControlRequired) {
					code = houseRockets.ControlUnavailableErrorCode
				}
				if errors.Is(update.Err, houseRockets.ErrPlayerEliminated) {
					code = houseRockets.PlayerEliminatedErrorCode
				}
				event.Payload, _ = json.Marshal(ProtocolError{Code: code})
			}
		}
		if !room.leaseValid() {
			room.stop()
			return
		}
		event.MessageID = uuid.NewString()
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
