package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type userAddFailedLoginAttempts struct{}

func (m *userAddFailedLoginAttempts) Version() string { return "0017" }
func (m *userAddFailedLoginAttempts) Name() string    { return "userAddFailedLoginAttempts" }

func (m *userAddFailedLoginAttempts) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("User")

	// Add failedLoginAttempts field to all documents that don't have it, with default value 0
	_, err := col.UpdateMany(ctx, bson.M{}, bson.M{
		"$set": bson.M{
			"failedLoginAttempts": 0,
		},
	})

	return err
}
