package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createChoreCollection struct{}

func (m *createChoreCollection) Version() string { return "0003" }
func (m *createChoreCollection) Name() string    { return "createChoreCollection" }

func (m *createChoreCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"title", "houseId"},
			"properties": bson.M{
				"title":             bson.M{"bsonType": "string"},
				"description":       bson.M{"bsonType": "string"},
				"isCompleted":       bson.M{"bsonType": "bool"},
				"assignedTo":        bson.M{"bsonType": "string"},
				"dueDate":           bson.M{"bsonType": "date"},
				"createdOn":         bson.M{"bsonType": "date"},
				"houseId":           bson.M{"bsonType": "string"},
				"houseOwnerId":      bson.M{"bsonType": "string"},
				"level":             bson.M{"bsonType": "int"},
				"status":            bson.M{"bsonType": "int"},
				"isRecurring":       bson.M{"bsonType": "bool"},
				"recurringInterval": bson.M{"bsonType": "int"},
			},
		},
	}

	err := db.CreateCollection(ctx, "Chore", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
