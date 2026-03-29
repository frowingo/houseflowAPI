package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createUserCollection struct{}

func (m *createUserCollection) Version() string { return "0001" }
func (m *createUserCollection) Name() string    { return "createUserCollection" }

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
				"imageUrl":    bson.M{"bsonType": "string"},
				"houseIds":    bson.M{"bsonType": "array"},
				"isActive":    bson.M{"bsonType": "bool"},
				"createdOn":   bson.M{"bsonType": "date"},
				"updatedOn":   bson.M{"bsonType": "date"},
				"lastLogin":   bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "User", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
