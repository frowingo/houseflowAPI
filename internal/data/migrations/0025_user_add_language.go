package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type userAddLanguage struct{}

func (m *userAddLanguage) Version() string { return "0025" }
func (m *userAddLanguage) Name() string    { return "userAddLanguage" }

func (m *userAddLanguage) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("User")

	_, err := col.UpdateMany(
		ctx,
		bson.M{"$or": bson.A{
			bson.M{"language": bson.M{"$exists": false}},
			bson.M{"language": ""},
		}},
		bson.M{"$set": bson.M{"language": "en"}},
	)
	return err
}
