package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/redis/go-redis/v9"
)

const appEventChannel = redisNamespace + ":appEvents"

var _ coordinationAbstract.AppEventBus = (*RedisCoordinator)(nil)

func (coordinator *RedisCoordinator) PublishAppEvent(ctx context.Context, event coordinationAbstract.AppEvent) error {
	if err := coordinator.ensureOpen(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := coordinator.client.Publish(ctx, appEventChannel, payload).Err(); err != nil {
		return unavailableError(err)
	}
	return nil
}

func (coordinator *RedisCoordinator) SubscribeAppEvents(ctx context.Context) (coordinationAbstract.AppEventSubscription, error) {
	if err := coordinator.ensureOpen(); err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	pubSub := coordinator.client.Subscribe(streamCtx, appEventChannel)
	if _, err := pubSub.Receive(streamCtx); err != nil {
		cancel()
		_ = pubSub.Close()
		return nil, unavailableError(err)
	}
	subscription := &redisAppEventSubscription{
		coordinator: coordinator,
		ctx:         streamCtx,
		cancel:      cancel,
		pubSub:      pubSub,
		events:      make(chan coordinationAbstract.AppEvent, 64),
		errors:      make(chan error, 1),
	}
	if err := coordinator.registerSubscription(subscription, false); err != nil {
		_ = subscription.Close()
		return nil, err
	}
	go subscription.readLoop()
	return subscription, nil
}

type redisAppEventSubscription struct {
	coordinator *RedisCoordinator
	ctx         context.Context
	cancel      context.CancelFunc
	pubSub      *redis.PubSub
	events      chan coordinationAbstract.AppEvent
	errors      chan error
	closeOnce   sync.Once
}

func (subscription *redisAppEventSubscription) Events() <-chan coordinationAbstract.AppEvent {
	return subscription.events
}
func (subscription *redisAppEventSubscription) Errors() <-chan error { return subscription.errors }

func (subscription *redisAppEventSubscription) Close() error {
	var err error
	subscription.closeOnce.Do(func() {
		subscription.cancel()
		err = subscription.pubSub.Close()
	})
	return err
}

func (subscription *redisAppEventSubscription) readLoop() {
	defer func() {
		subscription.coordinator.unregisterSubscription(subscription, false)
		close(subscription.events)
		close(subscription.errors)
	}()
	for {
		message, err := subscription.pubSub.ReceiveMessage(subscription.ctx)
		if err != nil {
			if subscription.ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				select {
				case subscription.errors <- unavailableError(err):
				default:
				}
			}
			return
		}
		var event coordinationAbstract.AppEvent
		if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
			continue
		}
		select {
		case subscription.events <- event:
		case <-subscription.ctx.Done():
			return
		}
	}
}
