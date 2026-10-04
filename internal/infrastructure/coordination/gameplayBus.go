package coordination

import (
	"context"
	"encoding/json"
	"errors"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
)

var _ coordinationAbstract.GameplayCoordinator = (*RedisCoordinator)(nil)

func gameplayChannel(instanceID string) string {
	return redisNamespace + ":instance:" + encodeKeyPart(instanceID) + ":gameplay"
}

func (coordinator *RedisCoordinator) SubscribeGameplay(ctx context.Context) (coordinationAbstract.RoomEventSubscription, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return nil, err
	}
	subscription, err := newRedisEventSubscription(coordinator, ctx, gameplayChannel(coordinator.instanceID))
	if err != nil {
		return nil, err
	}
	if err := coordinator.registerSubscription(subscription, false); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	subscription.ephemeral = true
	subscription.start()
	return subscription, nil
}

func (coordinator *RedisCoordinator) PublishGameplay(ctx context.Context, targetInstanceID string, envelope coordinationAbstract.MessageEnvelope) error {
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
	if len(payload) > 4096 {
		return errors.New("gameplay envelope is too large")
	}
	count, err := coordinator.client.Publish(ctx, gameplayChannel(targetInstanceID), payload).Result()
	if err != nil {
		return unavailableError(err)
	}
	if count == 0 {
		return coordinationAbstract.ErrUnavailable
	}
	return nil
}
