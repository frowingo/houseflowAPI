package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	"houseflowApi/internal/helpers"
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
)

type RoomManagerOptions struct {
	LeaseTTL             time.Duration
	RenewInterval        time.Duration
	CommandTimeout       time.Duration
	CommandQueueSize     int
	MaxCommandAttempts   int
	RetryDelay           time.Duration
	ParticipantDirectory gameAbstract.GameParticipantDirectory
	ReconcileInterval    time.Duration
	MatchRepository      gameAbstract.GameMatchRepository
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

	mutex                sync.RWMutex
	rooms                map[string]*managedRoom
	activations          map[string]*roomActivation
	started              bool
	closed               bool
	ctx                  context.Context
	cancel               context.CancelFunc
	subscription         coordinationAbstract.CommandSubscription
	gameplaySubscription coordinationAbstract.RoomEventSubscription
	errors               chan RuntimeError
	workers              sync.WaitGroup
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
	if options.RenewInterval >= options.LeaseTTL {
		return nil, errors.New("room renew interval must be shorter than lease TTL")
	}
	maximumProcessingTime := options.CommandTimeout*time.Duration(options.MaxCommandAttempts) +
		options.RetryDelay*time.Duration(options.MaxCommandAttempts-1)
	if options.RenewInterval+maximumProcessingTime >= options.LeaseTTL {
		return nil, errors.New("room lease TTL must cover command retries and the renew interval")
	}
	return &RoomManager{
		coordinator: coordinator,
		repository:  repository,
		sender:      sender,
		processor:   newCommandProcessor(sender),
		options:     options,
		rooms:       make(map[string]*managedRoom),
		activations: make(map[string]*roomActivation),
		errors:      make(chan RuntimeError, 32),
	}, nil
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
	if gameplay, ok := manager.coordinator.(coordinationAbstract.GameplayCoordinator); ok {
		gameplaySubscription, err := gameplay.SubscribeGameplay(rootCtx)
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
		if room := manager.rooms[roomID]; room != nil {
			manager.mutex.Unlock()
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
		manager.mutex.Unlock()

		ownership, activationErr := manager.activateRoom(ctx, rootCtx, roomID)
		manager.mutex.Lock()
		if manager.activations[roomID] == activation {
			delete(manager.activations, roomID)
			close(activation.done)
		}
		manager.mutex.Unlock()
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
	if supportsGameRuntime(session.Snapshot().GameKey) {
		owners, ok := manager.repository.(gameAbstract.RuntimeOwnerRepository)
		if !ok {
			return RoomOwnership{}, errors.New("durable runtime owner repository is required")
		}
		if _, ok := manager.coordinator.(coordinationAbstract.GameplayCoordinator); !ok {
			return RoomOwnership{}, errors.New("gameplay coordinator is required")
		}
		expectedOwner, err = owners.FindRuntimeOwner(ctx, roomID)
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
		if supportsGameRuntime(session.Snapshot().GameKey) {
			current, err := manager.repository.(gameAbstract.RuntimeOwnerRepository).FindRuntimeOwner(ctx, roomID)
			if err != nil {
				return RoomOwnership{}, err
			}
			if current.LeaseID != owner.LeaseID || current.OwnerInstanceID != owner.OwnerInstanceID {
				return RoomOwnership{}, coordinationAbstract.ErrUnavailable
			}
			ownership.RuntimeEpoch = current.Generation
		}
		return ownership, nil
	}
	return manager.startRoom(ctx, rootCtx, lease, expectedOwner)
}

func (manager *RoomManager) startRoom(
	ctx context.Context,
	rootCtx context.Context,
	lease coordinationAbstract.RoomLease,
	expectedOwner gameAbstract.RuntimeOwner,
) (RoomOwnership, error) {
	roomID := lease.RoomID
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
	var runtimeOwner gameAbstract.RuntimeOwner
	if supportsGameRuntime(snapshot.GameKey) {
		runtimeOwner, err = manager.repository.(gameAbstract.RuntimeOwnerRepository).ClaimRuntimeOwner(ctx, expectedOwner.Generation, gameAbstract.RuntimeOwner{
			SessionID: roomID, OwnerInstanceID: lease.OwnerInstanceID, LeaseID: lease.LeaseID,
		})
		if err != nil {
			manager.releaseLease(lease)
			return RoomOwnership{}, err
		}
		if runtimeOwner.Started {
			manager.releaseLease(lease)
			return RoomOwnership{}, gameAbstract.ErrRuntimeRecoveryRequired
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
	}
	room.leaseDeadline.Store(&lease.ExpiresAt)
	if err := room.syncGameRuntime(snapshot, true); err != nil {
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
	select {
	case manager.errors <- RuntimeError{RoomID: roomID, Err: err}:
	default:
	}
}

func normalizeOptions(options RoomManagerOptions) RoomManagerOptions {
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

type managedRoom struct {
	manager              *RoomManager
	ctx                  context.Context
	cancel               context.CancelFunc
	lease                coordinationAbstract.RoomLease
	queue                chan coordinationAbstract.CommandDelivery
	stopOnce             sync.Once
	leaseDeadline        atomic.Pointer[time.Time]
	runtimeOwner         gameAbstract.RuntimeOwner
	gameMutex            sync.RWMutex
	game                 *gameRuntime
	snapshot             atomic.Pointer[gameDomain.SessionSnapshot]
	lobbyMutex           sync.Mutex
	lobbyLastSeen        map[string]time.Time
	members              map[string]string
	frozenPlayers        []gameParticipant
	previewSequence      int64
	pendingResult        *houseRockets.HouseRocketsResultModel
	pendingCancellations []coordinationAbstract.CommandDelivery
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

func (room *managedRoom) run(snapshot gameDomain.SessionSnapshot) {
	defer room.manager.workers.Done()
	defer room.manager.removeRoom(room)
	defer room.manager.releaseLease(room.lease)
	defer room.stop()
	var deadlineTimer *time.Timer
	var deadlineChannel <-chan time.Time
	resetDeadline := func(current gameDomain.SessionSnapshot) {
		if deadlineTimer != nil {
			if !deadlineTimer.Stop() {
				select {
				case <-deadlineTimer.C:
				default:
				}
			}
		}
		deadline, exists := sessionDeadline(current)
		if !exists {
			deadlineTimer = nil
			deadlineChannel = nil
			return
		}
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		deadlineTimer = time.NewTimer(delay)
		deadlineChannel = deadlineTimer.C
	}
	resetDeadline(snapshot)
	var completionChannel <-chan time.Time
	if room.manager.options.MatchRepository != nil && supportsGameRuntime(snapshot.GameKey) {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		completionChannel = ticker.C
	}
	var reconcileChannel <-chan time.Time
	if room.manager.options.ParticipantDirectory != nil && supportsGameRuntime(snapshot.GameKey) {
		interval := room.manager.options.ReconcileInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		reconcileChannel = ticker.C
	}
	defer func() {
		if deadlineTimer != nil {
			deadlineTimer.Stop()
		}
	}()

	for {
		room.captureResult()
		select {
		case <-room.ctx.Done():
			return
		case <-completionChannel:
			room.captureResult()
			if room.pendingResult != nil && room.completeMatch() {
				return
			}
		case <-reconcileChannel:
			room.captureResult()
			if room.pendingResult != nil {
				continue
			}
			updated, err := room.reconcilePlayers(snapshot, time.Now().UTC())
			if err != nil {
				room.manager.report(room.lease.RoomID, err)
				room.stop()
				return
			}
			changed := updated.Version != snapshot.Version
			snapshot = updated
			if err := room.syncGameRuntime(snapshot, false); err != nil {
				room.manager.report(room.lease.RoomID, err)
				return
			}
			resetDeadline(snapshot)
			if changed {
				if err := room.publishSnapshot("", snapshot); err != nil {
					room.manager.report(room.lease.RoomID, err)
					return
				}
			}
			if snapshot.State == gameDomain.SessionCancelled || snapshot.State == gameDomain.SessionFinished {
				return
			}
		case delivery := <-room.queue:
			envelope := delivery.Envelope()
			room.captureResult()
			if room.pendingResult != nil {
				if len(room.pendingCancellations) > 0 {
					first := room.pendingCancellations[0].Envelope()
					if envelope.MessageID == first.MessageID && envelope.ActorID == first.ActorID && envelope.Type == first.Type {
						if len(room.pendingCancellations) < room.manager.options.CommandQueueSize {
							room.pendingCancellations = append(room.pendingCancellations, delivery)
						}
						continue
					}
				}
				_ = room.publishCommandRejected(envelope, helpers.NewConflictError("houseRockets.error.finalizing"))
				_ = delivery.Ack(room.ctx)
				continue
			}
			if snapshot.GameKey == houseRockets.GameKey && snapshot.State == gameDomain.SessionRunning && envelope.Type == gameCommands.CancelGameSessionCommandType && room.manager.options.MatchRepository != nil {
				if err := room.requestMatchCancellation(envelope); err != nil {
					_ = room.publishCommandRejected(envelope, err)
					if !isRetryableCommandError(err) {
						_ = delivery.Ack(room.ctx)
					}
				} else {
					room.pendingCancellations = append(room.pendingCancellations, delivery)
					room.captureResult()
				}
				continue
			}
			updated, err := room.processCommand(envelope)
			if err != nil {
				room.manager.report(room.lease.RoomID, err)
				if !isRetryableCommandError(err) {
					if rejectionErr := room.publishCommandRejected(envelope, err); rejectionErr != nil {
						room.manager.report(room.lease.RoomID, rejectionErr)
						if errors.Is(rejectionErr, coordinationAbstract.ErrLeaseLost) {
							return
						}
					}
					_ = delivery.Ack(room.ctx)
				}
				continue
			}
			snapshot = updated
			if err := room.syncGameRuntime(snapshot, true); err != nil {
				room.manager.report(room.lease.RoomID, err)
				return
			}
			resetDeadline(snapshot)
			publishErr := room.publishSnapshot(delivery.Envelope().MessageID, updated)
			if publishErr != nil {
				room.manager.report(room.lease.RoomID, publishErr)
			}
			if err := delivery.Ack(room.ctx); err != nil {
				room.manager.report(room.lease.RoomID, err)
			}
			if errors.Is(publishErr, coordinationAbstract.ErrLeaseLost) ||
				errors.Is(publishErr, coordinationAbstract.ErrUnavailable) {
				return
			}
			if snapshot.State == gameDomain.SessionFinished || snapshot.State == gameDomain.SessionCancelled {
				return
			}
		case <-deadlineChannel:
			deadline, exists := sessionDeadline(snapshot)
			if !exists {
				resetDeadline(snapshot)
				continue
			}
			current, reconcileErr := room.reconcilePlayers(snapshot, deadline)
			if reconcileErr != nil {
				room.manager.report(room.lease.RoomID, reconcileErr)
				return
			}
			if current.Version != snapshot.Version {
				snapshot = current
				if err := room.syncGameRuntime(snapshot, false); err != nil {
					room.manager.report(room.lease.RoomID, err)
					return
				}
				resetDeadline(snapshot)
				if err := room.publishSnapshot("", snapshot); err != nil {
					room.manager.report(room.lease.RoomID, err)
					return
				}
				if snapshot.State == gameDomain.SessionCancelled {
					return
				}
				continue
			}
			updated, err := room.advance(deadline)
			if err != nil {
				room.manager.report(room.lease.RoomID, err)
				stored, loadErr := room.manager.repository.FindByID(room.ctx, room.lease.RoomID)
				if loadErr != nil {
					room.manager.report(room.lease.RoomID, loadErr)
					return
				}
				snapshot = stored.Snapshot()
				if err := room.syncGameRuntime(snapshot, false); err != nil {
					room.manager.report(room.lease.RoomID, err)
					return
				}
				resetDeadline(snapshot)
				continue
			}
			messageID := advanceCommandID(snapshot, deadline)
			snapshot = updated
			if err := room.syncGameRuntime(snapshot, false); err != nil {
				room.manager.report(room.lease.RoomID, err)
				return
			}
			resetDeadline(snapshot)
			if err := room.publishSnapshot(messageID, updated); err != nil {
				room.manager.report(room.lease.RoomID, err)
				if errors.Is(err, coordinationAbstract.ErrLeaseLost) ||
					errors.Is(err, coordinationAbstract.ErrUnavailable) {
					return
				}
			}
			if snapshot.State == gameDomain.SessionFinished || snapshot.State == gameDomain.SessionCancelled {
				return
			}
		}
	}
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
	return room.manager.coordinator.PublishRoomEvent(room.ctx, room.lease, coordinationAbstract.MessageEnvelope{
		MessageID: messageID,
		RoomID:    room.lease.RoomID,
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
	return room.manager.coordinator.PublishRoomEvent(room.ctx, room.lease, coordinationAbstract.MessageEnvelope{
		MessageID:    envelope.MessageID,
		RoomID:       room.lease.RoomID,
		Type:         CommandRejectedMessageType,
		ConnectionID: envelope.ConnectionID,
		Payload:      payload,
	})
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
