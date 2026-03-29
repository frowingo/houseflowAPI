package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreIndexes struct{}

func (m *choreIndexes) Version() string { return "0009" }
func (m *choreIndexes) Name() string    { return "choreIndexes" }

func (m *choreIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Chore")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "houseId", Value: 1}},
			Options: options.Index().SetName("idxChoreHouseId"),
		},
		{
			Keys:    bson.D{{Key: "assignedTo", Value: 1}},
			Options: options.Index().SetName("idxChoreAssignedTo"),
		},
	})
	return err
}
