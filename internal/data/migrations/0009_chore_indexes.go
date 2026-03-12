package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreIndexes struct{}

func (m *choreIndexes) Version() string { return "0009" }
func (m *choreIndexes) Name() string    { return "chore_indexes" }

func (m *choreIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Chore")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "house_id", Value: 1}},
			Options: options.Index().SetName("idx_chore_houseid"),
		},
		{
			Keys:    bson.D{{Key: "assigned_to", Value: 1}},
			Options: options.Index().SetName("idx_chore_assignedto"),
		},
	})
	return err
}
