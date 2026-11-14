package appEvents

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type recordingBus struct {
	events []coordinationAbstract.AppEvent
	down   bool
}

func (bus *recordingBus) Available() bool { return !bus.down }

func (bus *recordingBus) PublishAppEvent(_ context.Context, event coordinationAbstract.AppEvent) error {
	bus.events = append(bus.events, event)
	return nil
}

func (*recordingBus) SubscribeAppEvents(context.Context) (coordinationAbstract.AppEventSubscription, error) {
	return nil, nil
}

type houseLookup struct {
	databaseAbstract.DbRepository[entities.House]
	house *entities.House
}

func (lookup houseLookup) FindByID(context.Context, primitive.ObjectID) (*entities.House, error) {
	return lookup.house, nil
}

func TestHouseChangedTargetsCurrentMembers(t *testing.T) {
	id := primitive.NewObjectID()
	bus := &recordingBus{}
	service := NewService(bus, houseLookup{house: &entities.House{
		Id: id, MemberIds: []string{"member-a", "member-b"},
	}}, nil, nil)

	service.HouseChanged(context.Background(), id.Hex(), true)

	if len(bus.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(bus.events))
	}
	if bus.events[0].Type != coordinationAbstract.HouseChangedEvent || bus.events[0].HouseID != id.Hex() {
		t.Fatalf("house event = %+v", bus.events[0])
	}
	if bus.events[1].Type != coordinationAbstract.UserChangedEvent {
		t.Fatalf("user event = %+v", bus.events[1])
	}
	for _, event := range bus.events {
		if !reflect.DeepEqual(event.UserIDs, []string{"member-a", "member-b"}) {
			t.Fatalf("recipients = %v", event.UserIDs)
		}
	}
}

func TestEventsAreStillPublishedWhileTheBusReportsUnavailable(t *testing.T) {
	id := primitive.NewObjectID()
	bus := &recordingBus{down: true}
	service := NewService(bus, houseLookup{house: &entities.House{Id: id, MemberIds: []string{"member-a"}}}, nil, nil)

	service.HouseChanged(context.Background(), id.Hex(), true)
	service.UsersChanged(context.Background(), "member-a")

	if len(bus.events) != 3 {
		t.Fatalf("events=%+v, want all 3 after a transient unavailability", bus.events)
	}
}

type stalledBus struct {
	recordingBus
}

func (*stalledBus) PublishAppEvent(ctx context.Context, _ coordinationAbstract.AppEvent) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestOutageDoesNotCostEachCommandThePublishTimeout(t *testing.T) {
	id := primitive.NewObjectID()
	bus := &stalledBus{recordingBus{down: true}}
	service := NewService(bus, houseLookup{house: &entities.House{Id: id, MemberIds: []string{"member-a"}}}, nil, nil)

	started := time.Now()
	service.HouseChanged(context.Background(), id.Hex(), true)
	service.UsersChanged(context.Background(), "member-a")

	if elapsed := time.Since(started); elapsed >= publishTimeout {
		t.Fatalf("two commands took %v during an outage, want well under %v", elapsed, publishTimeout)
	}
}

func TestDeliveryOnlyReachesTargetUsersAndOmitsRecipients(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	a := &client{outbound: make(chan []byte, 1)}
	b := &client{outbound: make(chan []byte, 1)}
	service.clients["member-a"] = map[*client]struct{}{a: {}}
	service.clients["member-b"] = map[*client]struct{}{b: {}}

	service.deliver(coordinationAbstract.AppEvent{
		Type: coordinationAbstract.HouseChangedEvent, HouseID: "house-1", UserIDs: []string{"member-a"},
	})

	select {
	case raw := <-a.outbound:
		var message map[string]any
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message["type"] != coordinationAbstract.HouseChangedEvent || message["houseId"] != "house-1" {
			t.Fatalf("message = %v", message)
		}
		if _, leaked := message["userIds"]; leaked {
			t.Fatalf("routing metadata leaked: %v", message)
		}
	default:
		t.Fatal("target user received no event")
	}
	select {
	case <-b.outbound:
		t.Fatal("non-target user received an event")
	default:
	}
}

type fanoutBus struct {
	mutex         sync.Mutex
	subscriptions []*fanoutSubscription
}

func (*fanoutBus) Available() bool { return true }

func (bus *fanoutBus) PublishAppEvent(_ context.Context, event coordinationAbstract.AppEvent) error {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()
	for _, subscription := range bus.subscriptions {
		subscription.events <- event
	}
	return nil
}

func (bus *fanoutBus) SubscribeAppEvents(context.Context) (coordinationAbstract.AppEventSubscription, error) {
	subscription := &fanoutSubscription{events: make(chan coordinationAbstract.AppEvent, 8), errors: make(chan error)}
	bus.mutex.Lock()
	bus.subscriptions = append(bus.subscriptions, subscription)
	bus.mutex.Unlock()
	return subscription, nil
}

type fanoutSubscription struct {
	events chan coordinationAbstract.AppEvent
	errors chan error
}

func (subscription *fanoutSubscription) Events() <-chan coordinationAbstract.AppEvent {
	return subscription.events
}
func (subscription *fanoutSubscription) Errors() <-chan error { return subscription.errors }
func (subscription *fanoutSubscription) Close() error         { return nil }

type healthBus struct {
	recordingBus
	ctx          context.Context
	health       chan bool
	subscription *fanoutSubscription
}

func (bus *healthBus) Available() bool {
	select {
	case available := <-bus.health:
		return available
	case <-bus.ctx.Done():
		return true
	}
}

func (bus *healthBus) SubscribeAppEvents(context.Context) (coordinationAbstract.AppEventSubscription, error) {
	return bus.subscription, nil
}

func TestSubscriptionHealth(t *testing.T) {
	for _, test := range []struct {
		name         string
		availability []bool
		lose         func(*fanoutSubscription)
		disconnect   bool
	}{
		{name: "transient unavailability", availability: []bool{false, true, false, true}},
		{name: "recovery resets failures", availability: []bool{false, false, true, false, false, true}},
		{name: "sustained outage", availability: []bool{false, false, false}, disconnect: true},
		{name: "subscription error", lose: func(subscription *fanoutSubscription) {
			subscription.errors <- errors.New("subscription lost")
		}, disconnect: true},
		{name: "events closed", lose: func(subscription *fanoutSubscription) {
			close(subscription.events)
		}, disconnect: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			subscription := &fanoutSubscription{
				events: make(chan coordinationAbstract.AppEvent, 1), errors: make(chan error, 1),
			}
			bus := &healthBus{ctx: ctx, health: make(chan bool), subscription: subscription}
			service := NewService(bus, nil, nil, nil)
			healthTicks := make(chan time.Time)
			service.healthTicks = healthTicks
			connection := &client{outbound: make(chan []byte, 1)}
			service.clients["member-a"] = map[*client]struct{}{connection: {}}
			service.Start(ctx)
			t.Cleanup(func() {
				cancel()
				shutdown, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				if err := service.Close(shutdown); err != nil {
					t.Errorf("close service: %v", err)
				}
			})
			deadline := time.Now().Add(time.Second)
			for !service.Stats().Online && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !service.Stats().Online {
				t.Fatal("service did not subscribe")
			}

			for index, available := range test.availability {
				select {
				case healthTicks <- time.Now():
				case <-time.After(time.Second):
					t.Fatalf("health tick %d was not consumed", index)
				}
				select {
				case bus.health <- available:
				case <-time.After(time.Second):
					t.Fatalf("health reading %d was not consumed", index)
				}
				if test.disconnect && index == len(test.availability)-1 {
					break
				}
				subscription.events <- coordinationAbstract.AppEvent{
					Type: coordinationAbstract.UserChangedEvent, UserIDs: []string{"member-a"},
				}
				select {
				case <-connection.outbound:
				case <-time.After(time.Second):
					t.Fatalf("client stopped receiving events after health reading %d", index)
				}
				if !service.Stats().Online {
					t.Fatalf("service disconnected after health reading %d", index)
				}
			}
			if test.lose != nil {
				test.lose(subscription)
			}
			if test.disconnect {
				deadline = time.Now().Add(time.Second)
				for service.Stats().Online && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if service.Stats().Online {
					t.Fatal("subscription loss did not disconnect clients")
				}
				cancel()
				select {
				case <-service.done:
				case <-time.After(time.Second):
					t.Fatal("service did not stop")
				}
			}
			closed := true
			connection.closeOnce.Do(func() { closed = false })
			if closed != test.disconnect {
				t.Fatalf("client closed = %v, want %v", closed, test.disconnect)
			}
		})
	}
}

func TestEventReachesTargetOnAnotherInstance(t *testing.T) {
	bus := &fanoutBus{}
	first := NewService(bus, nil, nil, nil)
	second := NewService(bus, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first.Start(ctx)
	second.Start(ctx)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = first.Close(shutdown)
		_ = second.Close(shutdown)
	}()
	deadline := time.Now().Add(time.Second)
	for !(first.Stats().Online && second.Stats().Online) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !first.Stats().Online || !second.Stats().Online {
		t.Fatal("both instances did not subscribe")
	}
	connection := &client{outbound: make(chan []byte, 1)}
	second.mutex.Lock()
	second.clients["member-a"] = map[*client]struct{}{connection: {}}
	second.mutex.Unlock()

	first.UsersChanged(ctx, "member-a")
	select {
	case raw := <-connection.outbound:
		if string(raw) != `{"type":"user.changed"}` {
			t.Fatalf("message = %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("event did not reach the second instance")
	}
}
