package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createHouseCollection struct{}

func (m *createHouseCollection) Version() string { return "0002" }
func (m *createHouseCollection) Name() string    { return "create_house_collection" }

func (m *createHouseCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"ownerId", "name"},
			"properties": bson.M{
				"ownerId":        bson.M{"bsonType": "string"},
				"inviteCode":     bson.M{"bsonType": "string"},
				"name":           bson.M{"bsonType": "string"},
				"type":           bson.M{"bsonType": "int"},
				"memberIds":      bson.M{"bsonType": "array"},
				"maxMemberCount": bson.M{"bsonType": "int"},
				"createdOn":      bson.M{"bsonType": "date"},
				"updatedOn":      bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "House", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
