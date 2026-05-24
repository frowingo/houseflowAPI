package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createImageAssetCollection struct{}

func (m *createImageAssetCollection) Version() string { return "0018" }
func (m *createImageAssetCollection) Name() string    { return "createImageAssetCollection" }

func (m *createImageAssetCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"properties": bson.M{
				"fileName":  bson.M{"bsonType": "string"},
				"fileUrl":   bson.M{"bsonType": "string"},
				"publicId":  bson.M{"bsonType": "string"},
				"createdOn": bson.M{"bsonType": "date"},
				"updatedOn": bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "ImageAsset", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
