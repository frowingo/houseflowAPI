package tests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	houseRockets "houseflowApi/internal/application/game/gameSpesific/houseRockets"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/realtime"
)

func persistRocketsSession(t *testing.T, fixture *gameSessionApplicationFixture, running bool) gameDomain.SessionSnapshot {
	t.Helper()
	now := time.Now().UTC()
	rules := houseRockets.Definition().Rules
	if running {
		rules.ReadyWindowDuration = 0
		rules.CountdownDuration = 0
	}
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "rockets-runtime-session", HouseID: fixture.houseID, GameKey: houseRockets.GameKey,
		ProtocolVersion: houseRockets.ProtocolVersion, Mode: gameDomain.RealtimeGame, Rules: rules, CreatedBy: fixture.ownerID, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if running {
		for _, id := range []string{fixture.ownerID, fixture.memberID} {
			if err := session.JoinPlayer(id, now); err != nil {
				t.Fatal(err)
			}
			if err := session.SetReady(id, true, now); err != nil {
				t.Fatal(err)
			}
		}
		if session.Snapshot().State != gameDomain.SessionRunning {
			t.Fatal("expected running session")
		}
	}
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("rockets-create", "create", "rockets")); err != nil {
		t.Fatal(err)
	}
	return session.Snapshot()
}

func awaitRocketsEvent(t *testing.T, subscription coordinationAbstract.RoomEventSubscription, predicate func(coordinationAbstract.MessageEnvelope) bool) coordinationAbstract.MessageEnvelope {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, open := <-subscription.Messages():
			if !open {
				t.Fatal("subscription closed")
			}
			if predicate(event) {
				return event
			}
		case err := <-subscription.Errors():
			if err != nil {
				t.Fatal(err)
			}
		case <-timer.C:
			t.Fatal("timed out waiting for runtime event")
			return coordinationAbstract.MessageEnvelope{}
		}
	}
}

func awaitRocketsGrant(t *testing.T, subscription coordinationAbstract.RoomEventSubscription, connectionID string) houseRockets.HouseRocketsControlGrantedModel {
	t.Helper()
	event := awaitRocketsEvent(t, subscription, func(event coordinationAbstract.MessageEnvelope) bool {
		return event.Type == houseRockets.ControlGrantedMessageType && event.ConnectionID == connectionID
	})
	var grant houseRockets.HouseRocketsControlGrantedModel
	if err := json.Unmarshal(event.Payload, &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func awaitRocketsFrame(t *testing.T, subscription coordinationAbstract.RoomEventSubscription, predicate func(houseRockets.RuntimeFrame) bool) houseRockets.RuntimeFrame {
	t.Helper()
	var frame houseRockets.RuntimeFrame
	awaitRocketsEvent(t, subscription, func(event coordinationAbstract.MessageEnvelope) bool {
		if event.Type != realtime.GameRuntimeSnapshotEventType {
			return false
		}
		if err := json.Unmarshal(event.Payload, &frame); err != nil {
			t.Fatal(err)
		}
		return predicate(frame)
	})
	return frame
}

func TestHouseRocketsRuntimeRoutesAcrossTwoInstancesAndStopsOnLeaseLoss(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := persistRocketsSession(t, fixture, true)
	redisURL := prepareRedis(t)
	ownerCoordinator := newTestCoordinator(t, redisURL, "rockets-owner")
	remoteCoordinator := newTestCoordinator(t, redisURL, "rockets-remote")
	ownerManager := newTestRoomManager(t, fixture, ownerCoordinator)
	remoteManager := newTestRoomManager(t, fixture, remoteCoordinator)
	ownership, err := ownerManager.EnsureRoom(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	remoteOwnership, err := remoteManager.EnsureRoom(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ownership.Local || remoteOwnership.Local || ownership.RuntimeEpoch <= 0 || ownership.RuntimeEpoch != remoteOwnership.RuntimeEpoch {
		t.Fatalf("owners: %+v / %+v", ownership, remoteOwnership)
	}
	events, err := remoteCoordinator.SubscribeRoomEvents(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	bind := houseRockets.RuntimeInput{Kind: houseRockets.InputBind, RuntimeEpoch: ownership.RuntimeEpoch}
	if err := remoteManager.DispatchGameplay(fixture.ctx, session.SessionID, fixture.ownerID, "remote-connection", bind); err != nil {
		t.Fatal(err)
	}
	first := awaitRocketsGrant(t, events, "remote-connection")
	if err := ownerManager.DispatchGameplay(fixture.ctx, session.SessionID, fixture.memberID, "local-connection", bind); err != nil {
		t.Fatal(err)
	}
	second := awaitRocketsGrant(t, events, "local-connection")
	steer := houseRockets.RuntimeInput{Kind: houseRockets.InputSteer, RuntimeEpoch: first.RuntimeEpoch, ControlGeneration: first.ControlGeneration, InputSequence: 1, Heading: 0.4}
	if err := remoteManager.DispatchGameplay(fixture.ctx, session.SessionID, fixture.ownerID, "remote-connection", steer); err != nil {
		t.Fatal(err)
	}
	steer.ControlGeneration = second.ControlGeneration
	steer.Heading = -0.3
	if err := ownerManager.DispatchGameplay(fixture.ctx, session.SessionID, fixture.memberID, "local-connection", steer); err != nil {
		t.Fatal(err)
	}
	frame := awaitRocketsFrame(t, events, func(frame houseRockets.RuntimeFrame) bool {
		return frame.Controls[0].LastProcessedInputSequence == 1 && frame.Controls[1].LastProcessedInputSequence == 1
	})
	if math.Abs(frame.World.Players[0].CourseHeading-0.4) > 1e-12 || math.Abs(frame.World.Players[1].CourseHeading+0.3) > 1e-12 {
		t.Fatalf("routed heading = %+v", frame.World.Players)
	}
	// Invalid/duplicate remote messages are published but never processed/ACKed.
	steer.ControlGeneration = first.ControlGeneration
	steer.Heading = 1.7
	if err := remoteManager.DispatchGameplay(fixture.ctx, session.SessionID, fixture.ownerID, "remote-connection", steer); err != nil {
		t.Fatal(err)
	}
	steer.InputSequence = 99
	steer.RuntimeEpoch--
	if err := remoteManager.DispatchGameplay(fixture.ctx, session.SessionID, fixture.ownerID, "remote-connection", steer); err != nil {
		t.Fatal(err)
	}
	next := awaitRocketsFrame(t, events, func(candidate houseRockets.RuntimeFrame) bool { return candidate.World.Tick >= frame.World.Tick+12 })
	if next.Controls[0].LastProcessedInputSequence != 1 || math.Abs(next.World.Players[0].CourseHeading-0.4) > 1e-12 {
		t.Fatal("stale remote input changed authoritative state")
	}
	lease, exists, err := ownerCoordinator.CurrentRoomOwner(fixture.ctx, session.SessionID)
	if err != nil || !exists {
		t.Fatal(err)
	}
	if _, err := ownerCoordinator.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	select {
	case runtimeError := <-ownerManager.Errors():
		// Earlier rejected inputs may be buffered. Wait for lease-loss report.
		for !errors.Is(runtimeError.Err, coordinationAbstract.ErrLeaseLost) {
			select {
			case runtimeError = <-ownerManager.Errors():
			case <-deadline.C:
				t.Fatal("lease loss was not detected")
			}
		}
	case <-deadline.C:
		t.Fatal("lease loss was not detected")
	}
	recovered, err := remoteManager.EnsureRoom(fixture.ctx, session.SessionID)
	if err != nil || !recovered.Local || recovered.RuntimeEpoch <= ownership.RuntimeEpoch {
		t.Fatalf("checkpoint takeover failed: %+v %v", recovered, err)
	}
}

func TestRuntimeOwnerCASAndRedisCounterReset(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := persistRocketsSession(t, fixture, false)
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	initial, err := owners.FindRuntimeOwner(fixture.ctx, session.SessionID)
	if err != nil || initial.Generation != 0 {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan gameAbstract.RuntimeOwner, 2)
	for _, id := range []string{"first", "second"} {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			owner, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, gameAbstract.RuntimeOwner{SessionID: session.SessionID, OwnerInstanceID: id, LeaseID: id})
			if err == nil {
				results <- owner
			} else if !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
				t.Errorf("CAS error: %v", err)
			}
		}(id)
	}
	workers.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("CAS winners=%d", len(results))
	}
	winner := <-results
	if _, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, winner); !errors.Is(err, gameAbstract.ErrRuntimeOwnerConflict) {
		t.Fatalf("old CAS = %v", err)
	}
	redisURL := prepareRedis(t)
	coordinator := newTestCoordinator(t, redisURL, "reset-owner")
	lease, acquired, err := coordinator.AcquireRoom(fixture.ctx, session.SessionID, 5*time.Second)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	claimed, err := owners.ClaimRuntimeOwner(fixture.ctx, winner.Generation, gameAbstract.RuntimeOwner{SessionID: session.SessionID, OwnerInstanceID: lease.OwnerInstanceID, LeaseID: lease.LeaseID})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReleaseRoom(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	fenceKey := "houseflow:coordination:v1:room:" + base64.RawURLEncoding.EncodeToString([]byte(session.SessionID)) + ":fence"
	if err := client.Del(fixture.ctx, fenceKey).Err(); err != nil {
		t.Fatal(err)
	}
	newLease, acquired, err := coordinator.AcquireRoom(fixture.ctx, session.SessionID, 5*time.Second)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	if newLease.FencingToken != lease.FencingToken {
		t.Fatal("test must actually reset Redis counter")
	}
	next, err := owners.ClaimRuntimeOwner(fixture.ctx, claimed.Generation, gameAbstract.RuntimeOwner{SessionID: session.SessionID, OwnerInstanceID: newLease.OwnerInstanceID, LeaseID: newLease.LeaseID})
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation <= claimed.Generation || !next.Started {
		t.Fatal("durable epoch/start barrier was reset")
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, claimed); !errors.Is(err, gameAbstract.ErrRuntimeRecoveryRequired) {
		t.Fatalf("old owner start = %v", err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, next); !errors.Is(err, gameAbstract.ErrRuntimeRecoveryRequired) {
		t.Fatalf("spawn replay = %v", err)
	}
}

type slowGamePublisher struct {
	*infrastructureCoordination.RedisCoordinator
	blocked  chan struct{}
	release  chan struct{}
	once     sync.Once
	renewals atomic.Int64
}

func (coordinator *slowGamePublisher) RenewRoom(ctx context.Context, lease coordinationAbstract.RoomLease, ttl time.Duration) (bool, error) {
	coordinator.renewals.Add(1)
	return coordinator.RedisCoordinator.RenewRoom(ctx, lease, ttl)
}
func (coordinator *slowGamePublisher) PublishRoomEvent(ctx context.Context, lease coordinationAbstract.RoomLease, envelope coordinationAbstract.MessageEnvelope) error {
	if envelope.Type == realtime.GameRuntimeSnapshotEventType {
		coordinator.once.Do(func() {
			close(coordinator.blocked)
			select {
			case <-coordinator.release:
			case <-ctx.Done():
			}
		})
	}
	return coordinator.RedisCoordinator.PublishRoomEvent(ctx, lease, envelope)
}

func TestHouseRocketsRuntimeSlowPublishingDoesNotBlockPhysicsOrRenewal(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := persistRocketsSession(t, fixture, true)
	redisURL := prepareRedis(t)
	base := newTestCoordinator(t, redisURL, "slow-publisher")
	coordinator := &slowGamePublisher{RedisCoordinator: base, blocked: make(chan struct{}), release: make(chan struct{})}
	manager, err := realtime.NewRoomManager(coordinator, fixture.repository, newGameSessionMediator(fixture), realtime.RoomManagerOptions{LeaseTTL: 5 * time.Second, RenewInterval: 100 * time.Millisecond, CommandTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	defer closeRoomManager(t, manager)
	events, err := base.SubscribeRoomEvents(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	if _, err := manager.EnsureRoom(fixture.ctx, session.SessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-coordinator.blocked:
	case <-time.After(time.Second):
		t.Fatal("publisher did not block")
	}
	// Test-only delay: production physics has no sleep or synchronous I/O.
	timer := time.NewTimer(350 * time.Millisecond)
	<-timer.C
	if coordinator.renewals.Load() < 3 {
		t.Fatal("lease renewal was blocked by fanout I/O")
	}
	close(coordinator.release)
	frame := awaitRocketsFrame(t, events, func(frame houseRockets.RuntimeFrame) bool { return frame.World.Tick >= 30 })
	if frame.Phase != houseRockets.PhasePlaying {
		t.Fatal("slow fanout stopped physics")
	}
}
