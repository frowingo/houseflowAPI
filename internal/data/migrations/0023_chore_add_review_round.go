package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type choreAddReviewRound struct{}

func (m *choreAddReviewRound) Version() string { return "0023" }
func (m *choreAddReviewRound) Name() string    { return "choreAddReviewRound" }

func (m *choreAddReviewRound) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("Chore")

	_, err := col.UpdateMany(
		ctx,
		bson.M{"reviewRound": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"reviewRound": 0}},
	)
	return err
}
