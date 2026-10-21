package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dataDeletionRequestCollection = "DataDeletionRequest"

// dataDeletionRequests stores the confirmation codes returned to a provider's
// data deletion callback (Facebook). Records expire through a TTL index.
type dataDeletionRequests struct{}

func (m *dataDeletionRequests) Version() string { return "0051" }
func (m *dataDeletionRequests) Name() string    { return "dataDeletionRequests" }

func (m *dataDeletionRequests) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": bson.A{"_id", "provider", "status", "requestedOn", "expiresAt"},
		"properties": bson.M{
			"_id":            bson.M{"bsonType": "string", "minLength": 1},
			"provider":       bson.M{"bsonType": bson.A{"int", "long"}, "minimum": 1},
			"status":         bson.M{"bsonType": "string"},
			"accountDeleted": bson.M{"bsonType": "bool"},
			"requestedOn":    bson.M{"bsonType": "date"},
			"expiresAt":      bson.M{"bsonType": "date"},
		},
	}}
	err := db.CreateCollection(ctx, dataDeletionRequestCollection, options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}
	if _, err := db.Collection(dataDeletionRequestCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetName("ttlDataDeletionRequestExpiresAt").SetExpireAfterSeconds(0),
	}); err != nil {
		return err
	}

	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "auth.error.invalid_signed_request", "The request signature is not valid."},
		{"tr", "auth.error.invalid_signed_request", "İsteğin imzası geçersiz."},
		{"en", "auth.error.data_deletion_not_found", "No data deletion request was found for this code."},
		{"tr", "auth.error.data_deletion_not_found", "Bu kodla bir veri silme isteği bulunamadı."},
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
