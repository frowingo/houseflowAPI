package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type announcementQueryIndexes struct{}

func (m *announcementQueryIndexes) Version() string { return "0028" }
func (m *announcementQueryIndexes) Name() string    { return "announcementQueryIndexes" }

func (m *announcementQueryIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Announcement")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "houseId", Value: 1},
				{Key: "userId", Value: 1},
				{Key: "createdOn", Value: -1},
			},
			Options: options.Index().SetName("idxAnnouncementHouseUserCreatedOn"),
		},
		{
			Keys: bson.D{
				{Key: "houseId", Value: 1},
				{Key: "displayUntil", Value: 1},
			},
			Options: options.Index().SetName("idxAnnouncementHouseDisplayUntil"),
		},
	})
	return err
}
