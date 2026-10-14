package realtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/application/game/module"
	"houseflowApi/internal/infrastructure/cqrs"
)

const (
	GameSessionSnapshotEventType = "gameSession.snapshot"
	defaultLeaseTTL              = 10 * time.Second
	defaultRenewInterval         = 3 * time.Second
	defaultCommandTimeout        = 2 * time.Second
	defaultCommandQueueSize      = 64
	defaultMaxCommandAttempts    = 3
	defaultRetryDelay            = 50 * time.Millisecond
	roomCleanupTimeout           = 2 * time.Second
)

var (
	ErrRoomRuntimeNotStarted = errors.New("room runtime is not started")
	ErrRoomRuntimeStopped    = errors.New("room runtime is stopped")
	ErrTerminalRoom          = errors.New("terminal game session cannot own a realtime room")
	ErrRoomCommandQueueFull  = errors.New("room command queue is full")
	ErrRealtimeCapacity      = errors.New("realtime instance capacity reached")
)

type RoomManagerOptions struct {
	// Games resolves the tick module of a session; sessions of other games run
	// the shared lobby lifecycle only.
	Games                *module.Registry
	LeaseTTL             time.Duration
	RenewInterval        time.Duration
	CommandTimeout       time.Duration
	CommandQueueSize     int
	MaxCommandAttempts   int
	RetryDelay           time.Duration
	ParticipantDirectory gameAbstract.GameParticipantDirectory
	ReconcileInterval    time.Duration
	MatchRepository      gameAbstract.GameMatchRepository
	CheckpointInterval   time.Duration
	CheckpointTTL        time.Duration
	RecoveryMaxAge       time.Duration
	RecoveryScanInterval time.Duration
	RecoveryScanBatch    int
	FinalizationTimeout  time.Duration
	IdleRoomTimeout      time.Duration
	MaxOwnedRooms        int
	Metrics              *Metrics
}

type RoomOwnership struct {
	RoomID          string
	OwnerInstanceID string
	FencingToken    int64
	Local           bool
	RuntimeEpoch    int64
}

type RuntimeError struct {
	RoomID string
	Err    error
}

type RoomManager struct {
	coordinator coordinationAbstract.Coordinator
	repository  gameAbstract.GameSessionRepository
	sender      cqrs.Sender
	processor   commandProcessor
	options     RoomManagerOptions
	// Tick-game capabilities, resolved once at construction.
	owners      gameAbstract.RuntimeOwnerRepository
	gameplay    coordinationAbstract.GameplayCoordinator
	checkpoints coordinationAbstract.RuntimeCheckpointStore
	scanner     gameAbstract.ActiveGameSessionScanner

	mutex                sync.RWMutex
	rooms                map[string]*managedRoom
	activations          map[string]*roomActivation
	startingRooms        int
	started              bool
	closed               bool
	ctx                  context.Context
	cancel               context.CancelFunc
	subscription         coordinationAbstract.CommandSubscription
	gameplaySubscription coordinationAbstract.RoomEventSubscription
	errors               chan RuntimeError
	workers              sync.WaitGroup
	// local is this instance's gateway, attached when the gateway is built.
	local atomic.Value
}

type audienceHolder struct{ audience localAudience }

func (manager *RoomManager) attachAudience(audience localAudience) {
	manager.local.Store(audienceHolder{audience})
}

func (manager *RoomManager) audience() localAudience {
	holder, _ := manager.local.Load().(audienceHolder)
	return holder.audience
}

// track registers a background worker unless the manager is closing.
func (manager *RoomManager) track() bool {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.closed {
		return false
	}
	manager.workers.Add(1)
	return true
}

func NewRoomManager(
	coordinator coordinationAbstract.Coordinator,
	repository gameAbstract.GameSessionRepository,
	sender cqrs.Sender,
	options RoomManagerOptions,
) (*RoomManager, error) {
	if coordinator == nil {
		return nil, errors.New("room coordinator is required")
	}
	if repository == nil {
		return nil, errors.New("game session repository is required")
	}
	if sender == nil {
		return nil, errors.New("CQRS sender is required")
	}
	options = normalizeOptions(options)
	if options.MaxOwnedRooms < 0 {
		return nil, errors.New("maximum owned rooms must not be negative")
	}
	if options.RecoveryScanBatch > 1000 || options.CheckpointTTL <= options.CheckpointInterval || options.IdleRoomTimeout < 2*time.Millisecond {
		return nil, errors.New("invalid checkpoint TTL, recovery scan batch or idle room timeout")
	}
	if options.RenewInterval >= options.LeaseTTL {
		return nil, errors.New("room renew interval must be shorter than lease TTL")
	}
	maximumProcessingTime := options.CommandTimeout*time.Duration(options.MaxCommandAttempts) +
		options.RetryDelay*time.Duration(options.MaxCommandAttempts-1)
	if options.RenewInterval+maximumProcessingTime >= options.LeaseTTL {
		return nil, errors.New("room lease TTL must cover command retries and the renew interval")
	}
	manager := &RoomManager{
		coordinator: coordinator,
		repository:  repository,
		sender:      sender,
		processor:   newCommandProcessor(sender),
		options:     options,
		rooms:       make(map[string]*managedRoom),
		activations: make(map[string]*roomActivation),
		errors:      make(chan RuntimeError, 32),
	}
	if options.Games.HasTickGames() {
		if err := manager.resolveTickCapabilities(); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

// Tick games need durable owner fencing, cross-instance gameplay input and a
// checkpoint store; a missing capability fails construction, not a match.
func (manager *RoomManager) resolveTickCapabilities() error {
	var ok bool
	if manager.owners, ok = manager.repository.(gameAbstract.RuntimeOwnerRepository); !ok {
		return errors.New("tick games require a durable runtime owner repository")
	}
	if manager.gameplay, ok = manager.coordinator.(coordinationAbstract.GameplayCoordinator); !ok {
		return errors.New("tick games require a gameplay coordinator")
	}
	if manager.checkpoints, ok = manager.coordinator.(coordinationAbstract.RuntimeCheckpointStore); !ok {
		return errors.New("tick games require a runtime checkpoint store")
	}
	manager.scanner, _ = manager.repository.(gameAbstract.ActiveGameSessionScanner)
	return nil
}

// tickGame returns the realtime module of a game, or nil for lobby-only games.
func (manager *RoomManager) tickGame(gameKey string) module.TickModule {
	game, _ := manager.options.Games.Tick(gameKey)
	return game
}

func (manager *RoomManager) Start(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if manager.closed {
		return ErrRoomRuntimeStopped
	}
	if manager.started {
		return nil
	}
	rootCtx, cancel := context.WithCancel(ctx)
	manager.ctx = rootCtx
	manager.cancel = cancel

	subscription, err := manager.coordinator.SubscribeCommands(rootCtx)
	if err != nil {
		manager.started = false
		manager.ctx = nil
		manager.cancel = nil
		cancel()
		return err
	}
	if manager.gameplay != nil {
		gameplaySubscription, err := manager.gameplay.SubscribeGameplay(rootCtx)
		if err != nil {
			_ = subscription.Close()
			cancel()
			manager.started = false
			return err
		}
		manager.gameplaySubscription = gameplaySubscription
		manager.workers.Add(1)
		go manager.gameplayLoop(rootCtx, gameplaySubscription)
	}
	manager.subscription = subscription
	manager.started = true
	manager.workers.Add(1)
	go manager.commandLoop(rootCtx, subscription)
	if manager.scanner != nil && manager.options.MatchRepository != nil && manager.options.RecoveryScanInterval > 0 {
		manager.workers.Add(1)
		go manager.recoveryLoop(rootCtx, manager.scanner)
	}
	return nil
}

func (manager *RoomManager) Errors() <-chan RuntimeError {
	return manager.errors
}

func (manager *RoomManager) EnsureRoom(
	ctx context.Context,
	roomID string,
) (RoomOwnership, error) {
	if roomID == "" {
		return RoomOwnership{}, errors.New("room ID is required")
	}
	for {
		rootCtx, err := manager.runningContext()
		if err != nil {
			return RoomOwnership{}, err
		}
		manager.mutex.Lock()
		if manager.closed || !manager.started {
			manager.mutex.Unlock()
			return RoomOwnership{}, ErrRoomRuntimeStopped
		}
		if room := manager.rooms[roomID]; room != nil {
			manager.mutex.Unlock()
			if snapshot := room.snapshot.Load(); snapshot != nil && (snapshot.State == gameDomain.SessionFinished || snapshot.State == gameDomain.SessionCancelled) {
				return RoomOwnership{}, ErrTerminalRoom
			}
			if !room.leaseValid() {
				room.stop()
				return RoomOwnership{}, coordinationAbstract.ErrLeaseLost
			}
			return room.ownership(), nil
		}
		if activation := manager.activations[roomID]; activation != nil {
			manager.mutex.Unlock()
			select {
			case <-activation.done:
				continue
			case <-ctx.Done():
				return RoomOwnership{}, ctx.Err()
			}
		}
		activation := &roomActivation{done: make(chan struct{})}
		manager.activations[roomID] = activation
		manager.workers.Add(1)
		manager.mutex.Unlock()

		ownership, activationErr := manager.activateRoom(ctx, rootCtx, roomID)
		manager.mutex.Lock()
		if manager.activations[roomID] == activation {
			delete(manager.activations, roomID)
			close(activation.done)
		}
		manager.mutex.Unlock()
		manager.workers.Done()
		return ownership, activationErr
	}
}

func (manager *RoomManager) activateRoom(
	ctx context.Context,
	rootCtx context.Context,
	roomID string,
) (RoomOwnership, error) {
	// Capture durable generation before requesting a lease. A delayed claimant
	// must never reload this value and retry CAS against a newer owner.
	session, err := manager.repository.FindByID(ctx, roomID)
	if err != nil {
		return RoomOwnership{}, err
	}
	var expectedOwner gameAbstract.RuntimeOwner
	if manager.tickGame(session.Snapshot().GameKey) != nil {
		expectedOwner, err = manager.owners.FindRuntimeOwner(ctx, roomID)
		if err != nil {
			return RoomOwnership{}, err
		}
	}
	acquireStartedAt := time.Now()
	lease, acquired, err := manager.coordinator.AcquireRoom(ctx, roomID, manager.options.LeaseTTL)
	lease.ExpiresAt = acquireStartedAt.Add(manager.options.LeaseTTL)
	if err != nil {
		return RoomOwnership{}, err
	}
	if !acquired {
		owner, exists, err := manager.coordinator.CurrentRoomOwner(ctx, roomID)
		if err != nil {
			return RoomOwnership{}, err
		}
		if !exists {
			return RoomOwnership{}, coordinationAbstract.ErrUnavailable
		}
		if owner.OwnerInstanceID == manager.coordinator.InstanceID() {
			if _, err := manager.coordinator.ReleaseRoom(ctx, owner); err != nil {
				return RoomOwnership{}, err
			}
			lease, acquired, err = manager.coordinator.AcquireRoom(ctx, roomID, manager.options.LeaseTTL)
			if err != nil {
				return RoomOwnership{}, err
			}
			if acquired {
				return manager.startRoom(ctx, rootCtx, lease, expectedOwner)
			}
			owner, exists, err = manager.coordinator.CurrentRoomOwner(ctx, roomID)
			if err != nil {
				return RoomOwnership{}, err
			}
			if !exists {
				return RoomOwnership{}, coordinationAbstract.ErrUnavailable
			}
		}
		ownership := ownershipFromLease(owner, false)
		if manager.tickGame(session.Snapshot().GameKey) != nil {
			current, err := manager.waitForRuntimeOwner(ctx, owner)
			if err != nil {
				return RoomOwnership{}, err
			}
			ownership.RuntimeEpoch = current.Generation
		}
		return ownership, nil
	}
	return manager.startRoom(ctx, rootCtx, lease, expectedOwner)
}

// Redis ownership becomes visible before the owner publishes its durable Mongo
// generation. A concurrent gateway may observe that gap; wait for publication
// under the same live lease without claiming or retrying another owner's CAS.
func (manager *RoomManager) waitForRuntimeOwner(ctx context.Context, lease coordinationAbstract.RoomLease) (gameAbstract.RuntimeOwner, error) {
	ctx, cancel := context.WithTimeout(ctx, manager.options.CommandTimeout)
	defer cancel()
	ticker := time.NewTicker(defaultRetryDelay)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return gameAbstract.RuntimeOwner{}, err
		}
		current, err := manager.owners.FindRuntimeOwner(ctx, lease.RoomID)
		if err != nil {
			return gameAbstract.RuntimeOwner{}, err
		}
		activeLease, exists, err := manager.coordinator.CurrentRoomOwner(ctx, lease.RoomID)
		if err != nil {
			return gameAbstract.RuntimeOwner{}, err
		}
		if !exists || activeLease.LeaseID != lease.LeaseID || activeLease.OwnerInstanceID != lease.OwnerInstanceID {
			return gameAbstract.RuntimeOwner{}, coordinationAbstract.ErrLeaseLost
		}
		if current.Generation > 0 && current.LeaseID == lease.LeaseID && current.OwnerInstanceID == lease.OwnerInstanceID {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return gameAbstract.RuntimeOwner{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (manager *RoomManager) startRoom(
	ctx context.Context,
	rootCtx context.Context,
	lease coordinationAbstract.RoomLease,
	expectedOwner gameAbstract.RuntimeOwner,
) (RoomOwnership, error) {
	roomID := lease.RoomID
	// Reserve only local ownership after lease acquisition. A saturated instance
	// may still serve sockets/input for a room owned by another instance.
	manager.mutex.Lock()
	if manager.closed || !manager.started {
		manager.mutex.Unlock()
		manager.releaseLease(lease)
		return RoomOwnership{}, ErrRoomRuntimeStopped
	}
	if manager.options.MaxOwnedRooms > 0 && len(manager.rooms)+manager.startingRooms >= manager.options.MaxOwnedRooms {
		manager.mutex.Unlock()
		manager.releaseLease(lease)
		manager.options.Metrics.admissionRejected.Add(1)
		return RoomOwnership{}, ErrRealtimeCapacity
	}
	manager.startingRooms++
	manager.mutex.Unlock()
	defer func() { manager.mutex.Lock(); manager.startingRooms--; manager.mutex.Unlock() }()
	session, err := manager.repository.FindByID(ctx, roomID)
	if err != nil {
		manager.releaseLease(lease)
		return RoomOwnership{}, err
	}
	snapshot := session.Snapshot()
	if snapshot.State == gameDomain.SessionFinished || snapshot.State == gameDomain.SessionCancelled {
		manager.releaseLease(lease)
		return RoomOwnership{}, ErrTerminalRoom
	}
	game := manager.tickGame(snapshot.GameKey)
	var runtimeOwner gameAbstract.RuntimeOwner
	if game != nil {
		runtimeOwner, err = manager.owners.ClaimRuntimeOwner(ctx, expectedOwner.Generation, gameAbstract.RuntimeOwner{
			SessionID: roomID, OwnerInstanceID: lease.OwnerInstanceID, LeaseID: lease.LeaseID,
		})
		if err != nil {
			manager.releaseLease(lease)
			return RoomOwnership{}, err
		}
	}
	renewStartedAt := time.Now()
	renewed, err := manager.coordinator.RenewRoom(ctx, lease, manager.options.LeaseTTL)
	if err != nil || !renewed {
		manager.releaseLease(lease)
		return RoomOwnership{}, errors.Join(coordinationAbstract.ErrLeaseLost, err)
	}
	lease.ExpiresAt = renewStartedAt.Add(manager.options.LeaseTTL)

	roomCtx, roomCancel := context.WithCancel(rootCtx)
	room := &managedRoom{
		manager:      manager,
		ctx:          roomCtx,
		cancel:       roomCancel,
		lease:        lease,
		queue:        make(chan coordinationAbstract.CommandDelivery, manager.options.CommandQueueSize),
		runtimeOwner: runtimeOwner,
		tick:         tickState{game: game},
	}
	room.leaseDeadline.Store(&lease.ExpiresAt)
	if err := room.syncSession(snapshot, true); err != nil {
		roomCancel()
		manager.releaseLease(lease)
		return RoomOwnership{}, err
	}
	manager.mutex.Lock()
	if manager.closed || !manager.started {
		manager.mutex.Unlock()
		roomCancel()
		manager.releaseLease(lease)
		return RoomOwnership{}, ErrRoomRuntimeStopped
	}
	if existing := manager.rooms[roomID]; existing != nil {
		manager.mutex.Unlock()
		roomCancel()
		manager.releaseLease(lease)
		return existing.ownership(), nil
	}
	manager.rooms[roomID] = room
	manager.workers.Add(2)
	manager.mutex.Unlock()
	go room.renewLoop()
	go room.run(snapshot)
	return room.ownership(), nil
}

func (manager *RoomManager) Dispatch(
	ctx context.Context,
	envelope coordinationAbstract.MessageEnvelope,
) error {
	if envelope.MessageID == "" || envelope.RoomID == "" || envelope.Type == "" {
		return errors.New("message ID, room ID and type are required")
	}
	if envelope.ActorID == "" {
		return errors.New("authenticated actor ID is required")
	}
	if _, err := manager.runningContext(); err != nil {
		return err
	}
	owner, exists, err := manager.coordinator.CurrentRoomOwner(ctx, envelope.RoomID)
	if err != nil {
		return err
	}
	if !exists {
		ownership, err := manager.EnsureRoom(ctx, envelope.RoomID)
		if err != nil {
			return err
		}
		owner.OwnerInstanceID = ownership.OwnerInstanceID
	}
	return manager.coordinator.PublishCommand(ctx, owner.OwnerInstanceID, envelope)
}

func (manager *RoomManager) Close(ctx context.Context) error {
	manager.mutex.Lock()
	if manager.closed {
		manager.mutex.Unlock()
		return nil
	}
	manager.closed = true
	manager.started = false
	cancel := manager.cancel
	subscription := manager.subscription
	gameplaySubscription := manager.gameplaySubscription
	rooms := make([]*managedRoom, 0, len(manager.rooms))
	for _, room := range manager.rooms {
		rooms = append(rooms, room)
	}
	manager.mutex.Unlock()

	if cancel != nil {
		cancel()
	}
	if subscription != nil {
		_ = subscription.Close()
	}
	if gameplaySubscription != nil {
		_ = gameplaySubscription.Close()
	}
	for _, room := range rooms {
		room.stop()
	}

	done := make(chan struct{})
	go func() {
		manager.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (manager *RoomManager) commandLoop(
	ctx context.Context,
	subscription coordinationAbstract.CommandSubscription,
) {
	defer manager.workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case err, open := <-subscription.Errors():
			if open && err != nil {
				manager.report("", err)
			}
		case delivery, open := <-subscription.Deliveries():
			if !open {
				return
			}
			if err := manager.routeDelivery(ctx, delivery); err != nil {
				manager.report(delivery.Envelope().RoomID, err)
			}
		}
	}
}

func (manager *RoomManager) routeDelivery(
	ctx context.Context,
	delivery coordinationAbstract.CommandDelivery,
) error {
	envelope := delivery.Envelope()
	room := manager.localRoom(envelope.RoomID)
	if room == nil {
		ownership, err := manager.EnsureRoom(ctx, envelope.RoomID)
		if err != nil {
			if errors.Is(err, gameAbstract.ErrGameSessionNotFound) || errors.Is(err, ErrTerminalRoom) {
				return errors.Join(err, delivery.Ack(ctx))
			}
			return err
		}
		if !ownership.Local {
			envelope.SourceInstanceID = ""
			if err := manager.coordinator.PublishCommand(ctx, ownership.OwnerInstanceID, envelope); err != nil {
				return err
			}
			return delivery.Ack(ctx)
		}
		room = manager.localRoom(envelope.RoomID)
		if room == nil {
			return coordinationAbstract.ErrLeaseLost
		}
	}
	select {
	case room.queue <- delivery:
		return nil
	case <-room.ctx.Done():
		return coordinationAbstract.ErrLeaseLost
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrRoomCommandQueueFull
	}
}

func (manager *RoomManager) runningContext() (context.Context, error) {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	if manager.closed {
		return nil, ErrRoomRuntimeStopped
	}
	if !manager.started || manager.ctx == nil {
		return nil, ErrRoomRuntimeNotStarted
	}
	if manager.ctx.Err() != nil {
		return nil, ErrRoomRuntimeStopped
	}
	return manager.ctx, nil
}

func (manager *RoomManager) localRoom(roomID string) *managedRoom {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	return manager.rooms[roomID]
}

func (manager *RoomManager) removeRoom(room *managedRoom) {
	manager.mutex.Lock()
	if manager.rooms[room.lease.RoomID] == room {
		delete(manager.rooms, room.lease.RoomID)
	}
	manager.mutex.Unlock()
}

func (manager *RoomManager) releaseLease(lease coordinationAbstract.RoomLease) {
	ctx, cancel := context.WithTimeout(context.Background(), roomCleanupTimeout)
	defer cancel()
	_, _ = manager.coordinator.ReleaseRoom(ctx, lease)
}

func (manager *RoomManager) report(roomID string, err error) {
	manager.options.Metrics.errors.Add(1)
	select {
	case manager.errors <- RuntimeError{RoomID: roomID, Err: err}:
	default:
	}
}

func normalizeOptions(options RoomManagerOptions) RoomManagerOptions {
	if options.Metrics == nil {
		options.Metrics = &Metrics{}
	}
	if options.LeaseTTL <= 0 {
		options.LeaseTTL = defaultLeaseTTL
	}
	if options.RenewInterval <= 0 {
		options.RenewInterval = defaultRenewInterval
	}
	if options.CommandTimeout <= 0 {
		options.CommandTimeout = defaultCommandTimeout
	}
	if options.CommandQueueSize <= 0 {
		options.CommandQueueSize = defaultCommandQueueSize
	}
	if options.MaxCommandAttempts <= 0 {
		options.MaxCommandAttempts = defaultMaxCommandAttempts
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = defaultRetryDelay
	}
	if options.CheckpointInterval <= 0 {
		options.CheckpointInterval = defaultCheckpointInterval
	}
	if options.CheckpointTTL <= 0 {
		options.CheckpointTTL = defaultCheckpointTTL
	}
	if options.RecoveryMaxAge <= 0 {
		options.RecoveryMaxAge = defaultRecoveryMaxAge
	}
	if options.RecoveryScanInterval == 0 {
		options.RecoveryScanInterval = defaultRecoveryScanInterval
	}
	if options.RecoveryScanBatch <= 0 {
		options.RecoveryScanBatch = defaultRecoveryScanBatch
	}
	if options.FinalizationTimeout <= 0 {
		options.FinalizationTimeout = defaultFinalizationTimeout
	}
	if options.IdleRoomTimeout <= 0 {
		options.IdleRoomTimeout = time.Minute
	}
	return options
}

func ownershipFromLease(lease coordinationAbstract.RoomLease, local bool) RoomOwnership {
	return RoomOwnership{
		RoomID:          lease.RoomID,
		OwnerInstanceID: lease.OwnerInstanceID,
		FencingToken:    lease.FencingToken,
		Local:           local,
	}
}
