package tests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

type gameSessionPersistenceFixture struct {
	ctx        context.Context
	db         *mongo.Database
	repository gameAbstract.GameSessionRepository
}

func newGameSessionPersistenceFixture(t *testing.T) *gameSessionPersistenceFixture {
	t.Helper()
	uri := os.Getenv("HOUSEFLOW_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("HOUSEFLOW_TEST_MONGO_URI is required for game session persistence integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("houseflow_game_session_test_" + primitive.NewObjectID().Hex())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := db.Drop(cleanupCtx); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		if err := client.Disconnect(cleanupCtx); err != nil {
			t.Errorf("disconnect test client: %v", err)
		}
	})
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunAll(ctx, db, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}

	return &gameSessionPersistenceFixture{
		ctx:        ctx,
		db:         db,
		repository: database.NewGameSessionRepository(client, db.Name()),
	}
}

func TestGameSessionRepositoryPersistsSnapshotReceiptAndActiveSlotAtomically(t *testing.T) {
	fixture := newGameSessionPersistenceFixture(t)
	session := newPersistenceTestSession(t)

	if _, err := fixture.repository.Create(
		fixture.ctx,
		session.Snapshot(),
		session.PendingEvents(),
		persistenceCommand("create-1", "gameSession.create", "create-payload"),
	); err != nil {
		t.Fatal(err)
	}
	session.ClearPendingEvents()

	stored, err := fixture.repository.FindActive(fixture.ctx, "house-1", "shared-game")
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
	fixture := newGameSessionPersistenceFixture(t)
	_, _ = seedGameSession(t, fixture)

	firstWriter, err := fixture.repository.FindByID(fixture.ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	secondWriter, err := fixture.repository.FindByID(fixture.ctx, "session-1")
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
		_, err := fixture.repository.Save(
			fixture.ctx,
			1,
			session.Snapshot(),
			session.PendingEvents(),
			persistenceCommand("join-"+playerID, "gameSession.join", playerID),
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

	stored, err := fixture.repository.FindByID(fixture.ctx, "session-1")
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
	fixture := newGameSessionPersistenceFixture(t)
	session, _ := seedGameSession(t, fixture)
	// The receipt is inserted first; a rejected snapshot must roll it back.
	if err := fixture.db.RunCommand(fixture.ctx, bson.D{{Key: "collMod", Value: database.GameSessionCollectionName}, {Key: "validator", Value: bson.M{"players": bson.M{"$size": 0}}}}).Err(); err != nil {
		t.Fatal(err)
	}

	if err := session.JoinPlayer("player-1", persistenceTestTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.Save(
		fixture.ctx,
		1,
		session.Snapshot(),
		session.PendingEvents(),
		persistenceCommand("join-rollback", "gameSession.join", "rollback-payload"),
	); err == nil {
		t.Fatal("save must fail when the snapshot write is rejected")
	}

	stored, err := fixture.repository.FindByID(fixture.ctx, "session-1")
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
	fixture := newGameSessionPersistenceFixture(t)
	session, _ := seedGameSession(t, fixture)
	endedAt := persistenceTestTime().Add(time.Minute)
	if err := session.Cancel(gameDomain.EndReasonCancelledByUser, endedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.Save(fixture.ctx, 1, session.Snapshot(), session.PendingEvents(), persistenceCommand("cancel-1", "gameSession.cancel", "cancel-payload")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, "house-1", "shared-game"); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("cancelled session still active: %v", err)
	}
	expiresAt := assertStoredSessionFields(t, fixture, "session-1", "", true)
	if !expiresAt.Equal(endedAt.Add(database.CancelledSessionRetention)) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, endedAt.Add(database.CancelledSessionRetention))
	}
}

func TestGameSessionRepositorySystemTransitionsKeepNoReceipt(t *testing.T) {
	fixture := newGameSessionPersistenceFixture(t)
	session, _ := seedGameSession(t, fixture)
	system := gameAbstract.CommandDescriptor{CommandID: "advance-1", ActorID: gameAbstract.SystemActorID, CommandType: "gameSession.advance", PayloadHash: "advance"}
	if _, err := fixture.repository.Save(fixture.ctx, 1, session.Snapshot(), nil, system); err != nil {
		t.Fatal(err)
	}
	if err := session.Cancel(gameDomain.EndReasonInsufficientPlayers, persistenceTestTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.Save(fixture.ctx, 1, session.Snapshot(), session.PendingEvents(), system); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.repository.FindProcessedCommand(fixture.ctx, system); err != nil || found {
		t.Fatalf("system receipt lookup = %t %v", found, err)
	}
	assertReceiptCount(t, fixture, 1)
}

// The recovery scanner pages only active slots, in stable cursor order.
func TestGameSessionRepositoryPagesActiveSessionsOnly(t *testing.T) {
	fixture := newGameSessionPersistenceFixture(t)
	for index, gameKey := range []string{"game-a", "game-b", "game-c", "game-d"} {
		session := newPersistenceTestSessionFor(t, fmt.Sprintf("scan-%d", index), gameKey)
		if gameKey == "game-c" {
			if err := session.Cancel(gameDomain.EndReasonCancelledByUser, persistenceTestTime()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("scan-create-"+gameKey, "gameSession.create", gameKey)); err != nil {
			t.Fatal(err)
		}
	}
	scanner := fixture.repository.(gameAbstract.ActiveGameSessionScanner)
	var seen []string
	after := ""
	for page := 0; page < 3; page++ {
		items, err := scanner.ListActiveSessions(fixture.ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
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
	fixture := newGameSessionPersistenceFixture(t)
	session := newPersistenceTestSession(t)

	_, err := fixture.repository.Create(
		fixture.ctx,
		session.Snapshot(),
		nil,
		persistenceCommand("create-invalid", "gameSession.create", "invalid-payload"),
	)
	if !errors.Is(err, gameAbstract.ErrInvalidPersistenceBatch) {
		t.Fatalf("create error = %v, want ErrInvalidPersistenceBatch", err)
	}
	count, countErr := fixture.db.Collection(database.GameSessionCollectionName).
		CountDocuments(fixture.ctx, bson.M{})
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
	fixture *gameSessionPersistenceFixture,
) (*gameDomain.GameSession, string) {
	t.Helper()
	session := newPersistenceTestSession(t)
	events := session.PendingEvents()
	if _, err := fixture.repository.Create(
		fixture.ctx,
		session.Snapshot(),
		events,
		persistenceCommand("create-seed", "gameSession.create", "seed-payload"),
	); err != nil {
		t.Fatal(err)
	}
	session.ClearPendingEvents()
	return session, events[0].EventID
}

func assertReceiptCount(t *testing.T, fixture *gameSessionPersistenceFixture, expected int64) {
	t.Helper()
	count, err := fixture.db.Collection(entities.GameSessionCommandReceiptCollectionName).
		CountDocuments(fixture.ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("command receipt count = %d, want %d", count, expected)
	}
}

func assertStoredSessionFields(t *testing.T, fixture *gameSessionPersistenceFixture, sessionID, activeKey string, expires bool) time.Time {
	t.Helper()
	var document struct {
		ActiveKey string     `bson:"activeKey"`
		ExpiresAt *time.Time `bson:"expiresAt"`
	}
	if err := fixture.db.Collection(database.GameSessionCollectionName).FindOne(fixture.ctx, bson.M{"_id": sessionID}).Decode(&document); err != nil {
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

func persistenceCommand(commandID string, commandType string, payloadHash string) gameAbstract.CommandDescriptor {
	return gameAbstract.CommandDescriptor{
		CommandID:   commandID,
		ActorID:     "user-1",
		CommandType: commandType,
		PayloadHash: payloadHash,
	}
}
