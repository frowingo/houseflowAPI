package gamecore

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/tests/testkit/gamekit"

	"go.mongodb.org/mongo-driver/bson"
)

func TestGameSessionRepositoryPersistsSnapshotReceiptAndActiveSlotAtomically(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	session := newPersistenceTestSession(t)

	if _, err := fixture.Repository.Create(
		fixture.Ctx,
		session.Snapshot(),
		session.PendingEvents(),
		gamekit.PersistenceCommand("create-1", "gameSession.create", "create-payload"),
	); err != nil {
		t.Fatal(err)
	}
	session.ClearPendingEvents()

	stored, err := fixture.Repository.FindActive(fixture.Ctx, "house-1", "shared-game")
	if err != nil {
		t.Fatal(err)
	}
	storedSnapshot := stored.Snapshot()
	if storedSnapshot.Version != 1 || storedSnapshot.State != gameDomain.SessionLobby {
		t.Fatalf("stored snapshot version/state = %d/%s, want 1/lobby", storedSnapshot.Version, storedSnapshot.State)
	}
	if storedSnapshot.HouseID != "house-1" || storedSnapshot.GameKey != "shared-game" {
		t.Fatalf("stored snapshot identity = %s/%s", storedSnapshot.HouseID, storedSnapshot.GameKey)
	}
	if len(stored.PendingEvents()) != 0 {
		t.Fatal("restored session must not contain pending events")
	}
	assertStoredSessionFields(t, fixture, storedSnapshot.SessionID, "house-1:shared-game", false)
	assertReceiptCount(t, fixture, 1)
}

func TestGameSessionRepositoryRejectsConcurrentUpdateWithoutExtraReceipt(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	_, _ = seedGameSession(t, fixture)

	firstWriter, err := fixture.Repository.FindByID(fixture.Ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	secondWriter, err := fixture.Repository.FindByID(fixture.Ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	changeTime := persistenceTestTime().Add(time.Second)
	if err := firstWriter.JoinPlayer("player-1", changeTime); err != nil {
		t.Fatal(err)
	}
	if err := secondWriter.JoinPlayer("player-2", changeTime); err != nil {
		t.Fatal(err)
	}

	type saveResult struct {
		playerID string
		err      error
	}
	start := make(chan struct{})
	results := make(chan saveResult, 2)
	save := func(playerID string, session *gameDomain.GameSession) {
		<-start
		_, err := fixture.Repository.Save(
			fixture.Ctx,
			1,
			session.Snapshot(),
			session.PendingEvents(),
			gamekit.PersistenceCommand("join-"+playerID, "gameSession.join", playerID),
		)
		results <- saveResult{
			playerID: playerID,
			err:      err,
		}
	}
	go save("player-1", firstWriter)
	go save("player-2", secondWriter)
	close(start)

	var winnerID string
	var loserID string
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			if winnerID != "" {
				t.Fatal("both concurrent saves succeeded")
			}
			winnerID = result.playerID
		case errors.Is(result.err, gameAbstract.ErrConcurrentSessionUpdate):
			loserID = result.playerID
		default:
			t.Fatalf("concurrent save error = %v, want ErrConcurrentSessionUpdate", result.err)
		}
	}
	if winnerID == "" || loserID == "" {
		t.Fatalf("winner/loser = %q/%q, want exactly one of each", winnerID, loserID)
	}

	stored, err := fixture.Repository.FindByID(fixture.Ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version() != 2 {
		t.Fatalf("stored version = %d, want 2", stored.Version())
	}
	if _, exists := stored.Player(winnerID); !exists {
		t.Fatal("winning writer's player is missing")
	}
	if _, exists := stored.Player(loserID); exists {
		t.Fatal("losing writer's player must not be persisted")
	}
	assertReceiptCount(t, fixture, 2)
}

func TestGameSessionRepositoryRollsBackReceiptWhenSnapshotWriteFails(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	session, _ := seedGameSession(t, fixture)
	// The receipt is inserted first; a rejected snapshot must roll it back.
	if err := fixture.DB.RunCommand(fixture.Ctx, bson.D{{Key: "collMod", Value: database.GameSessionCollectionName}, {Key: "validator", Value: bson.M{"players": bson.M{"$size": 0}}}}).Err(); err != nil {
		t.Fatal(err)
	}

	if err := session.JoinPlayer("player-1", persistenceTestTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.Save(
		fixture.Ctx,
		1,
		session.Snapshot(),
		session.PendingEvents(),
		gamekit.PersistenceCommand("join-rollback", "gameSession.join", "rollback-payload"),
	); err == nil {
		t.Fatal("save must fail when the snapshot write is rejected")
	}

	stored, err := fixture.Repository.FindByID(fixture.Ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version() != 1 {
		t.Fatalf("stored version = %d, want rollback to version 1", stored.Version())
	}
	if _, exists := stored.Player("player-1"); exists {
		t.Fatal("snapshot update must roll back")
	}
	assertReceiptCount(t, fixture, 1)
}

func TestGameSessionRepositoryCancellationReleasesSlotAndExpires(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	session, _ := seedGameSession(t, fixture)
	endedAt := persistenceTestTime().Add(time.Minute)
	if err := session.Cancel(gameDomain.EndReasonCancelledByUser, endedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.Save(fixture.Ctx, 1, session.Snapshot(), session.PendingEvents(), gamekit.PersistenceCommand("cancel-1", "gameSession.cancel", "cancel-payload")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.FindActive(fixture.Ctx, "house-1", "shared-game"); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("cancelled session still active: %v", err)
	}
	expiresAt := assertStoredSessionFields(t, fixture, "session-1", "", true)
	if !expiresAt.Equal(endedAt.Add(database.CancelledSessionRetention)) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, endedAt.Add(database.CancelledSessionRetention))
	}
}

func TestGameSessionRepositorySystemTransitionsKeepNoReceipt(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	session, _ := seedGameSession(t, fixture)
	system := gameAbstract.CommandDescriptor{CommandID: "advance-1", ActorID: gameAbstract.SystemActorID, CommandType: "gameSession.advance", PayloadHash: "advance"}
	if _, err := fixture.Repository.Save(fixture.Ctx, 1, session.Snapshot(), nil, system); err != nil {
		t.Fatal(err)
	}
	if err := session.Cancel(gameDomain.EndReasonInsufficientPlayers, persistenceTestTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Repository.Save(fixture.Ctx, 1, session.Snapshot(), session.PendingEvents(), system); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.Repository.FindProcessedCommand(fixture.Ctx, system); err != nil || found {
		t.Fatalf("system receipt lookup = %t %v", found, err)
	}
	assertReceiptCount(t, fixture, 1)
}

// The recovery scanner pages only active slots, in stable cursor order.
func TestGameSessionRepositoryPagesActiveSessionsOnly(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	for index, gameKey := range []string{"game-a", "game-b", "game-c", "game-d"} {
		session := newPersistenceTestSessionFor(t, fmt.Sprintf("scan-%d", index), gameKey)
		if gameKey == "game-c" {
			if err := session.Cancel(gameDomain.EndReasonCancelledByUser, persistenceTestTime()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := fixture.Repository.Create(fixture.Ctx, session.Snapshot(), session.PendingEvents(), gamekit.PersistenceCommand("scan-create-"+gameKey, "gameSession.create", gameKey)); err != nil {
			t.Fatal(err)
		}
	}
	scanner := fixture.Repository.(gameAbstract.ActiveGameSessionScanner)
	var seen []string
	after := ""
	for page := 0; page < 3; page++ {
		items, err := scanner.ListActiveSessions(fixture.Ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.State != gameDomain.SessionLobby {
				t.Fatalf("scanned state of %s = %q", item.SessionID, item.State)
			}
			seen = append(seen, item.SessionID+"/"+item.GameKey)
			after = item.Cursor
		}
		if len(items) < 2 {
			break
		}
	}
	if strings.Join(seen, ",") != "scan-0/game-a,scan-1/game-b,scan-3/game-d" {
		t.Fatalf("scanned sessions = %v", seen)
	}
}

func TestGameSessionRepositoryRejectsInconsistentPersistenceBatch(t *testing.T) {
	fixture := gamekit.NewGameSessionPersistenceFixture(t)
	session := newPersistenceTestSession(t)

	_, err := fixture.Repository.Create(
		fixture.Ctx,
		session.Snapshot(),
		nil,
		gamekit.PersistenceCommand("create-invalid", "gameSession.create", "invalid-payload"),
	)
	if !errors.Is(err, gameAbstract.ErrInvalidPersistenceBatch) {
		t.Fatalf("create error = %v, want ErrInvalidPersistenceBatch", err)
	}
	count, countErr := fixture.DB.Collection(database.GameSessionCollectionName).
		CountDocuments(fixture.Ctx, bson.M{})
	if countErr != nil {
		t.Fatal(countErr)
	}
	if count != 0 {
		t.Fatalf("stored sessions = %d, want 0", count)
	}
}

func newPersistenceTestSession(t *testing.T) *gameDomain.GameSession {
	t.Helper()
	return newPersistenceTestSessionFor(t, "session-1", "shared-game")
}

func newPersistenceTestSessionFor(t *testing.T, sessionID, gameKey string) *gameDomain.GameSession {
	t.Helper()
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{
		SessionID:       sessionID,
		HouseID:         "house-1",
		GameKey:         gameKey,
		ProtocolVersion: 1,
		Mode:            gameDomain.RealtimeGame,
		Rules: gameDomain.SessionRules{
			MinimumPlayers:      2,
			MaximumPlayers:      4,
			ReadyWindowDuration: 30 * time.Second,
			CountdownDuration:   3 * time.Second,
		},
		CreatedBy: "user-1",
		CreatedAt: persistenceTestTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func seedGameSession(
	t *testing.T,
	fixture *gamekit.GameSessionPersistenceFixture,
) (*gameDomain.GameSession, string) {
	t.Helper()
	session := newPersistenceTestSession(t)
	events := session.PendingEvents()
	if _, err := fixture.Repository.Create(
		fixture.Ctx,
		session.Snapshot(),
		events,
		gamekit.PersistenceCommand("create-seed", "gameSession.create", "seed-payload"),
	); err != nil {
		t.Fatal(err)
	}
	session.ClearPendingEvents()
	return session, events[0].EventID
}

func assertReceiptCount(t *testing.T, fixture *gamekit.GameSessionPersistenceFixture, expected int64) {
	t.Helper()
	count, err := fixture.DB.Collection(entities.GameSessionCommandReceiptCollectionName).
		CountDocuments(fixture.Ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("command receipt count = %d, want %d", count, expected)
	}
}

func assertStoredSessionFields(t *testing.T, fixture *gamekit.GameSessionPersistenceFixture, sessionID, activeKey string, expires bool) time.Time {
	t.Helper()
	var document struct {
		ActiveKey string     `bson:"activeKey"`
		ExpiresAt *time.Time `bson:"expiresAt"`
	}
	if err := fixture.DB.Collection(database.GameSessionCollectionName).FindOne(fixture.Ctx, bson.M{"_id": sessionID}).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.ActiveKey != activeKey || (document.ExpiresAt != nil) != expires {
		t.Fatalf("stored activeKey/expiresAt = %q/%v, want %q/%t", document.ActiveKey, document.ExpiresAt, activeKey, expires)
	}
	if document.ExpiresAt == nil {
		return time.Time{}
	}
	return document.ExpiresAt.UTC()
}

func persistenceTestTime() time.Time {
	return time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
}
