package tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"

	"github.com/redis/go-redis/v9"
)

const coordinationTestTimeout = 5 * time.Second

func TestOnlyOneInstanceAcquiresRoomOwnership(t *testing.T) {
	redisURL := prepareRedis(t)
	first := newTestCoordinator(t, redisURL, "instance-a")
	second := newTestCoordinator(t, redisURL, "instance-b")

	start := make(chan struct{})
	results := make(chan bool, 2)
	var waitGroup sync.WaitGroup
	for _, coordinator := range []*infrastructureCoordination.RedisCoordinator{first, second} {
		waitGroup.Add(1)
		go func(candidate *infrastructureCoordination.RedisCoordinator) {
			defer waitGroup.Done()
			<-start
			_, acquired, err := candidate.AcquireRoom(context.Background(), "room-one", time.Second)
			if err != nil {
				t.Errorf("acquire room: %v", err)
				return
			}
			results <- acquired
		}(coordinator)
	}
	close(start)
	waitGroup.Wait()
	close(results)

	acquiredCount := 0
	for acquired := range results {
		if acquired {
			acquiredCount++
		}
	}
	if acquiredCount != 1 {
		t.Fatalf("acquired owner count = %d, want 1", acquiredCount)
	}
	owner, exists, err := first.CurrentRoomOwner(context.Background(), "room-one")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || (owner.OwnerInstanceID != first.InstanceID() && owner.OwnerInstanceID != second.InstanceID()) {
		t.Fatalf("current owner = %+v, exists=%v", owner, exists)
	}
}

func TestRoomLeaseCanBeRenewedAndReleased(t *testing.T) {
	redisURL := prepareRedis(t)
	first := newTestCoordinator(t, redisURL, "instance-a")
	second := newTestCoordinator(t, redisURL, "instance-b")
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	defer cancel()

	lease, acquired, err := first.AcquireRoom(ctx, "room-renew", 200*time.Millisecond)
	if err != nil || !acquired {
		t.Fatalf("lease acquired=%v err=%v", acquired, err)
	}
	time.Sleep(100 * time.Millisecond)
	renewed, err := first.RenewRoom(ctx, lease, 400*time.Millisecond)
	if err != nil || !renewed {
		t.Fatalf("lease renewed=%v err=%v", renewed, err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, acquired, err := second.AcquireRoom(ctx, lease.RoomID, time.Second); err != nil || acquired {
		t.Fatalf("second owner acquired active lease=%v err=%v", acquired, err)
	}
	released, err := first.ReleaseRoom(ctx, lease)
	if err != nil || !released {
		t.Fatalf("lease released=%v err=%v", released, err)
	}
	if _, acquired, err := second.AcquireRoom(ctx, lease.RoomID, time.Second); err != nil || !acquired {
		t.Fatalf("second owner acquired after release=%v err=%v", acquired, err)
	}
}

func TestExpiredOwnerCannotPublishWithStaleFencingToken(t *testing.T) {
	redisURL := prepareRedis(t)
	first := newTestCoordinator(t, redisURL, "instance-a")
	second := newTestCoordinator(t, redisURL, "instance-b")
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	defer cancel()

	events, err := second.SubscribeRoomEvents(ctx, "room-fence")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()

	firstLease, acquired, err := first.AcquireRoom(ctx, "room-fence", 250*time.Millisecond)
	if err != nil || !acquired {
		t.Fatalf("first lease acquired=%v err=%v", acquired, err)
	}
	if err := first.PublishRoomEvent(ctx, firstLease, testEnvelope("event-before-expiry", "room-fence")); err != nil {
		t.Fatal(err)
	}
	assertEvent(t, ctx, events, "event-before-expiry")

	var secondLease coordinationAbstract.RoomLease
	eventually(t, time.Second, func() bool {
		var acquireErr error
		secondLease, acquired, acquireErr = second.AcquireRoom(ctx, "room-fence", time.Second)
		return acquireErr == nil && acquired
	})
	if secondLease.FencingToken <= firstLease.FencingToken {
		t.Fatalf(
			"new fencing token = %d, want greater than %d",
			secondLease.FencingToken,
			firstLease.FencingToken,
		)
	}

	err = first.PublishRoomEvent(ctx, firstLease, testEnvelope("stale-event", "room-fence"))
	if !errors.Is(err, coordinationAbstract.ErrLeaseLost) {
		t.Fatalf("stale publish error = %v, want ErrLeaseLost", err)
	}
	if err := second.PublishRoomEvent(ctx, secondLease, testEnvelope("new-owner-event", "room-fence")); err != nil {
		t.Fatal(err)
	}
	assertEvent(t, ctx, events, "new-owner-event")
}

func TestCommandRoutingIsDurableAndMessageClaimIsIdempotent(t *testing.T) {
	redisURL := prepareRedis(t)
	sender := newTestCoordinator(t, redisURL, "instance-a")
	owner := newTestCoordinator(t, redisURL, "instance-b")
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	defer cancel()

	envelope := testEnvelope("command-one", "room-command")
	envelope.Payload = json.RawMessage(`{"ready":true}`)
	if err := sender.PublishCommand(ctx, owner.InstanceID(), envelope); err != nil {
		t.Fatal(err)
	}

	commands, err := owner.SubscribeCommands(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer commands.Close()

	var delivery coordinationAbstract.CommandDelivery
	select {
	case delivery = <-commands.Deliveries():
	case err := <-commands.Errors():
		t.Fatalf("command subscription error: %v", err)
	case <-ctx.Done():
		t.Fatal("command was not routed to owner instance")
	}
	if delivery.Envelope().MessageID != envelope.MessageID {
		t.Fatalf("message ID = %q, want %q", delivery.Envelope().MessageID, envelope.MessageID)
	}

	claimed, err := owner.ClaimMessage(ctx, envelope.RoomID, envelope.MessageID, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, err=%v", claimed, err)
	}
	claimed, err = owner.ClaimMessage(ctx, envelope.RoomID, envelope.MessageID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("duplicate message claim unexpectedly succeeded")
	}
	if err := delivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUnacknowledgedCommandIsRedeliveredWithoutProcessRestart(t *testing.T) {
	redisURL := prepareRedis(t)
	sender := newTestCoordinator(t, redisURL, "redelivery-sender")
	owner := newTestCoordinator(t, redisURL, "redelivery-owner")
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	defer cancel()

	commands, err := owner.SubscribeCommands(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer commands.Close()
	envelope := testEnvelope("redelivered-command", "redelivery-room")
	if err := sender.PublishCommand(ctx, owner.InstanceID(), envelope); err != nil {
		t.Fatal(err)
	}

	first := awaitCommandDelivery(t, ctx, commands)
	second := awaitCommandDelivery(t, ctx, commands)
	if second.Envelope().MessageID != first.Envelope().MessageID {
		t.Fatalf(
			"redelivered message ID = %q, want %q",
			second.Envelope().MessageID,
			first.Envelope().MessageID,
		)
	}
	if err := second.Ack(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownRemovesHeartbeatPresenceAndOwnedLease(t *testing.T) {
	redisURL := prepareRedis(t)
	first := newTestCoordinator(t, redisURL, "instance-a")
	second := newTestCoordinator(t, redisURL, "instance-b")
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	defer cancel()

	eventually(t, time.Second, func() bool {
		alive, err := second.InstanceAlive(ctx, first.InstanceID())
		return err == nil && alive
	})
	lease, acquired, err := first.AcquireRoom(ctx, "room-shutdown", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("lease acquired=%v err=%v", acquired, err)
	}
	if err := first.TouchPresence(ctx, coordinationAbstract.Presence{
		RoomID:       lease.RoomID,
		UserID:       "user-a",
		ConnectionID: "connection-a",
	}, time.Minute); err != nil {
		t.Fatal(err)
	}

	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool {
		alive, aliveErr := second.InstanceAlive(ctx, first.InstanceID())
		return aliveErr == nil && !alive
	})
	if _, exists, err := second.CurrentRoomOwner(ctx, lease.RoomID); err != nil || exists {
		t.Fatalf("room owner exists=%v err=%v after owner shutdown", exists, err)
	}
	presences, err := second.ListPresence(ctx, lease.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if len(presences) != 0 {
		t.Fatalf("presence count = %d, want 0 after owner shutdown", len(presences))
	}
}

func TestRedisOutageReturnsControlledUnavailableError(t *testing.T) {
	coordinator, err := infrastructureCoordination.NewRedisCoordinator(
		infrastructureCoordination.RedisOptions{
			URL:        "redis://127.0.0.1:1/0",
			InstanceID: "unavailable-instance",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err = coordinator.Ping(ctx)
	if !errors.Is(err, coordinationAbstract.ErrUnavailable) {
		t.Fatalf("ping error = %v, want ErrUnavailable", err)
	}
}

func prepareRedis(t *testing.T) string {
	t.Helper()
	redisURL := os.Getenv("HOUSEFLOW_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("HOUSEFLOW_TEST_REDIS_URL is required for coordination integration tests")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), coordinationTestTimeout)
	t.Cleanup(cancel)
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = client.FlushDB(cleanupCtx).Err()
		_ = client.Close()
	})
	return redisURL
}

func newTestCoordinator(
	t *testing.T,
	redisURL string,
	instanceID string,
) *infrastructureCoordination.RedisCoordinator {
	t.Helper()
	coordinator, err := infrastructureCoordination.NewRedisCoordinator(
		infrastructureCoordination.RedisOptions{
			URL:               redisURL,
			InstanceID:        instanceID,
			HeartbeatTTL:      600 * time.Millisecond,
			HeartbeatInterval: 100 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Start(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = coordinator.Close(ctx)
	})
	return coordinator
}

func testEnvelope(messageID string, roomID string) coordinationAbstract.MessageEnvelope {
	return coordinationAbstract.MessageEnvelope{
		MessageID: messageID,
		RoomID:    roomID,
		Type:      "test.message",
	}
}

func assertEvent(
	t *testing.T,
	ctx context.Context,
	subscription coordinationAbstract.RoomEventSubscription,
	messageID string,
) {
	t.Helper()
	select {
	case message := <-subscription.Messages():
		if message.MessageID != messageID {
			t.Fatalf("event message ID = %q, want %q", message.MessageID, messageID)
		}
	case err := <-subscription.Errors():
		t.Fatalf("room event subscription error: %v", err)
	case <-ctx.Done():
		t.Fatalf("event %q was not delivered", messageID)
	}
}

func awaitCommandDelivery(
	t *testing.T,
	ctx context.Context,
	subscription coordinationAbstract.CommandSubscription,
) coordinationAbstract.CommandDelivery {
	t.Helper()
	select {
	case delivery := <-subscription.Deliveries():
		return delivery
	case err := <-subscription.Errors():
		t.Fatalf("command subscription error: %v", err)
	case <-ctx.Done():
		t.Fatal("command was not delivered")
	}
	return nil
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
