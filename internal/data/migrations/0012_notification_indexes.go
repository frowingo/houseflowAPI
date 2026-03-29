package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type notificationIndexes struct{}

func (m *notificationIndexes) Version() string { return "0012" }
func (m *notificationIndexes) Name() string    { return "notificationIndexes" }

func (m *notificationIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Notification")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "houseId", Value: 1}},
			Options: options.Index().SetName("idxNotificationHouseId"),
		},
	})
	return err
}
