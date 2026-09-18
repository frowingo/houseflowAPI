package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"

	"github.com/redis/go-redis/v9"
)

type redisCommandDelivery struct {
	coordinator *RedisCoordinator
	streamKey   string
	entryID     string
	envelope    coordinationAbstract.MessageEnvelope
	ackOnce     sync.Once
	ackError    error
}

func (delivery *redisCommandDelivery) Envelope() coordinationAbstract.MessageEnvelope {
	return delivery.envelope
}

func (delivery *redisCommandDelivery) Ack(ctx context.Context) error {
	delivery.ackOnce.Do(func() {
		delivery.ackError = delivery.coordinator.client.XDel(ctx, delivery.streamKey, delivery.entryID).Err()
		if delivery.ackError != nil {
			delivery.ackError = unavailableError(delivery.ackError)
		}
	})
	return delivery.ackError
}

type redisCommandSubscription struct {
	coordinator *RedisCoordinator
	ctx         context.Context
	cancel      context.CancelFunc
	deliveries  chan coordinationAbstract.CommandDelivery
	errors      chan error
	done        chan struct{}
	started     atomic.Bool
	closeOnce   sync.Once
}

func newRedisCommandSubscription(
	coordinator *RedisCoordinator,
	parentCtx context.Context,
) *redisCommandSubscription {
	ctx, cancel := context.WithCancel(parentCtx)
	return &redisCommandSubscription{
		coordinator: coordinator,
		ctx:         ctx,
		cancel:      cancel,
		deliveries:  make(chan coordinationAbstract.CommandDelivery, 64),
		errors:      make(chan error, 4),
		done:        make(chan struct{}),
	}
}

func (subscription *redisCommandSubscription) start() {
	subscription.started.Store(true)
	go subscription.readLoop()
}

func (subscription *redisCommandSubscription) Deliveries() <-chan coordinationAbstract.CommandDelivery {
	return subscription.deliveries
}

func (subscription *redisCommandSubscription) Errors() <-chan error {
	return subscription.errors
}

func (subscription *redisCommandSubscription) Close() error {
	subscription.closeOnce.Do(subscription.cancel)
	if subscription.started.Load() {
		<-subscription.done
	}
	return nil
}

func (subscription *redisCommandSubscription) readLoop() {
	defer func() {
		subscription.coordinator.unregisterSubscription(subscription, true)
		close(subscription.deliveries)
		close(subscription.errors)
		close(subscription.done)
	}()

	streamKey := commandStreamKey(subscription.coordinator.instanceID)
	lastID := "0-0"
	for {
		streams, err := subscription.coordinator.client.XRead(subscription.ctx, &redis.XReadArgs{
			Streams: []string{streamKey, lastID},
			Count:   16,
			Block:   time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if subscription.ctx.Err() != nil {
				return
			}
			subscription.coordinator.available.Store(false)
			subscription.sendError(unavailableError(err))
			continue
		}
		subscription.coordinator.available.Store(true)

		for _, stream := range streams {
			for _, message := range stream.Messages {
				payload, ok := redisValueString(message.Values["payload"])
				if !ok {
					subscription.sendError(errors.New("command stream entry has no payload"))
					_ = subscription.coordinator.client.XDel(subscription.ctx, streamKey, message.ID).Err()
					lastID = message.ID
					continue
				}
				var envelope coordinationAbstract.MessageEnvelope
				if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
					subscription.sendError(fmt.Errorf("decode command envelope: %w", err))
					_ = subscription.coordinator.client.XDel(subscription.ctx, streamKey, message.ID).Err()
					lastID = message.ID
					continue
				}

				delivery := &redisCommandDelivery{
					coordinator: subscription.coordinator,
					streamKey:   streamKey,
					entryID:     message.ID,
					envelope:    envelope,
				}
				select {
				case subscription.deliveries <- delivery:
					lastID = message.ID
				case <-subscription.ctx.Done():
					return
				}
			}
		}
	}
}

func (subscription *redisCommandSubscription) sendError(err error) {
	select {
	case subscription.errors <- err:
	default:
	}
}

type redisEventSubscription struct {
	coordinator *RedisCoordinator
	ctx         context.Context
	cancel      context.CancelFunc
	pubSub      *redis.PubSub
	messages    chan coordinationAbstract.MessageEnvelope
	errors      chan error
	done        chan struct{}
	started     atomic.Bool
	closeOnce   sync.Once
}

func newRedisEventSubscription(
	coordinator *RedisCoordinator,
	parentCtx context.Context,
	channel string,
) (*redisEventSubscription, error) {
	ctx, cancel := context.WithCancel(parentCtx)
	pubSub := coordinator.client.Subscribe(ctx, channel)
	if _, err := pubSub.Receive(ctx); err != nil {
		cancel()
		_ = pubSub.Close()
		coordinator.available.Store(false)
		return nil, unavailableError(err)
	}
	return &redisEventSubscription{
		coordinator: coordinator,
		ctx:         ctx,
		cancel:      cancel,
		pubSub:      pubSub,
		messages:    make(chan coordinationAbstract.MessageEnvelope, 64),
		errors:      make(chan error, 4),
		done:        make(chan struct{}),
	}, nil
}

func (subscription *redisEventSubscription) start() {
	subscription.started.Store(true)
	go subscription.readLoop()
}

func (subscription *redisEventSubscription) Messages() <-chan coordinationAbstract.MessageEnvelope {
	return subscription.messages
}

func (subscription *redisEventSubscription) Errors() <-chan error {
	return subscription.errors
}

func (subscription *redisEventSubscription) Close() error {
	var closeErr error
	subscription.closeOnce.Do(func() {
		subscription.cancel()
		closeErr = subscription.pubSub.Close()
	})
	if subscription.started.Load() {
		<-subscription.done
	}
	return closeErr
}

func (subscription *redisEventSubscription) readLoop() {
	defer func() {
		subscription.coordinator.unregisterSubscription(subscription, false)
		close(subscription.messages)
		close(subscription.errors)
		close(subscription.done)
	}()

	for {
		message, err := subscription.pubSub.ReceiveMessage(subscription.ctx)
		if err != nil {
			if subscription.ctx.Err() != nil {
				return
			}
			subscription.coordinator.available.Store(false)
			subscription.sendError(unavailableError(err))
			continue
		}
		var envelope coordinationAbstract.MessageEnvelope
		if err := json.Unmarshal([]byte(message.Payload), &envelope); err != nil {
			subscription.sendError(fmt.Errorf("decode room event envelope: %w", err))
			continue
		}
		subscription.coordinator.available.Store(true)
		select {
		case subscription.messages <- envelope:
		case <-subscription.ctx.Done():
			return
		}
	}
}

func (subscription *redisEventSubscription) sendError(err error) {
	select {
	case subscription.errors <- err:
	default:
	}
}

func redisValueString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}
