package coordination

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	defaultHeartbeatTTL      = 15 * time.Second
	defaultHeartbeatInterval = 5 * time.Second
	cleanupTimeout           = 5 * time.Second
)

var compareAndDeleteScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

type RedisOptions struct {
	URL               string
	InstanceID        string
	HeartbeatTTL      time.Duration
	HeartbeatInterval time.Duration
}

type managedSubscription interface {
	Close() error
}

type RedisCoordinator struct {
	client            *redis.Client
	instanceID        string
	heartbeatValue    string
	heartbeatTTL      time.Duration
	heartbeatInterval time.Duration
	available         atomic.Bool
	mutex             sync.Mutex
	started           bool
	closed            bool
	rootCancel        context.CancelFunc
	workers           sync.WaitGroup
	leases            map[string]coordinationAbstract.RoomLease
	presences         map[string]coordinationAbstract.Presence
	subscriptions     map[managedSubscription]struct{}
	commandSubscribed bool
}

var _ coordinationAbstract.Coordinator = (*RedisCoordinator)(nil)

func NewRedisCoordinator(options RedisOptions) (*RedisCoordinator, error) {
	if options.URL == "" {
		return nil, errors.New("redis URL is required")
	}
	if options.InstanceID == "" {
		return nil, errors.New("coordination instance ID is required")
	}
	if options.HeartbeatTTL <= 0 {
		options.HeartbeatTTL = defaultHeartbeatTTL
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = defaultHeartbeatInterval
	}
	if options.HeartbeatInterval >= options.HeartbeatTTL {
		return nil, errors.New("heartbeat interval must be shorter than heartbeat TTL")
	}

	redisOptions, err := redis.ParseURL(options.URL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	redisOptions.MaxRetries = 2
	redisOptions.DialTimeout = 3 * time.Second
	redisOptions.ReadTimeout = 3 * time.Second
	redisOptions.WriteTimeout = 3 * time.Second

	return &RedisCoordinator{
		client:            redis.NewClient(redisOptions),
		instanceID:        options.InstanceID,
		heartbeatValue:    uuid.NewString(),
		heartbeatTTL:      options.HeartbeatTTL,
		heartbeatInterval: options.HeartbeatInterval,
		leases:            make(map[string]coordinationAbstract.RoomLease),
		presences:         make(map[string]coordinationAbstract.Presence),
		subscriptions:     make(map[managedSubscription]struct{}),
	}, nil
}

func NewInstanceID(base string) string {
	if base == "" {
		base, _ = os.Hostname()
	}
	if base == "" {
		base = "houseflow"
	}
	return base + "-" + uuid.NewString()
}

func (coordinator *RedisCoordinator) InstanceID() string {
	return coordinator.instanceID
}

func (coordinator *RedisCoordinator) Start(ctx context.Context) {
	coordinator.mutex.Lock()
	if coordinator.started || coordinator.closed {
		coordinator.mutex.Unlock()
		return
	}
	rootCtx, cancel := context.WithCancel(ctx)
	coordinator.rootCancel = cancel
	coordinator.started = true
	coordinator.workers.Add(1)
	coordinator.mutex.Unlock()

	go coordinator.heartbeatLoop(rootCtx)
}

func (coordinator *RedisCoordinator) Available() bool {
	return coordinator.available.Load()
}

func (coordinator *RedisCoordinator) Ping(ctx context.Context) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	if err := coordinator.client.Ping(ctx).Err(); err != nil {
		coordinator.available.Store(false)
		return unavailableError(err)
	}
	coordinator.available.Store(true)
	return nil
}

func (coordinator *RedisCoordinator) InstanceAlive(ctx context.Context, instanceID string) (bool, error) {
	if instanceID == "" {
		return false, errors.New("instance ID is required")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return false, err
	}
	exists, err := coordinator.client.Exists(ctx, instanceHeartbeatKey(instanceID)).Result()
	if err != nil {
		coordinator.available.Store(false)
		return false, unavailableError(err)
	}
	return exists == 1, nil
}

func (coordinator *RedisCoordinator) heartbeatLoop(ctx context.Context) {
	defer coordinator.workers.Done()
	coordinator.touchHeartbeat(ctx)
	ticker := time.NewTicker(coordinator.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			coordinator.touchHeartbeat(ctx)
		}
	}
}

func (coordinator *RedisCoordinator) touchHeartbeat(ctx context.Context) {
	err := coordinator.client.Set(
		ctx,
		instanceHeartbeatKey(coordinator.instanceID),
		coordinator.heartbeatValue,
		coordinator.heartbeatTTL,
	).Err()
	coordinator.available.Store(err == nil)
}

func (coordinator *RedisCoordinator) Close(ctx context.Context) error {
	coordinator.mutex.Lock()
	if coordinator.closed {
		coordinator.mutex.Unlock()
		return nil
	}
	coordinator.closed = true
	cancel := coordinator.rootCancel
	subscriptions := make([]managedSubscription, 0, len(coordinator.subscriptions))
	for subscription := range coordinator.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	leases := make([]coordinationAbstract.RoomLease, 0, len(coordinator.leases))
	for _, lease := range coordinator.leases {
		leases = append(leases, lease)
	}
	presences := make([]coordinationAbstract.Presence, 0, len(coordinator.presences))
	for _, presence := range coordinator.presences {
		presences = append(presences, presence)
	}
	coordinator.mutex.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, subscription := range subscriptions {
		_ = subscription.Close()
	}
	coordinator.workers.Wait()

	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cleanupCancel()
	var cleanupErrors []error
	for _, lease := range leases {
		if _, err := coordinator.releaseRoom(cleanupCtx, lease); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	for _, presence := range presences {
		if err := coordinator.removePresence(cleanupCtx, presence.RoomID, presence.ConnectionID); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if err := compareAndDeleteScript.Run(
		cleanupCtx,
		coordinator.client,
		[]string{instanceHeartbeatKey(coordinator.instanceID)},
		coordinator.heartbeatValue,
	).Err(); err != nil && !errors.Is(err, redis.Nil) {
		cleanupErrors = append(cleanupErrors, unavailableError(err))
	}
	if err := coordinator.client.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	coordinator.available.Store(false)
	return errors.Join(cleanupErrors...)
}

func (coordinator *RedisCoordinator) ensureOpen() error {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.closed {
		return coordinationAbstract.ErrCoordinatorStopped
	}
	return nil
}

func (coordinator *RedisCoordinator) registerSubscription(subscription managedSubscription, command bool) error {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if coordinator.closed {
		return coordinationAbstract.ErrCoordinatorStopped
	}
	if command && coordinator.commandSubscribed {
		return coordinationAbstract.ErrAlreadySubscribed
	}
	coordinator.subscriptions[subscription] = struct{}{}
	if command {
		coordinator.commandSubscribed = true
	}
	return nil
}

func (coordinator *RedisCoordinator) unregisterSubscription(subscription managedSubscription, command bool) {
	coordinator.mutex.Lock()
	delete(coordinator.subscriptions, subscription)
	if command {
		coordinator.commandSubscribed = false
	}
	coordinator.mutex.Unlock()
}

func unavailableError(err error) error {
	return fmt.Errorf("%w: %v", coordinationAbstract.ErrUnavailable, err)
}
