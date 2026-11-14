package realtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
)

type failingSubscription struct {
	messages chan coordinationAbstract.MessageEnvelope
	errors   chan error
	closed   atomic.Int32
}

func (subscription *failingSubscription) Messages() <-chan coordinationAbstract.MessageEnvelope {
	return subscription.messages
}
func (subscription *failingSubscription) Errors() <-chan error { return subscription.errors }
func (subscription *failingSubscription) Close() error {
	subscription.closed.Add(1)
	return nil
}

// A hub whose subscription failed leaves the gateway, so a reconnecting
// client subscribes again instead of joining a hub nobody reads for.
func TestRoomHubSubscriptionFailureRemovesAndStopsHub(t *testing.T) {
	gateway := &Gateway{options: GatewayOptions{Metrics: &Metrics{}}, hubs: map[string]*roomHub{}, errors: make(chan RuntimeError, 1)}
	subscription := &failingSubscription{messages: make(chan coordinationAbstract.MessageEnvelope), errors: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	hub := &roomHub{gateway: gateway, roomID: "room", ctx: ctx, cancel: cancel, subscription: subscription, clients: map[string]*gatewayClient{}}
	gateway.hubs["room"] = hub
	gateway.workers.Add(1)
	go hub.run()
	subscription.errors <- errors.New("subscription lost")
	done := make(chan struct{})
	go func() { gateway.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("hub did not stop")
	}
	if gateway.hubs["room"] != nil || ctx.Err() == nil || subscription.closed.Load() != 1 {
		t.Fatalf("dead hub: registered=%v cancelled=%v closes=%d", gateway.hubs["room"] != nil, ctx.Err() != nil, subscription.closed.Load())
	}
	// A client leaving afterwards neither stops it twice nor removes a newer hub.
	newer := &roomHub{roomID: "room"}
	gateway.hubs["room"] = newer
	gateway.leaveHub(hub, &gatewayClient{id: "late"})
	if gateway.hubs["room"] != newer || subscription.closed.Load() != 1 {
		t.Fatal("late leave disturbed the newer hub or re-stopped the dead one")
	}
}
