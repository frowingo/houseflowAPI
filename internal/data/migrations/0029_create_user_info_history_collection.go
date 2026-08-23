package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createUserInfoHistoryCollection struct{}

func (m *createUserInfoHistoryCollection) Version() string { return "0029" }
func (m *createUserInfoHistoryCollection) Name() string {
	return "createUserInfoHistoryCollection"
}

func (m *createUserInfoHistoryCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"userId", "columnName", "value", "updateOn"},
			"properties": bson.M{
				"userId":     bson.M{"bsonType": "string"},
				"columnName": bson.M{"bsonType": "string"},
				"value":      bson.M{"bsonType": []string{"string", "date", "int", "long"}},
				"updateOn":   bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "UserInfoHistory", options.CreateCollection().SetValidator(validator))
	if err != nil && !isCollectionExistsError(err) {
		return err
	}

	_, err = db.Collection("UserInfoHistory").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "userId", Value: 1},
			{Key: "updateOn", Value: -1},
		},
		Options: options.Index().SetName("idxUserInfoHistoryUserIdUpdateOn"),
	})
	return err
}
