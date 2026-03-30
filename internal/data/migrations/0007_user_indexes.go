package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type userIndexes struct{}

func (m *userIndexes) Version() string { return "0007" }
func (m *userIndexes) Name() string    { return "userIndexes" }

func (m *userIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("User")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idxUserEmailUnique"),
		},
		{
			Keys:    bson.D{{Key: "phoneNumber", Value: 1}},
			Options: options.Index().SetUnique(true).SetSparse(true).SetName("idxUserPhoneUnique"),
		},
	})
	return err
}
