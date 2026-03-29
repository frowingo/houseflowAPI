package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
)

type removePhoneUniqueIndex struct{}

func (m *removePhoneUniqueIndex) Version() string { return "0015" }
func (m *removePhoneUniqueIndex) Name() string    { return "removePhoneUniqueIndex" }

func (m *removePhoneUniqueIndex) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("User")
	_, err := col.Indexes().DropOne(ctx, "idxUserPhoneUnique")
	return err
}
