package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// authCodeAttempts expires per-code failure counters once their window ends.
type authCodeAttempts struct{}

func (*authCodeAttempts) Version() string { return "0065" }
func (*authCodeAttempts) Name() string    { return "authCodeAttempts" }

func (*authCodeAttempts) Up(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection("AuthCodeAttempt").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expiresAt", Value: 1}},
		Options: options.Index().SetName("ttlAuthCodeAttemptExpiresAt").SetExpireAfterSeconds(0),
	})
	return err
}
