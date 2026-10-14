package tests

import (
	"errors"
	"testing"
	"time"

	gameAbstract "houseflowApi/internal/application/game/abstract"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/migrations"

	"go.mongodb.org/mongo-driver/bson"
)

// Documents written before 0044 gain the active slot key and retention
// deadlines; completed history is kept.
func TestGameDataRetentionMigrationBackfillsExistingDocuments(t *testing.T) {
	fixture := newGameSessionApplicationFixture(t)
	endedAt := time.Now().UTC().Truncate(time.Millisecond)
	rules := bson.M{"minimumPlayers": 2, "maximumPlayers": 4, "readyWindowMilliseconds": int64(30000), "countdownMilliseconds": int64(3000)}
	legacySession := func(id, state string, ended bool) bson.M {
		document := bson.M{"_id": id, "houseId": fixture.houseID, "gameKey": "legacyGame-" + id, "protocolVersion": 2, "mode": "realtime", "state": state, "rules": rules, "players": bson.A{}, "createdBy": fixture.ownerID, "createdAt": endedAt.Add(-time.Minute), "updatedAt": endedAt, "version": int64(3)}
		if ended {
			document["endedAt"] = endedAt
			document["endReason"] = "cancelledByUser"
		}
		return document
	}
	sessions := fixture.db.Collection(database.GameSessionCollectionName)
	if _, err := sessions.InsertMany(fixture.ctx, []any{legacySession("lobby", "lobby", false), legacySession("cancelled", "cancelled", true), legacySession("finished", "finished", true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection(database.GameMatchResultCollectionName).InsertMany(fixture.ctx, []any{
		bson.M{"_id": "cancelled", "houseId": fixture.houseID, "gameKey": "legacyGame-cancelled", "schemaVersion": 1, "status": "cancelled", "endReason": "cancelledByUser", "startedAt": endedAt.Add(-time.Minute), "endedAt": endedAt, "resultEventType": "legacy.result", "runtimeEpoch": int64(1), "payload": []byte(`{}`), "payloadHash": "0000000000000000000000000000000000000000000000000000000000000000", "createdAt": endedAt},
		bson.M{"_id": "finished", "houseId": fixture.houseID, "gameKey": "legacyGame-finished", "schemaVersion": 1, "status": "completed", "endReason": "lastSurvivor", "startedAt": endedAt.Add(-time.Minute), "endedAt": endedAt, "resultEventType": "legacy.result", "runtimeEpoch": int64(1), "payload": []byte(`{}`), "payloadHash": "0000000000000000000000000000000000000000000000000000000000000000", "createdAt": endedAt},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection(database.RuntimeOwnerCollectionName).InsertMany(fixture.ctx, []any{
		bson.M{"_id": "lobby", "generation": int64(1), "ownerInstanceId": "owner", "leaseId": "lease", "started": false},
		bson.M{"_id": "finished", "generation": int64(1), "ownerInstanceId": "owner", "leaseId": "lease", "started": true, "completed": true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Collection("_migrations").DeleteOne(fixture.ctx, bson.M{"version": "0044"}); err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunAll(fixture.ctx, fixture.db, migrations.AllMigrations()); err != nil {
		t.Fatal(err)
	}

	type retention struct {
		ActiveKey string     `bson:"activeKey"`
		ExpiresAt *time.Time `bson:"expiresAt"`
	}
	read := func(collection, id string) retention {
		t.Helper()
		var document retention
		if err := fixture.db.Collection(collection).FindOne(fixture.ctx, bson.M{"_id": id}).Decode(&document); err != nil {
			t.Fatalf("%s/%s: %v", collection, id, err)
		}
		return document
	}
	if lobby := read(database.GameSessionCollectionName, "lobby"); lobby.ActiveKey != fixture.houseID+":legacyGame-lobby" || lobby.ExpiresAt != nil {
		t.Fatalf("active session = %+v", lobby)
	}
	cancelledExpiry := endedAt.Add(database.CancelledSessionRetention)
	for _, collection := range []string{database.GameSessionCollectionName, database.GameMatchResultCollectionName} {
		if cancelled := read(collection, "cancelled"); cancelled.ActiveKey != "" || cancelled.ExpiresAt == nil || !cancelled.ExpiresAt.Equal(cancelledExpiry) {
			t.Fatalf("%s cancelled = %+v", collection, cancelled)
		}
		if finished := read(collection, "finished"); finished.ActiveKey != "" || finished.ExpiresAt != nil {
			t.Fatalf("%s finished history = %+v", collection, finished)
		}
	}
	if owner := read(database.RuntimeOwnerCollectionName, "lobby"); owner.ExpiresAt != nil {
		t.Fatalf("active runtime owner expires: %+v", owner)
	}
	if owner := read(database.RuntimeOwnerCollectionName, "finished"); owner.ExpiresAt == nil || !owner.ExpiresAt.Equal(endedAt.Add(time.Hour)) {
		t.Fatalf("finished runtime owner = %+v", owner)
	}
	active, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, "legacyGame-lobby")
	if err != nil || active.Snapshot().SessionID != "lobby" {
		t.Fatalf("backfilled active lookup = %v", err)
	}

	for collection, index := range map[string]string{
		database.GameSessionCollectionName:     "ttlGameSessionExpiresAt",
		database.GameMatchResultCollectionName: "ttlGameMatchResultExpiresAt",
		database.RuntimeOwnerCollectionName:    "ttlGameRuntimeOwnerExpiresAt",
		"GameSessionCommandReceipt":            "ttlGameSessionCommandReceiptProcessedAt",
	} {
		assertIndexExists(t, fixture, collection, index)
	}
	assertIndexExists(t, fixture, database.GameSessionCollectionName, "uqGameSessionActiveKey")
	if _, err := fixture.repository.FindActive(fixture.ctx, fixture.houseID, "legacyGame-cancelled"); !errors.Is(err, gameAbstract.ErrGameSessionNotFound) {
		t.Fatalf("cancelled active lookup = %v", err)
	}
}

func assertIndexExists(t *testing.T, fixture *gameSessionApplicationFixture, collection, name string) {
	t.Helper()
	cursor, err := fixture.db.Collection(collection).Indexes().List(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []bson.M
	if err := cursor.All(fixture.ctx, &indexes); err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if index["name"] == name {
			return
		}
	}
	t.Fatalf("%s index %s missing", collection, name)
}
