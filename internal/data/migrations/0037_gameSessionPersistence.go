package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type gameSessionPersistence struct{}

func (m *gameSessionPersistence) Version() string { return "0037" }
func (m *gameSessionPersistence) Name() string    { return "gameSessionPersistence" }

func (m *gameSessionPersistence) Up(ctx context.Context, db *mongo.Database) error {
	if err := createGameSessionCollection(ctx, db); err != nil {
		return err
	}
	if err := createOutboxMessageCollection(ctx, db); err != nil {
		return err
	}
	return nil
}

func createGameSessionCollection(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{
			"_id", "houseId", "gameKey", "protocolVersion", "mode", "state",
			"rules", "players", "createdBy", "createdAt", "updatedAt", "version",
		},
		"properties": bson.M{
			"_id":             bson.M{"bsonType": "string"},
			"houseId":         bson.M{"bsonType": "string"},
			"gameKey":         bson.M{"bsonType": "string"},
			"protocolVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"mode":            bson.M{"enum": bson.A{"realtime", "turnBased"}},
			"state": bson.M{"enum": bson.A{
				"lobby", "readyWindow", "countdown", "running", "finished", "cancelled",
			}},
			"rules": bson.M{
				"bsonType": "object",
				"required": bson.A{
					"minimumPlayers", "maximumPlayers", "readyWindowMilliseconds", "countdownMilliseconds",
				},
				"properties": bson.M{
					"minimumPlayers":          bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 2},
					"maximumPlayers":          bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 2},
					"readyWindowMilliseconds": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 0},
					"countdownMilliseconds":   bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 0},
				},
			},
			"players": bson.M{
				"bsonType": "array",
				"items": bson.M{
					"bsonType": "object",
					"required": bson.A{"playerId", "state", "joinedAt"},
					"properties": bson.M{
						"playerId": bson.M{"bsonType": "string"},
						"state": bson.M{"enum": bson.A{
							"waiting", "ready", "playing", "finished", "left",
						}},
						"joinedAt": bson.M{"bsonType": "date"},
						"readyAt":  bson.M{"bsonType": "date"},
						"leftAt":   bson.M{"bsonType": "date"},
					},
				},
			},
			"createdBy":         bson.M{"bsonType": "string"},
			"createdAt":         bson.M{"bsonType": "date"},
			"updatedAt":         bson.M{"bsonType": "date"},
			"readyWindowEndsAt": bson.M{"bsonType": "date"},
			"countdownEndsAt":   bson.M{"bsonType": "date"},
			"startedAt":         bson.M{"bsonType": "date"},
			"endedAt":           bson.M{"bsonType": "date"},
			"endReason":         bson.M{"bsonType": "string"},
			"version":           bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
		},
	}}
	if err := db.CreateCollection(
		ctx,
		"GameSession",
		options.CreateCollection().SetValidator(validator),
	); err != nil && !isCollectionExistsError(err) {
		return err
	}

	_, err := db.Collection("GameSession").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "houseId", Value: 1},
			{Key: "state", Value: 1},
			{Key: "updatedAt", Value: -1},
		},
		Options: options.Index().SetName("idxGameSessionHouseStateUpdatedAt"),
	})
	return err
}

func createOutboxMessageCollection(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{
			"_id", "eventId", "aggregateType", "aggregateId", "aggregateVersion",
			"eventType", "payload", "occurredAt", "createdAt", "attemptCount", "nextAttemptAt",
		},
		"properties": bson.M{
			"_id":              bson.M{"bsonType": "objectId"},
			"eventId":          bson.M{"bsonType": "string"},
			"aggregateType":    bson.M{"bsonType": "string"},
			"aggregateId":      bson.M{"bsonType": "string"},
			"aggregateVersion": bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"eventType":        bson.M{"bsonType": "string"},
			"payload":          bson.M{"bsonType": "binData"},
			"occurredAt":       bson.M{"bsonType": "date"},
			"createdAt":        bson.M{"bsonType": "date"},
			"publishedAt":      bson.M{"bsonType": "date"},
			"attemptCount":     bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 0},
			"nextAttemptAt":    bson.M{"bsonType": "date"},
			"claimedBy":        bson.M{"bsonType": "string"},
			"claimedUntil":     bson.M{"bsonType": "date"},
			"lastError":        bson.M{"bsonType": "string"},
		},
	}}
	if err := db.CreateCollection(
		ctx,
		"OutboxMessage",
		options.CreateCollection().SetValidator(validator),
	); err != nil && !isCollectionExistsError(err) {
		return err
	}

	_, err := db.Collection("OutboxMessage").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "eventId", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uqOutboxMessageEventId"),
		},
		{
			Keys: bson.D{
				{Key: "aggregateType", Value: 1},
				{Key: "aggregateId", Value: 1},
				{Key: "aggregateVersion", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("uqOutboxMessageAggregateVersion"),
		},
		{
			Keys: bson.D{
				{Key: "publishedAt", Value: 1},
				{Key: "nextAttemptAt", Value: 1},
				{Key: "claimedUntil", Value: 1},
				{Key: "createdAt", Value: 1},
			},
			Options: options.Index().SetName("idxOutboxMessagePending"),
		},
	})
	return err
}
