package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// houseChoreReviewPhoto prepares the house setting and the per-review-round
// private object-key map used by the chore review photo flow.
type houseChoreReviewPhoto struct{}

func (m *houseChoreReviewPhoto) Version() string { return "0055" }
func (m *houseChoreReviewPhoto) Name() string    { return "houseChoreReviewPhoto" }

func (m *houseChoreReviewPhoto) Up(ctx context.Context, db *mongo.Database) error {
	if _, err := db.Collection("House").UpdateMany(ctx,
		bson.M{"isChoreReviewPhotoRequired": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"isChoreReviewPhotoRequired": false}},
	); err != nil {
		return err
	}
	if _, err := db.Collection("Chore").UpdateMany(ctx,
		bson.M{"reviewPhotoKeys": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"reviewPhotoKeys": bson.M{}}},
	); err != nil {
		return err
	}

	houseValidator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"ownerId", "name", "isChoreReviewPhotoRequired"},
		"properties": bson.M{
			"ownerId":                    bson.M{"bsonType": "string"},
			"inviteCode":                 bson.M{"bsonType": "string"},
			"name":                       bson.M{"bsonType": "string"},
			"type":                       bson.M{"bsonType": "int"},
			"memberIds":                  bson.M{"bsonType": "array"},
			"maxMemberCount":             bson.M{"bsonType": "int"},
			"profileImage":               bson.M{"bsonType": "string"},
			"isChoreReviewPhotoRequired": bson.M{"bsonType": "bool"},
			"createdOn":                  bson.M{"bsonType": "date"},
			"updatedOn":                  bson.M{"bsonType": "date"},
		},
	}}
	if err := updateCollectionValidator(ctx, db, "House", houseValidator); err != nil {
		return err
	}

	choreValidator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"title", "houseId"},
		"properties": bson.M{
			"title":             bson.M{"bsonType": "string"},
			"description":       bson.M{"bsonType": "string"},
			"isCompleted":       bson.M{"bsonType": "bool"},
			"assignedTo":        bson.M{"bsonType": "string"},
			"dueDate":           bson.M{"bsonType": "date"},
			"createdOn":         bson.M{"bsonType": "date"},
			"completedAt":       bson.M{"bsonType": "date"},
			"completedBy":       bson.M{"bsonType": "string"},
			"houseId":           bson.M{"bsonType": "string"},
			"houseOwnerId":      bson.M{"bsonType": "string"},
			"level":             bson.M{"bsonType": "int"},
			"status":            bson.M{"bsonType": "int"},
			"reviewRound":       bson.M{"bsonType": "int"},
			"reviewPhotoKeys":   bson.M{"bsonType": "object"},
			"isRecurring":       bson.M{"bsonType": "bool"},
			"recurringInterval": bson.M{"bsonType": "int"},
			"version":           bson.M{"bsonType": "long"},
		},
	}}
	if err := updateCollectionValidator(ctx, db, "Chore", choreValidator); err != nil {
		return err
	}

	houseInfoHistoryValidator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"houseId", "columnName", "value", "updatedBy", "updateOn"},
		"properties": bson.M{
			"houseId":    bson.M{"bsonType": "string"},
			"columnName": bson.M{"bsonType": "string"},
			"value":      bson.M{"bsonType": bson.A{"string", "int", "long", "bool"}},
			"updatedBy":  bson.M{"bsonType": "string"},
			"updateOn":   bson.M{"bsonType": "date"},
		},
	}}
	if err := updateCollectionValidator(ctx, db, "HouseInfoHistory", houseInfoHistoryValidator); err != nil {
		return err
	}

	now := time.Now().UTC()
	messages := []struct {
		language string
		value    string
	}{
		{"en", "The chore review photo requirement can be changed at most twice within 24 hours."},
		{"tr", "Görev inceleme fotoğrafı zorunluluğu 24 saat içinde en fazla iki kez değiştirilebilir."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      "house.error.review_photo_setting_update_limit",
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

func updateCollectionValidator(ctx context.Context, db *mongo.Database, collection string, validator bson.M) error {
	return db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: collection},
		{Key: "validator", Value: validator},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	}).Err()
}
