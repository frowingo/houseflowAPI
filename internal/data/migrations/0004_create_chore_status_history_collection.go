package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createChoreStatusHistoryCollection struct{}

func (m *createChoreStatusHistoryCollection) Version() string { return "0004" }
func (m *createChoreStatusHistoryCollection) Name() string {
	return "createChoreStatusHistoryCollection"
}

func (m *createChoreStatusHistoryCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"choreId"},
			"properties": bson.M{
				"choreId":  bson.M{"bsonType": "string"},
				"status":   bson.M{"bsonType": "int"},
				"dateTime": bson.M{"bsonType": "date"},
				"updater":  bson.M{"bsonType": "string"},
			},
		},
	}

	err := db.CreateCollection(ctx, "ChoreStatusHistory", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
