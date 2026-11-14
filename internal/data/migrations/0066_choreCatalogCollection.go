package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// choreCatalogCollection creates the chore template catalog. userId is a plain
// string because system entries use "SYSTEM" instead of a user ObjectID.
type choreCatalogCollection struct{}

func (*choreCatalogCollection) Version() string { return "0066" }
func (*choreCatalogCollection) Name() string    { return "choreCatalogCollection" }

func (*choreCatalogCollection) Up(ctx context.Context, db *mongo.Database) error {
	integer := bson.M{"bsonType": bson.A{"int", "long"}}
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{
				"title", "level", "category", "recurringInterval", "createdOn", "updatedOn", "isDeleted", "userId",
			},
			"properties": bson.M{
				"title":             bson.M{"bsonType": "string"},
				"description":       bson.M{"bsonType": "string"},
				"recurringInterval": integer,
				"level":             integer,
				"category":          integer,
				"createdOn":         bson.M{"bsonType": "date"},
				"updatedOn":         bson.M{"bsonType": "date"},
				"isDeleted":         bson.M{"bsonType": "bool"},
				"userId":            bson.M{"bsonType": "string"},
			},
		},
	}
	err := db.CreateCollection(ctx, "ChoreCatalog", options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}

	_, err = db.Collection("ChoreCatalog").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "userId", Value: 1}, {Key: "isDeleted", Value: 1}},
		Options: options.Index().SetName("idxChoreCatalogUserIdIsDeleted"),
	})
	return err
}
