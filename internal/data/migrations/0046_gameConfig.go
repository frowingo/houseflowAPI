package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const gameConfigCollection = "Game"

// gameConfig moves game availability and tunable values from environment
// variables and code into the Game collection. House Rockets is seeded with
// the values release 1.3.10 runs with, enabled as HOUSE_ROCKETS_ENABLED was in
// production; an existing document is never overwritten.
type gameConfig struct{}

func (m *gameConfig) Version() string { return "0046" }
func (m *gameConfig) Name() string    { return "gameConfig" }

func (m *gameConfig) Up(ctx context.Context, db *mongo.Database) error {
	if err := createGameConfigCollection(ctx, db); err != nil {
		return err
	}
	if err := seedHouseRocketsConfig(ctx, db); err != nil {
		return err
	}
	return insertGameConfigLocalizations(ctx, db)
}

func createGameConfigCollection(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"_id", "enabled", "rollout", "version", "updatedAt"},
		"properties": bson.M{
			"_id":     bson.M{"bsonType": "string", "minLength": 1},
			"enabled": bson.M{"bsonType": "bool"},
			"rollout": bson.M{
				"bsonType": "object",
				"required": bson.A{"houseIds"},
				"properties": bson.M{
					"houseIds": bson.M{"bsonType": "array", "items": bson.M{"bsonType": "string", "minLength": 1}},
				},
			},
			"rules":     bson.M{"bsonType": "object"},
			"settings":  bson.M{"bsonType": "object"},
			"version":   bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"updatedAt": bson.M{"bsonType": "date"},
			"updatedBy": bson.M{"bsonType": "string"},
		},
	}}
	err := db.CreateCollection(ctx, gameConfigCollection, options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}
	return nil
}

func seedHouseRocketsConfig(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection(gameConfigCollection).UpdateOne(ctx, bson.M{"_id": "houseRockets"}, bson.M{"$setOnInsert": bson.M{
		"enabled": true,
		"rollout": bson.M{"houseIds": bson.A{}},
		"rules": bson.M{
			"minimumPlayers":          int64(2),
			"maximumPlayers":          int64(8),
			"readyWindowMilliseconds": int64(30_000),
			"countdownMilliseconds":   int64(3_000),
		},
		"settings": bson.M{
			"snapshotRateHz":                int64(20),
			"maximumInputRateHz":            int64(20),
			"maximumSteerMessagesPerSecond": int64(30),
			"controlHeartbeatMilliseconds":  int64(2_000),
			"controlTimeoutMilliseconds":    int64(6_000),
			"reconnectGraceMilliseconds":    int64(10_000),
		},
		"version":   int64(1),
		"updatedAt": time.Now().UTC(),
		"updatedBy": "migration:0046",
	}}, options.Update().SetUpsert(true))
	return err
}

func insertGameConfigLocalizations(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "game.error.unavailable", "This game is not available for your house right now."},
		{"tr", "game.error.unavailable", "Bu oyun şu anda eviniz için kullanılamıyor."},
		{"en", "game.error.invalid_config", "The game configuration is invalid."},
		{"tr", "game.error.invalid_config", "Oyun yapılandırması geçersiz."},
		{"en", "game.error.config_conflict", "The game configuration was changed by someone else. Reload it and try again."},
		{"tr", "game.error.config_conflict", "Oyun yapılandırması başka biri tarafından değiştirildi. Yeniden yükleyip tekrar deneyin."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      message.key,
		}, bson.M{"$setOnInsert": bson.M{
			"value":     message.value,
			"createdOn": now,
			"updatedOn": now,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
