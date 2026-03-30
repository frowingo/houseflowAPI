package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createNotificationCollection struct{}

func (m *createNotificationCollection) Version() string { return "0005" }
func (m *createNotificationCollection) Name() string    { return "createNotificationCollection" }

func (m *createNotificationCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"properties": bson.M{
				"title":        bson.M{"bsonType": "string"},
				"message":      bson.M{"bsonType": "string"},
				"createdAt":    bson.M{"bsonType": "date"},
				"houseId":      bson.M{"bsonType": "string"},
				"houseOwnerId": bson.M{"bsonType": "string"},
			},
		},
	}

	err := db.CreateCollection(ctx, "Notification", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
