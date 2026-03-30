package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const announcementTTLDays = 0 // expireAfterSeconds uses displayUntil directly as the expiry time

type announcementIndexes struct{}

func (m *announcementIndexes) Version() string { return "0011" }
func (m *announcementIndexes) Name() string    { return "announcementIndexes" }

func (m *announcementIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Announcement")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "houseId", Value: 1}},
			Options: options.Index().SetName("idxAnnouncementHouseId"),
		},
		{
			// TTL index: MongoDB will automatically delete documents when
			// displayUntil is in the past (expireAfterSeconds = 0).
			Keys:    bson.D{{Key: "displayUntil", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(int32(time.Duration(announcementTTLDays) * 24 * time.Hour / time.Second)).SetName("idxAnnouncementDisplayUntilTTL"),
		},
	})
	return err
}
