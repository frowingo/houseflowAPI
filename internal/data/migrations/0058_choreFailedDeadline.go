package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreFailedDeadline struct{}

func (m *choreFailedDeadline) Version() string { return "0058" }
func (m *choreFailedDeadline) Name() string    { return "choreFailedDeadline" }

func (m *choreFailedDeadline) Up(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection("Chore").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "dueDate", Value: 1}},
		Options: options.Index().SetName("idxChoreDueDate"),
	})
	return err
}
