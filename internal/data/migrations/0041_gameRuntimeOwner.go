package migrations

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type gameRuntimeOwner struct{}

func (*gameRuntimeOwner) Version() string { return "0041" }
func (*gameRuntimeOwner) Name() string    { return "gameRuntimeOwner" }
func (*gameRuntimeOwner) Up(ctx context.Context, db *mongo.Database) error {
	err := db.CreateCollection(ctx, "GameRuntimeOwner", options.CreateCollection().SetValidator(bson.M{"$jsonSchema": bson.M{
		"bsonType": "object", "required": bson.A{"_id", "generation", "ownerInstanceId", "leaseId", "started"},
		"properties": bson.M{
			"_id":             bson.M{"bsonType": "string", "minLength": 1},
			"generation":      bson.M{"bsonType": "long", "minimum": 1},
			"ownerInstanceId": bson.M{"bsonType": "string", "minLength": 1},
			"leaseId":         bson.M{"bsonType": "string", "minLength": 1},
			"started":         bson.M{"bsonType": "bool"},
		},
	}}))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
