package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type houseIndexes struct{}

func (m *houseIndexes) Version() string { return "0008" }
func (m *houseIndexes) Name() string    { return "houseIndexes" }

func (m *houseIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("House")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "inviteCode", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idxHouseInviteCodeUnique"),
		},
		{
			Keys:    bson.D{{Key: "ownerId", Value: 1}},
			Options: options.Index().SetName("idxHouseOwnerId"),
		},
	})
	return err
}
