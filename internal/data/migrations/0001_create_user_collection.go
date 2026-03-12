package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createUserCollection struct{}

func (m *createUserCollection) Version() string { return "0001" }
func (m *createUserCollection) Name() string    { return "create_user_collection" }

func (m *createUserCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"firstName", "lastName", "email", "password"},
			"properties": bson.M{
				"firstName":   bson.M{"bsonType": "string"},
				"lastName":    bson.M{"bsonType": "string"},
				"phoneNumber": bson.M{"bsonType": "string"},
				"email":       bson.M{"bsonType": "string"},
				"password":    bson.M{"bsonType": "string"},
				"age":         bson.M{"bsonType": "int"},
				"image_url":   bson.M{"bsonType": "string"},
				"house_ids":   bson.M{"bsonType": "array"},
				"is_active":   bson.M{"bsonType": "bool"},
				"created_on":  bson.M{"bsonType": "date"},
				"updated_on":  bson.M{"bsonType": "date"},
				"last_login":  bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "User", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
