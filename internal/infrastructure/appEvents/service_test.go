package appEvents

import (
	"context"
	"encoding/json"
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
}

func (*recordingBus) Available() bool { return true }

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
