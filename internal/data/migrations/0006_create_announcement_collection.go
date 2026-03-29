package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type createAnnouncementCollection struct{}

func (m *createAnnouncementCollection) Version() string { return "0006" }
func (m *createAnnouncementCollection) Name() string    { return "createAnnouncementCollection" }

func (m *createAnnouncementCollection) Up(ctx context.Context, db *mongo.Database) error {
	validator := bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"properties": bson.M{
				"title":        bson.M{"bsonType": "string"},
				"description":  bson.M{"bsonType": "string"},
				"userId":       bson.M{"bsonType": "string"},
				"houseId":      bson.M{"bsonType": "string"},
				"createdOn":    bson.M{"bsonType": "date"},
				"displayUntil": bson.M{"bsonType": "date"},
			},
		},
	}

	err := db.CreateCollection(ctx, "Announcement", options.CreateCollection().SetValidator(validator))
	if isCollectionExistsError(err) {
		return nil
	}
	return err
}
