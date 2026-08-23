package tests

import (
	"testing"
	"time"

	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/data/migrations"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestUserInfoHistoryIsStoredAsAFieldBasedDocument(t *testing.T) {
	now := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.UTC)
	history := entities.UserInfoHistory{
		Id:         primitive.NewObjectID(),
		UserId:     primitive.NewObjectID().Hex(),
		ColumnName: entities.UserInfoColumnFirstName,
		Value:      "Ada",
		UpdateOn:   now,
	}

	payload, err := bson.Marshal(history)
	if err != nil {
		t.Fatalf("marshal user info history: %v", err)
	}

	var document bson.M
	if err := bson.Unmarshal(payload, &document); err != nil {
		t.Fatalf("unmarshal user info history: %v", err)
	}

	for _, field := range []string{"userId", "columnName", "value", "updateOn"} {
		if _, ok := document[field]; !ok {
			t.Fatalf("history document does not contain %q: %#v", field, document)
		}
	}
	if document["columnName"] != entities.UserInfoColumnFirstName {
		t.Fatalf("columnName is %v, want %q", document["columnName"], entities.UserInfoColumnFirstName)
	}
}

func TestUserInfoHistoryMigrationsAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, migration := range migrations.AllMigrations() {
		registered[migration.Version()] = true
	}

	for _, version := range []string{"0029", "0030"} {
		if !registered[version] {
			t.Fatalf("migration %s is not registered", version)
		}
	}
}
