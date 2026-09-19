package tests

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	coordinationAbstract "houseflowApi/internal/application/coordination/abstract"
	gameCommands "houseflowApi/internal/application/game/commands"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/data/entities"
	infrastructureCoordination "houseflowApi/internal/infrastructure/coordination"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/infrastructure/realtime"
)

const roomRuntimeTestTimeout = 5 * time.Second

func TestRoomRuntimeAllowsOnlyOneLocalOwnerAcrossInstances(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := fixture.createSession(t)
	redisURL := prepareRedis(t)
	firstCoordinator := newTestCoordinator(t, redisURL, "runtime-owner-a")
	secondCoordinator := newTestCoordinator(t, redisURL, "runtime-owner-b")
	firstManager := newTestRoomManager(t, fixture, firstCoordinator)
	secondManager := newTestRoomManager(t, fixture, secondCoordinator)

	start := make(chan struct{})
	ownerships := make(chan realtime.RoomOwnership, 2)
	errorsChannel := make(chan error, 2)
	var workers sync.WaitGroup
	for _, manager := range []*realtime.RoomManager{firstManager, secondManager} {
		workers.Add(1)
		go func(candidate *realtime.RoomManager) {
			defer workers.Done()
			<-start
			ownership, err := candidate.EnsureRoom(fixture.ctx, session.SessionID)
			ownerships <- ownership
			errorsChannel <- err
		}(manager)
	}
	close(start)
	workers.Wait()
	close(ownerships)
	close(errorsChannel)

	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	localOwners := 0
	var ownerInstanceID string
	var fencingToken int64
	for ownership := range ownerships {
		if ownerInstanceID == "" {
			ownerInstanceID = ownership.OwnerInstanceID
			fencingToken = ownership.FencingToken
		}
		if ownership.OwnerInstanceID != ownerInstanceID || ownership.FencingToken != fencingToken {
			t.Fatalf("instances observed different owners: %+v", ownership)
		}
		if ownership.Local {
			localOwners++
		}
	}
	if localOwners != 1 {
		t.Fatalf("local owner count = %d, want 1", localOwners)
	}
}

func TestRoomRuntimeRoutesCommandsInOrderAndReplaysIdempotently(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := fixture.createSession(t)
	redisURL := prepareRedis(t)
	ownerCoordinator := newTestCoordinator(t, redisURL, "runtime-command-owner")
	senderCoordinator := newTestCoordinator(t, redisURL, "runtime-command-sender")
	ownerManager := newTestRoomManager(t, fixture, ownerCoordinator)
	senderManager := newTestRoomManager(t, fixture, senderCoordinator)

	ownership, err := ownerManager.EnsureRoom(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ownership.Local || ownership.OwnerInstanceID != ownerCoordinator.InstanceID() {
		t.Fatalf("room ownership = %+v", ownership)
	}

	events, err := senderCoordinator.SubscribeRoomEvents(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	command := coordinationAbstract.MessageEnvelope{
		MessageID: "runtime-join-member",
		RoomID:    session.SessionID,
		Type:      gameCommands.JoinGameSessionCommandType,
		ActorID:   fixture.memberID,
	}

	if err := senderManager.Dispatch(fixture.ctx, command); err != nil {
		t.Fatal(err)
	}
	first := awaitRoomSnapshot(t, events)
	if first.Version != 2 || len(first.Players) != 1 || first.Players[0].PlayerID != fixture.memberID {
		t.Fatalf("first routed snapshot = %+v", first)
	}

	if err := senderManager.Dispatch(fixture.ctx, command); err != nil {
		t.Fatal(err)
	}
	replayed := awaitRoomSnapshot(t, events)
	if replayed.Version != first.Version || len(replayed.Players) != 1 {
		t.Fatalf("replayed snapshot = %+v, first = %+v", replayed, first)
	}

	stored, err := fixture.repository.FindByID(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version() != 2 || len(stored.Players()) != 1 {
		t.Fatalf("stored session version/players = %d/%d, want 2/1", stored.Version(), len(stored.Players()))
	}
	assertCollectionCount(t, fixture, entities.GameSessionCommandReceiptCollectionName, 2)
	assertCollectionCount(t, fixture, entities.OutboxMessageCollectionName, 2)
}

func TestRoomRuntimeRecoversPersistedDeadlinesAfterOwnerHandoff(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	session := createRuntimeSession(t, fixture, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 800 * time.Millisecond,
		CountdownDuration:   200 * time.Millisecond,
	})
	joinRuntimePlayer(t, fixture, session.SessionID, fixture.ownerID, "runtime-join-owner")
	joinRuntimePlayer(t, fixture, session.SessionID, fixture.memberID, "runtime-join-member")
	setRuntimePlayerReady(t, fixture, session.SessionID, fixture.ownerID, "runtime-ready-owner")
	readyWindow := setRuntimePlayerReady(t, fixture, session.SessionID, fixture.memberID, "runtime-ready-member")
	if readyWindow.State != gameDomain.SessionReadyWindow {
		t.Fatalf("prepared session state = %s, want readyWindow", readyWindow.State)
	}

	redisURL := prepareRedis(t)
	firstCoordinator := newTestCoordinator(t, redisURL, "runtime-deadline-a")
	secondCoordinator := newTestCoordinator(t, redisURL, "runtime-deadline-b")
	observerCoordinator := newTestCoordinator(t, redisURL, "runtime-deadline-observer")
	firstManager := newTestRoomManager(t, fixture, firstCoordinator)
	secondManager := newTestRoomManager(t, fixture, secondCoordinator)
	events, err := observerCoordinator.SubscribeRoomEvents(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })

	firstOwnership, err := firstManager.EnsureRoom(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	closeRoomManager(t, firstManager)
	secondOwnership, err := secondManager.EnsureRoom(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !secondOwnership.Local || secondOwnership.FencingToken <= firstOwnership.FencingToken {
		t.Fatalf("handoff ownership first=%+v second=%+v", firstOwnership, secondOwnership)
	}

	eventVersions := make([]int64, 0, 2)
	deadline := time.Now().Add(roomRuntimeTestTimeout)
	for time.Now().Before(deadline) {
		stored, findErr := fixture.repository.FindByID(fixture.ctx, session.SessionID)
		if findErr != nil {
			t.Fatal(findErr)
		}
		if stored.Snapshot().State == gameDomain.SessionRunning {
			break
		}
		select {
		case event := <-events.Messages():
			if event.Type != realtime.GameSessionSnapshotEventType {
				t.Fatalf("event type = %q", event.Type)
			}
			eventVersions = append(eventVersions, event.Sequence)
		case subscriptionErr := <-events.Errors():
			t.Fatal(subscriptionErr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	finalSnapshot := stored.Snapshot()
	if finalSnapshot.State != gameDomain.SessionRunning || finalSnapshot.StartedAt.IsZero() {
		t.Fatalf("final snapshot = %+v", finalSnapshot)
	}
	for index := 1; index < len(eventVersions); index++ {
		if eventVersions[index] <= eventVersions[index-1] {
			t.Fatalf("event versions are not increasing: %v", eventVersions)
		}
	}
}

func newTestRoomManager(
	t *testing.T,
	fixture *gameSessionApplicationFixture,
	coordinator *infrastructureCoordination.RedisCoordinator,
) *realtime.RoomManager {
	t.Helper()
	mediator := cqrs.New()
	membershipPolicy := fixture.membershipPolicy()
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.JoinGameSessionCommand](
		mediator,
		gameCommands.NewJoinGameSessionHandler(fixture.repository, membershipPolicy),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.SetPlayerReadyCommand](
		mediator,
		gameCommands.NewSetPlayerReadyHandler(fixture.repository, membershipPolicy),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.LeaveGameSessionCommand](
		mediator,
		gameCommands.NewLeaveGameSessionHandler(fixture.repository, membershipPolicy),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.CancelGameSessionCommand](
		mediator,
		gameCommands.NewCancelGameSessionHandler(fixture.repository, membershipPolicy),
	)
	cqrs.MustRegister[gameDomain.SessionSnapshot, gameCommands.AdvanceGameSessionCommand](
		mediator,
		gameCommands.NewAdvanceGameSessionHandler(fixture.repository),
	)
	manager, err := realtime.NewRoomManager(coordinator, fixture.repository, mediator, realtime.RoomManagerOptions{
		LeaseTTL:       5 * time.Second,
		RenewInterval:  500 * time.Millisecond,
		CommandTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRoomManager(t, manager) })
	return manager
}

func closeRoomManager(t *testing.T, manager *realtime.RoomManager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Close(ctx); err != nil {
		t.Errorf("close room manager: %v", err)
	}
}

func createRuntimeSession(
	t *testing.T,
	fixture *gameSessionApplicationFixture,
	rules gameDomain.SessionRules,
) gameDomain.SessionSnapshot {
	t.Helper()
	handler := gameCommands.NewCreateGameSessionHandler(fixture.repository, fixture.membershipPolicy())
	snapshot, err := handler.Handle(fixture.ctx, gameCommands.CreateGameSessionCommand{
		CommandID:       "runtime-create-session",
		UserID:          fixture.ownerID,
		HouseID:         fixture.houseID,
		GameKey:         "shared-game",
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules:           rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func joinRuntimePlayer(
	t *testing.T,
	fixture *gameSessionApplicationFixture,
	sessionID string,
	userID string,
	commandID string,
) {
	t.Helper()
	handler := gameCommands.NewJoinGameSessionHandler(fixture.repository, fixture.membershipPolicy())
	if _, err := handler.Handle(fixture.ctx, gameCommands.JoinGameSessionCommand{
		CommandID: commandID,
		SessionID: sessionID,
		UserID:    userID,
	}); err != nil {
		t.Fatal(err)
	}
}

func setRuntimePlayerReady(
	t *testing.T,
	fixture *gameSessionApplicationFixture,
	sessionID string,
	userID string,
	commandID string,
) gameDomain.SessionSnapshot {
	t.Helper()
	handler := gameCommands.NewSetPlayerReadyHandler(fixture.repository, fixture.membershipPolicy())
	snapshot, err := handler.Handle(fixture.ctx, gameCommands.SetPlayerReadyCommand{
		CommandID: commandID,
		SessionID: sessionID,
		UserID:    userID,
		Ready:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func awaitRoomSnapshot(
	t *testing.T,
	subscription coordinationAbstract.RoomEventSubscription,
) gameDomain.SessionSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), roomRuntimeTestTimeout)
	defer cancel()
	select {
	case event := <-subscription.Messages():
		if event.Type != realtime.GameSessionSnapshotEventType {
			t.Fatalf("event type = %q, want %q", event.Type, realtime.GameSessionSnapshotEventType)
		}
		var snapshot gameDomain.SessionSnapshot
		if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if event.Sequence != snapshot.Version {
			t.Fatalf("event sequence = %d, snapshot version = %d", event.Sequence, snapshot.Version)
		}
		return snapshot
	case err := <-subscription.Errors():
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("room snapshot event was not delivered")
	}
	return gameDomain.SessionSnapshot{}
}
