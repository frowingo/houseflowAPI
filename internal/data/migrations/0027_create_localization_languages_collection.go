package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createLocalizationLanguagesCollection struct{}

func (m *createLocalizationLanguagesCollection) Version() string { return "0027" }
func (m *createLocalizationLanguagesCollection) Name() string {
	return "createLocalizationLanguagesCollection"
}

func (m *createLocalizationLanguagesCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"code", "name", "nativeName", "isDefault", "isActive", "image"},
			"properties": bson.M{
				"code":       bson.M{"bsonType": "string"},
				"name":       bson.M{"bsonType": "string"},
				"nativeName": bson.M{"bsonType": "string"},
				"isDefault":  bson.M{"bsonType": "bool"},
				"isActive":   bson.M{"bsonType": "bool"},
				"image":      bson.M{"bsonType": "string"},
				"createdOn":  bson.M{"bsonType": "date"},
				"updatedOn":  bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "localization_languages", options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}

	col := db.Collection("localization_languages")
	if _, err = col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "code", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idxLocalizationLanguagesCodeUnique"),
	}); err != nil {
		return err
	}

	_, err = col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "isActive", Value: 1},
			{Key: "code", Value: 1},
		},
		Options: options.Index().SetName("idxLocalizationLanguagesIsActiveCode"),
	})
	return err
}
