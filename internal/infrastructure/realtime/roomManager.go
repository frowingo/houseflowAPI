package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
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
)

type RoomManagerOptions struct {
	LeaseTTL           time.Duration
	RenewInterval      time.Duration
	CommandTimeout     time.Duration
	CommandQueueSize   int
	MaxCommandAttempts int
	RetryDelay         time.Duration
}

type RoomOwnership struct {
	RoomID          string
	OwnerInstanceID string
	FencingToken    int64
	Local           bool
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

	mutex        sync.RWMutex
	rooms        map[string]*managedRoom
	activations  map[string]*roomActivation
	started      bool
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
	subscription coordinationAbstract.CommandSubscription
	errors       chan RuntimeError
	workers      sync.WaitGroup
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
	if manager.closed {
		manager.mutex.Unlock()
		return ErrRoomRuntimeStopped
	}
	if manager.started {
		manager.mutex.Unlock()
		return nil
	}
	rootCtx, cancel := context.WithCancel(ctx)
	manager.ctx = rootCtx
	manager.cancel = cancel
	manager.started = true
	manager.mutex.Unlock()

	subscription, err := manager.coordinator.SubscribeCommands(rootCtx)
	if err != nil {
		manager.mutex.Lock()
		manager.started = false
		manager.ctx = nil
		manager.cancel = nil
		manager.mutex.Unlock()
		cancel()
		return err
	}
	manager.mutex.Lock()
	manager.subscription = subscription
	manager.mutex.Unlock()
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
	lease, acquired, err := manager.coordinator.AcquireRoom(ctx, roomID, manager.options.LeaseTTL)
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
				return manager.startRoom(ctx, rootCtx, lease)
			}
			owner, exists, err = manager.coordinator.CurrentRoomOwner(ctx, roomID)
			if err != nil {
				return RoomOwnership{}, err
			}
			if !exists {
				return RoomOwnership{}, coordinationAbstract.ErrUnavailable
			}
		}
		return ownershipFromLease(owner, false), nil
	}
	return manager.startRoom(ctx, rootCtx, lease)
}

func (manager *RoomManager) startRoom(
	ctx context.Context,
	rootCtx context.Context,
	lease coordinationAbstract.RoomLease,
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

	roomCtx, roomCancel := context.WithCancel(rootCtx)
	room := &managedRoom{
		manager: manager,
		ctx:     roomCtx,
		cancel:  roomCancel,
		lease:   lease,
		queue:   make(chan coordinationAbstract.CommandDelivery, manager.options.CommandQueueSize),
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
	manager.workers.Add(1)
	manager.mutex.Unlock()
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
	manager  *RoomManager
	ctx      context.Context
	cancel   context.CancelFunc
	lease    coordinationAbstract.RoomLease
	queue    chan coordinationAbstract.CommandDelivery
	stopOnce sync.Once
}

type roomActivation struct {
	done chan struct{}
}

func (room *managedRoom) ownership() RoomOwnership {
	return ownershipFromLease(room.lease, true)
}

func (room *managedRoom) stop() {
	room.stopOnce.Do(room.cancel)
}

func (room *managedRoom) run(snapshot gameDomain.SessionSnapshot) {
	defer room.manager.workers.Done()
	defer room.manager.removeRoom(room)
	defer room.manager.releaseLease(room.lease)

	renewTicker := time.NewTicker(room.manager.options.RenewInterval)
	defer renewTicker.Stop()
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
	defer func() {
		if deadlineTimer != nil {
			deadlineTimer.Stop()
		}
	}()

	for {
		select {
		case <-room.ctx.Done():
			return
		case <-renewTicker.C:
			renewed, err := room.manager.coordinator.RenewRoom(
				room.ctx,
				room.lease,
				room.manager.options.LeaseTTL,
			)
			if err != nil || !renewed {
				if err == nil {
					err = coordinationAbstract.ErrLeaseLost
				}
				room.manager.report(room.lease.RoomID, err)
				return
			}
		case delivery := <-room.queue:
			updated, err := room.processCommand(delivery.Envelope())
			if err != nil {
				room.manager.report(room.lease.RoomID, err)
				if !isRetryableCommandError(err) {
					_ = delivery.Ack(room.ctx)
				}
				continue
			}
			snapshot = updated
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
		case <-deadlineChannel:
			deadline, exists := sessionDeadline(snapshot)
			if !exists {
				resetDeadline(snapshot)
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
				resetDeadline(snapshot)
				continue
			}
			messageID := advanceCommandID(snapshot, deadline)
			snapshot = updated
			resetDeadline(snapshot)
			if err := room.publishSnapshot(messageID, updated); err != nil {
				room.manager.report(room.lease.RoomID, err)
				if errors.Is(err, coordinationAbstract.ErrLeaseLost) ||
					errors.Is(err, coordinationAbstract.ErrUnavailable) {
					return
				}
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
	return false
}
