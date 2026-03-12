package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createChoreCollection struct{}

func (m *createChoreCollection) Version() string { return "0003" }
func (m *createChoreCollection) Name() string    { return "create_chore_collection" }

func (m *createChoreCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"title", "house_id"},
			"properties": bson.M{
				"title":              bson.M{"bsonType": "string"},
				"description":        bson.M{"bsonType": "string"},
				"is_completed":       bson.M{"bsonType": "bool"},
				"assigned_to":        bson.M{"bsonType": "string"},
				"due_date":           bson.M{"bsonType": "date"},
				"created_on":         bson.M{"bsonType": "date"},
				"house_id":           bson.M{"bsonType": "string"},
				"house_owner_id":     bson.M{"bsonType": "string"},
				"level":              bson.M{"bsonType": "int"},
				"status":             bson.M{"bsonType": "int"},
				"is_recurring":       bson.M{"bsonType": "bool"},
				"recurring_interval": bson.M{"bsonType": "int"},
			},
		},
	}

	err := db.CreateCollection(ctx, "Chore", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
