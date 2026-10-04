package migrations

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const activeGameSessionCollection = "ActiveGameSession"

type gameCatalogActiveSession struct{}

func (m *gameCatalogActiveSession) Version() string { return "0040" }
func (m *gameCatalogActiveSession) Name() string    { return "gameCatalogActiveSession" }

func (m *gameCatalogActiveSession) Up(ctx context.Context, db *mongo.Database) error {
	if err := createActiveGameSessionCollection(ctx, db); err != nil {
		return err
	}
	if err := backfillActiveGameSessions(ctx, db); err != nil {
		return err
	}
	return insertGameCatalogLocalizations(ctx, db)
}

func createActiveGameSessionCollection(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"_id", "houseId", "gameKey", "sessionId", "createdAt"},
		"properties": bson.M{
			"_id":       bson.M{"bsonType": "objectId"},
			"houseId":   bson.M{"bsonType": "string", "minLength": 1},
			"gameKey":   bson.M{"bsonType": "string", "minLength": 1},
			"sessionId": bson.M{"bsonType": "string", "minLength": 1},
			"createdAt": bson.M{"bsonType": "date"},
		},
	}}
	if err := db.CreateCollection(
		ctx,
		activeGameSessionCollection,
		options.CreateCollection().SetValidator(validator),
	); err != nil && !isCollectionExistsError(err) {
		return err
	}

	_, err := db.Collection(activeGameSessionCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "houseId", Value: 1},
				{Key: "gameKey", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("uqActiveGameSessionHouseGame"),
		},
		{
			Keys:    bson.D{{Key: "sessionId", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uqActiveGameSessionSession"),
		},
	})
	return err
}

func backfillActiveGameSessions(ctx context.Context, db *mongo.Database) error {
	cursor, err := db.Collection("GameSession").Find(ctx, bson.M{
		"state": bson.M{"$in": bson.A{"lobby", "readyWindow", "countdown", "running"}},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	type activeSession struct {
		SessionID string    `bson:"_id"`
		HouseID   string    `bson:"houseId"`
		GameKey   string    `bson:"gameKey"`
		CreatedAt time.Time `bson:"createdAt"`
	}
	collection := db.Collection(activeGameSessionCollection)
	for cursor.Next(ctx) {
		var session activeSession
		if err := cursor.Decode(&session); err != nil {
			return err
		}
		filter := bson.M{"houseId": session.HouseID, "gameKey": session.GameKey}
		_, err := collection.UpdateOne(ctx, filter, bson.M{"$setOnInsert": bson.M{
			"_id":       primitive.NewObjectID(),
			"houseId":   session.HouseID,
			"gameKey":   session.GameKey,
			"sessionId": session.SessionID,
			"createdAt": session.CreatedAt,
		}}, options.Update().SetUpsert(true))
		if err != nil {
			return err
		}
		var stored struct {
			SessionID string `bson:"sessionId"`
		}
		if err := collection.FindOne(ctx, filter).Decode(&stored); err != nil {
			return err
		}
		if stored.SessionID != session.SessionID {
			return fmt.Errorf(
				"multiple active game sessions for house %s and game %s: %s, %s",
				session.HouseID,
				session.GameKey,
				stored.SessionID,
				session.SessionID,
			)
		}
	}
	return cursor.Err()
}

func insertGameCatalogLocalizations(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "game.error.definition_not_found", "The requested game is not available."},
		{"tr", "game.error.definition_not_found", "İstenen oyun kullanılamıyor."},
		{"en", "game.error.active_session_not_found", "There is no active session for this game."},
		{"tr", "game.error.active_session_not_found", "Bu oyun için aktif bir oturum bulunmuyor."},
		{"en", "game.error.house_capacity_too_small", "The house capacity is too small for this game."},
		{"tr", "game.error.house_capacity_too_small", "Evin kapasitesi bu oyun için yetersiz."},
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
