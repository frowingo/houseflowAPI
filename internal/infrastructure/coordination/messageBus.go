package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/redis/go-redis/v9"
)

const (
	commandStreamMaxLength = 10000
	commandStreamRetention = 5 * time.Minute
)

var publishCommandScript = redis.NewScript(
	"local messageID = redis.call('XADD', KEYS[1], 'MAXLEN', '~', ARGV[2], '*', 'payload', ARGV[1]) " +
		"redis.call('PEXPIRE', KEYS[1], ARGV[3]) " +
		"return messageID",
)

var publishRoomEventScript = redis.NewScript(
	"if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end " +
		"redis.call('PUBLISH', KEYS[2], ARGV[2]) " +
		"return 1",
)

func (coordinator *RedisCoordinator) PublishCommand(
	ctx context.Context,
	targetInstanceID string,
	envelope coordinationAbstract.MessageEnvelope,
) error {
	if targetInstanceID == "" {
		return errors.New("target instance ID is required")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	if err := coordinator.prepareEnvelope(&envelope, false); err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	if err := publishCommandScript.Run(
		ctx,
		coordinator.client,
		[]string{commandStreamKey(targetInstanceID)},
		payload,
		commandStreamMaxLength,
		commandStreamRetention.Milliseconds(),
	).Err(); err != nil {
		coordinator.available.Store(false)
		return unavailableError(err)
	}
	coordinator.available.Store(true)
	return nil
}

func (coordinator *RedisCoordinator) SubscribeCommands(
	ctx context.Context,
) (coordinationAbstract.CommandSubscription, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return nil, err
	}
	subscription := newRedisCommandSubscription(coordinator, ctx)
	if err := coordinator.registerSubscription(subscription, true); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	subscription.start()
	return subscription, nil
}

func (coordinator *RedisCoordinator) PublishRoomEvent(
	ctx context.Context,
	lease coordinationAbstract.RoomLease,
	envelope coordinationAbstract.MessageEnvelope,
) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	if lease.OwnerInstanceID != coordinator.instanceID || lease.LeaseID == "" || lease.FencingToken <= 0 {
		return coordinationAbstract.ErrLeaseLost
	}
	envelope.RoomID = lease.RoomID
	envelope.FencingToken = lease.FencingToken
	if err := coordinator.prepareEnvelope(&envelope, true); err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	published, err := publishRoomEventScript.Run(
		ctx,
		coordinator.client,
		[]string{roomLeaseKey(lease.RoomID), roomEventChannel(lease.RoomID)},
		leaseValue(lease),
		payload,
	).Int64()
	if err != nil {
		coordinator.available.Store(false)
		return unavailableError(err)
	}
	if published == 0 {
		coordinator.forgetLease(lease)
		return coordinationAbstract.ErrLeaseLost
	}
	coordinator.available.Store(true)
	return nil
}

func (coordinator *RedisCoordinator) SubscribeRoomEvents(
	ctx context.Context,
	roomID string,
) (coordinationAbstract.RoomEventSubscription, error) {
	if roomID == "" {
		return nil, errors.New("room ID is required")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return nil, err
	}
	subscription, err := newRedisEventSubscription(coordinator, ctx, roomEventChannel(roomID))
	if err != nil {
		return nil, err
	}
	if err := coordinator.registerSubscription(subscription, false); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	subscription.start()
	return subscription, nil
}

func (coordinator *RedisCoordinator) ClaimMessage(
	ctx context.Context,
	scopeID string,
	messageID string,
	ttl time.Duration,
) (bool, error) {
	if scopeID == "" || messageID == "" {
		return false, errors.New("scope ID and message ID are required")
	}
	if ttl <= 0 {
		return false, errors.New("deduplication TTL must be greater than zero")
	}
	if err := coordinator.ensureOpen(); err != nil {
		return false, err
	}
	claimed, err := coordinator.client.SetNX(
		ctx,
		deduplicationKey(scopeID, messageID),
		coordinator.instanceID,
		ttl,
	).Result()
	if err != nil {
		coordinator.available.Store(false)
		return false, unavailableError(err)
	}
	coordinator.available.Store(true)
	return claimed, nil
}

func (coordinator *RedisCoordinator) prepareEnvelope(
	envelope *coordinationAbstract.MessageEnvelope,
	requireFencingToken bool,
) error {
	if envelope.MessageID == "" || envelope.RoomID == "" || envelope.Type == "" {
		return errors.New("message ID, room ID and type are required")
	}
	if requireFencingToken && envelope.FencingToken <= 0 {
		return errors.New("fencing token is required for room events")
	}
	if envelope.SourceInstanceID != "" && envelope.SourceInstanceID != coordinator.instanceID {
		return errors.New("message source instance does not match the coordinator")
	}
	envelope.SourceInstanceID = coordinator.instanceID
	if envelope.CreatedAt.IsZero() {
		envelope.CreatedAt = time.Now().UTC()
	}
	return nil
}
