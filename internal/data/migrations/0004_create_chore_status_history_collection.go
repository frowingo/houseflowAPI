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
	return "create_chore_status_history_collection"
}

func (m *createChoreStatusHistoryCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"chore_id"},
			"properties": bson.M{
				"chore_id":  bson.M{"bsonType": "string"},
				"status":    bson.M{"bsonType": "int"},
				"date_time": bson.M{"bsonType": "date"},
				"updater":   bson.M{"bsonType": "string"},
			},
		},
	}

	err := db.CreateCollection(ctx, "ChoreStatusHistory", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
