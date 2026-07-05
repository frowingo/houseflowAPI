package migrations

import (
	"context"
	"houseflowApi/internal/data/entities"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createLocalizationCollection struct{}

func (m *createLocalizationCollection) Version() string { return "0024" }
func (m *createLocalizationCollection) Name() string    { return "createLocalizationCollection" }

func (m *createLocalizationCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"language", "key", "type", "value"},
			"properties": bson.M{
				"language":  bson.M{"bsonType": "string", "enum": []string{string(entities.English), string(entities.Turkish)}},
				"key":       bson.M{"bsonType": "string"},
				"type":      bson.M{"bsonType": "string", "enum": []string{string(entities.Plaintext), string(entities.Message)}},
				"value":     bson.M{"bsonType": "string"},
				"createdOn": bson.M{"bsonType": "date"},
				"updatedOn": bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "localization", options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}

	col := db.Collection("localization")
	_, err = col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "language", Value: 1},
			{Key: "type", Value: 1},
			{Key: "key", Value: 1},
		},
		Options: options.Index().SetUnique(true).SetName("idxLocalizationLanguageTypeKeyUnique"),
	})
	if err != nil {
		return err
	}

	_, err = col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "type", Value: 1},
			{Key: "language", Value: 1},
		},
		Options: options.Index().SetName("idxLocalizationTypeLanguage"),
	})
	return err
}
