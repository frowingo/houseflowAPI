package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreStatusHistoryIndexes struct{}

func (m *choreStatusHistoryIndexes) Version() string { return "0010" }
func (m *choreStatusHistoryIndexes) Name() string    { return "choreStatusHistoryIndexes" }

func (m *choreStatusHistoryIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("ChoreStatusHistory")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "choreId", Value: 1}},
			Options: options.Index().SetName("idxChoreStatusHistoryChoreId"),
		},
	})
	return err
}
