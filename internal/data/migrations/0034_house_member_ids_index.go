package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type houseMemberIDsIndex struct{}

func (m *houseMemberIDsIndex) Version() string { return "0034" }
func (m *houseMemberIDsIndex) Name() string    { return "houseMemberIDsIndex" }

func (m *houseMemberIDsIndex) Up(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection("House").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "memberIds", Value: 1}},
		Options: options.Index().SetName("idxHouseMemberIds"),
	})
	return err
}
