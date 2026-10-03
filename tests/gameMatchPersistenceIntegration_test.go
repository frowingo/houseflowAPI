package tests

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	gameAbstract "houseflowApi/internal/application/game/abstract"
	gameDomain "houseflowApi/internal/application/game/domain"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"
)

func genericMatchCompletionFixture(t *testing.T) (*gameSessionPersistenceFixture, *database.GameMatchRepository, gameAbstract.MatchCompletion) {
	t.Helper()
	fixture := newGameSessionPersistenceFixture(t)
	now := persistenceTestTime()
	session, err := gameDomain.NewGameSession(gameDomain.NewSessionParams{SessionID: "turnGameSession", HouseID: "house", GameKey: "turnGame", ProtocolVersion: 1, Mode: gameDomain.TurnBasedGame, Rules: gameDomain.SessionRules{MinimumPlayers: 2, MaximumPlayers: 2}, CreatedBy: "player1", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"player1", "player2"} {
		if err := session.JoinPlayer(id, now); err != nil {
			t.Fatal(err)
		}
		if err := session.SetReady(id, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.repository.Create(fixture.ctx, session.Snapshot(), session.PendingEvents(), persistenceCommand("createTurnGame", "create", "turnGame")); err != nil {
		t.Fatal(err)
	}
	session, err = fixture.repository.FindByID(fixture.ctx, session.Snapshot().SessionID)
	if err != nil {
		t.Fatal(err)
	}
	owners := fixture.repository.(gameAbstract.RuntimeOwnerRepository)
	owner, err := owners.ClaimRuntimeOwner(fixture.ctx, 0, gameAbstract.RuntimeOwner{SessionID: session.Snapshot().SessionID, OwnerInstanceID: "turnOwner", LeaseID: "turnLease"})
	if err != nil {
		t.Fatal(err)
	}
	if err := owners.MarkRuntimeStarted(fixture.ctx, owner); err != nil {
		t.Fatal(err)
	}
	version := session.Version()
	endedAt := now.Add(10 * time.Second)
	if err := session.Finish("roundsCompleted", endedAt); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"winningTeam":"blue","rounds":4,"bonus":null}`)
	digest := sha256.Sum256(payload)
	completion := gameAbstract.MatchCompletion{ExpectedVersion: version, Session: session.Snapshot(), Events: session.PendingEvents(), Owner: owner, ResultEventType: "turnGame.result", Command: persistenceCommand("completeTurnGame", "turnGame.complete", hex.EncodeToString(digest[:])), Result: gameDomain.MatchResult{SessionID: session.Snapshot().SessionID, HouseID: "house", GameKey: "turnGame", SchemaVersion: 3, Status: gameDomain.MatchCompleted, EndReason: "roundsCompleted", StartedAt: session.Snapshot().StartedAt, EndedAt: endedAt, Payload: payload}}
	return fixture, database.NewGameMatchRepository(fixture.db.Client(), fixture.db.Name()), completion
}

func TestGameMatchRepositorySupportsAnotherGameAndImmutableReplay(t *testing.T) {
	fixture, matches, completion := genericMatchCompletionFixture(t)
	result, err := matches.Complete(fixture.ctx, completion)
	if err != nil {
		t.Fatal(err)
	}
	if result.GameKey != "turnGame" || result.SchemaVersion != 3 || !bytes.Equal(result.Payload, completion.Result.Payload) {
		t.Fatalf("generic payload changed: %+v", result)
	}
	stored, err := fixture.repository.FindByID(fixture.ctx, result.SessionID)
	if err != nil || stored.State() != gameDomain.SessionFinished {
		t.Fatalf("session not finished: %v", err)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, "house", "turnGame"); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("active slot not released: %v", err)
	}
	var outbox entities.OutboxMessage
	filter := bson.M{"eventId": "turnGame.result:" + result.SessionID}
	if err := fixture.db.Collection(entities.OutboxMessageCollectionName).FindOne(fixture.ctx, filter).Decode(&outbox); err != nil {
		t.Fatal(err)
	}
	if outbox.AggregateType != "gameMatch" || outbox.EventType != "turnGame.result" || !bytes.Equal(outbox.Payload, result.Payload) {
		t.Fatalf("incorrect generic outbox: %+v", outbox)
	}
	if _, err := matches.Complete(fixture.ctx, completion); err != nil {
		t.Fatalf("duplicate completion: %v", err)
	}
	changed := completion
	changed.Result.SchemaVersion++
	if _, err := matches.Complete(fixture.ctx, changed); !errors.Is(err, gameAbstract.ErrMatchResultConflict) {
		t.Fatalf("schema mutation accepted: %v", err)
	}
	changed = completion
	changed.ResultEventType = "anotherEvent"
	if _, err := matches.Complete(fixture.ctx, changed); !errors.Is(err, gameAbstract.ErrMatchResultConflict) {
		t.Fatalf("event mutation accepted: %v", err)
	}
	if err := matches.MarkPublished(fixture.ctx, result.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Collection(entities.OutboxMessageCollectionName).FindOne(fixture.ctx, filter).Decode(&outbox); err != nil || outbox.PublishedAt == nil {
		t.Fatalf("generic event not marked published: %v", err)
	}
	count, err := fixture.db.Collection(entities.OutboxMessageCollectionName).CountDocuments(fixture.ctx, filter)
	if err != nil || count != 1 {
		t.Fatalf("duplicate outbox: %d %v", count, err)
	}
}

func TestGameMatchRepositoryRejectsCrossSessionMetadataBeforeWriting(t *testing.T) {
	fixture, matches, completion := genericMatchCompletionFixture(t)
	for name, mutate := range map[string]func(*gameAbstract.MatchCompletion){
		"house":     func(c *gameAbstract.MatchCompletion) { c.Result.HouseID = "anotherHouse" },
		"game":      func(c *gameAbstract.MatchCompletion) { c.Result.GameKey = "anotherGame" },
		"session":   func(c *gameAbstract.MatchCompletion) { c.Result.SessionID = "anotherSession" },
		"time":      func(c *gameAbstract.MatchCompletion) { c.Result.StartedAt = c.Result.StartedAt.Add(time.Second) },
		"status":    func(c *gameAbstract.MatchCompletion) { c.Result.Status = gameDomain.MatchCancelled },
		"eventType": func(c *gameAbstract.MatchCompletion) { c.ResultEventType = "" },
		"hash":      func(c *gameAbstract.MatchCompletion) { c.Command.PayloadHash = "wrongHash" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := completion
			mutate(&changed)
			if _, err := matches.Complete(fixture.ctx, changed); !errors.Is(err, gameAbstract.ErrInvalidPersistenceBatch) {
				t.Fatalf("invalid metadata accepted: %v", err)
			}
		})
	}
	if _, err := matches.FindResult(fixture.ctx, completion.Result.SessionID); !errors.Is(err, gameAbstract.ErrMatchResultNotFound) {
		t.Fatalf("invalid result written: %v", err)
	}
	if _, err := fixture.repository.FindActive(fixture.ctx, "house", "turnGame"); err != nil {
		t.Fatalf("invalid result released slot: %v", err)
	}
}

func TestGameMatchRepositoryDetectsPayloadCorruption(t *testing.T) {
	fixture, matches, completion := genericMatchCompletionFixture(t)
	if _, err := matches.Complete(fixture.ctx, completion); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.db.Collection(database.GameMatchResultCollectionName).UpdateOne(fixture.ctx, bson.M{"_id": completion.Result.SessionID}, bson.M{"$set": bson.M{"payload": []byte(`{"winningTeam":"red"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := matches.FindResult(fixture.ctx, completion.Result.SessionID); !errors.Is(err, gameAbstract.ErrInvalidPersistenceBatch) {
		t.Fatalf("corrupt payload accepted: %v", err)
	}
}

func TestGameMatchMigrationIsRepeatableWithoutChangingStoredResults(t *testing.T) {
	fixture, matches, completion := genericMatchCompletionFixture(t)
	if _, err := matches.Complete(fixture.ctx, completion); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, migration := range migrations.AllMigrations() {
		if migration.Version() == "0042" {
			found = true
			if migration.Name() != "gameMatchResult" {
				t.Fatalf("0042 is not the shared result migration: %s", migration.Name())
			}
			for range 2 {
				if err := migration.Up(fixture.ctx, fixture.db); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if !found {
		t.Fatal("0042 migration missing")
	}
	stored, err := matches.FindResult(fixture.ctx, completion.Result.SessionID)
	if err != nil || !bytes.Equal(stored.Payload, completion.Result.Payload) {
		t.Fatalf("migration changed immutable result: %+v %v", stored, err)
	}
	count, err := fixture.db.Collection(entities.OutboxMessageCollectionName).CountDocuments(fixture.ctx, bson.M{"eventType": "turnGame.result"})
	if err != nil || count != 1 {
		t.Fatalf("migration recreated publication: %d %v", count, err)
	}
	collections, err := fixture.db.ListCollectionNames(fixture.ctx, bson.M{"name": "HouseRocketsMatchResult"})
	if err != nil || len(collections) != 0 {
		t.Fatalf("unused House Rockets result collection created: %v %v", collections, err)
	}
}
