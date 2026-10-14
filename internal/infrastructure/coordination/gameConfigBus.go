package coordination

import (
	"context"
	"encoding/json"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/google/uuid"
)

const gameConfigChangedType = "game.configChanged"

var _ coordinationAbstract.GameConfigBus = (*RedisCoordinator)(nil)

func gameConfigChannel() string { return redisNamespace + ":gameConfig" }

func (coordinator *RedisCoordinator) SubscribeGameConfigChanges(ctx context.Context) (coordinationAbstract.RoomEventSubscription, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return nil, err
	}
	subscription, err := newRedisEventSubscription(coordinator, ctx, gameConfigChannel())
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

func (coordinator *RedisCoordinator) PublishGameConfigChanged(ctx context.Context, gameKey string) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	envelope := coordinationAbstract.MessageEnvelope{MessageID: uuid.NewString(), RoomID: gameKey, Type: gameConfigChangedType}
	if err := coordinator.prepareEnvelope(&envelope, false); err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if err := coordinator.client.Publish(ctx, gameConfigChannel(), payload).Err(); err != nil {
		return unavailableError(err)
	}
	return nil
}
