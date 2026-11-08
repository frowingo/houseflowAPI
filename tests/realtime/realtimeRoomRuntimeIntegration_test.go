package realtime

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
	"houseflowApi/internal/infrastructure/realtime"
	"houseflowApi/tests/testkit"
	"houseflowApi/tests/testkit/gamekit"
)

const roomRuntimeTestTimeout = 5 * time.Second

func TestRoomRuntimeAllowsOnlyOneLocalOwnerAcrossInstances(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	session := fixture.CreateSession(t)
	redisURL := testkit.PrepareRedis(t)
	firstCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-owner-a")
	secondCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-owner-b")
	firstManager := gamekit.NewTestRoomManager(t, fixture, firstCoordinator)
	secondManager := gamekit.NewTestRoomManager(t, fixture, secondCoordinator)

	start := make(chan struct{})
	ownerships := make(chan realtime.RoomOwnership, 2)
	errorsChannel := make(chan error, 2)
	var workers sync.WaitGroup
	for _, manager := range []*realtime.RoomManager{firstManager, secondManager} {
		workers.Add(1)
		go func(candidate *realtime.RoomManager) {
			defer workers.Done()
			<-start
			ownership, err := candidate.EnsureRoom(fixture.Ctx, session.SessionID)
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
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	session := fixture.CreateSession(t)
	redisURL := testkit.PrepareRedis(t)
	ownerCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-command-owner")
	senderCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-command-sender")
	ownerManager := gamekit.NewTestRoomManager(t, fixture, ownerCoordinator)
	senderManager := gamekit.NewTestRoomManager(t, fixture, senderCoordinator)

	ownership, err := ownerManager.EnsureRoom(fixture.Ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ownership.Local || ownership.OwnerInstanceID != ownerCoordinator.InstanceID() {
		t.Fatalf("room ownership = %+v", ownership)
	}

	events, err := senderCoordinator.SubscribeRoomEvents(fixture.Ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	command := coordinationAbstract.MessageEnvelope{
		MessageID: "runtime-join-member",
		RoomID:    session.SessionID,
		Type:      gameCommands.JoinGameSessionCommandType,
		ActorID:   fixture.MemberID,
	}

	if err := senderManager.Dispatch(fixture.Ctx, command); err != nil {
		t.Fatal(err)
	}
	first := awaitRoomSnapshot(t, events)
	if first.Version != 2 || len(first.Players) != 1 || first.Players[0].PlayerID != fixture.MemberID {
		t.Fatalf("first routed snapshot = %+v", first)
	}

	if err := senderManager.Dispatch(fixture.Ctx, command); err != nil {
		t.Fatal(err)
	}
	replayed := awaitRoomSnapshot(t, events)
	if replayed.Version != first.Version || len(replayed.Players) != 1 {
		t.Fatalf("replayed snapshot = %+v, first = %+v", replayed, first)
	}

	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version() != 2 || len(stored.Players()) != 1 {
		t.Fatalf("stored session version/players = %d/%d, want 2/1", stored.Version(), len(stored.Players()))
	}
	gamekit.AssertCollectionCount(t, fixture, entities.GameSessionCommandReceiptCollectionName, 2)
}

func TestRoomRuntimeRecoversPersistedDeadlinesAfterOwnerHandoff(t *testing.T) {
	fixture := gamekit.NewGameSessionApplicationFixture(t)
	session := createRuntimeSession(t, fixture, gameDomain.SessionRules{
		MinimumPlayers:      2,
		MaximumPlayers:      4,
		ReadyWindowDuration: 800 * time.Millisecond,
		CountdownDuration:   200 * time.Millisecond,
	})
	joinRuntimePlayer(t, fixture, session.SessionID, fixture.OwnerID, "runtime-join-owner")
	joinRuntimePlayer(t, fixture, session.SessionID, fixture.MemberID, "runtime-join-member")
	setRuntimePlayerReady(t, fixture, session.SessionID, fixture.OwnerID, "runtime-ready-owner")
	readyWindow := setRuntimePlayerReady(t, fixture, session.SessionID, fixture.MemberID, "runtime-ready-member")
	if readyWindow.State != gameDomain.SessionReadyWindow {
		t.Fatalf("prepared session state = %s, want readyWindow", readyWindow.State)
	}

	redisURL := testkit.PrepareRedis(t)
	firstCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-deadline-a")
	secondCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-deadline-b")
	observerCoordinator := gamekit.NewTestCoordinator(t, redisURL, "runtime-deadline-observer")
	firstManager := gamekit.NewTestRoomManager(t, fixture, firstCoordinator)
	secondManager := gamekit.NewTestRoomManager(t, fixture, secondCoordinator)
	events, err := observerCoordinator.SubscribeRoomEvents(fixture.Ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })

	firstOwnership, err := firstManager.EnsureRoom(fixture.Ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	gamekit.CloseRoomManager(t, firstManager)
	secondOwnership, err := secondManager.EnsureRoom(fixture.Ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !secondOwnership.Local || secondOwnership.FencingToken <= firstOwnership.FencingToken {
		t.Fatalf("handoff ownership first=%+v second=%+v", firstOwnership, secondOwnership)
	}

	eventVersions := make([]int64, 0, 2)
	deadline := time.Now().Add(roomRuntimeTestTimeout)
	for time.Now().Before(deadline) {
		stored, findErr := fixture.Repository.FindByID(fixture.Ctx, session.SessionID)
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
	stored, err := fixture.Repository.FindByID(fixture.Ctx, session.SessionID)
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

func createRuntimeSession(
	t *testing.T,
	fixture *gamekit.GameSessionApplicationFixture,
	rules gameDomain.SessionRules,
) gameDomain.SessionSnapshot {
	t.Helper()
	catalog, err := gamekit.NewDefinitionCatalog(gameDomain.GameDefinition{
		GameKey:         "runtimeGame",
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules:           rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := gameCommands.NewEnsureActiveGameSessionHandler(
		fixture.Repository,
		catalog,
		fixture.MembershipPolicy(),
	)
	snapshot, err := handler.Handle(fixture.Ctx, gameCommands.EnsureActiveGameSessionCommand{
		CommandID: "runtime-create-session",
		UserID:    fixture.OwnerID,
		HouseID:   fixture.HouseID,
		GameKey:   "runtimeGame",
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func joinRuntimePlayer(
	t *testing.T,
	fixture *gamekit.GameSessionApplicationFixture,
	sessionID string,
	userID string,
	commandID string,
) {
	t.Helper()
	handler := gameCommands.NewJoinGameSessionHandler(fixture.Repository, fixture.MembershipPolicy())
	if _, err := handler.Handle(fixture.Ctx, gameCommands.JoinGameSessionCommand{
		CommandID: commandID,
		SessionID: sessionID,
		UserID:    userID,
	}); err != nil {
		t.Fatal(err)
	}
}

func setRuntimePlayerReady(
	t *testing.T,
	fixture *gamekit.GameSessionApplicationFixture,
	sessionID string,
	userID string,
	commandID string,
) gameDomain.SessionSnapshot {
	t.Helper()
	handler := gameCommands.NewSetPlayerReadyHandler(fixture.Repository, fixture.MembershipPolicy())
	snapshot, err := handler.Handle(fixture.Ctx, gameCommands.SetPlayerReadyCommand{
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
