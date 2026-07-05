package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type localizationTypeLanguageIndex struct{}

func (m *localizationTypeLanguageIndex) Version() string { return "0026" }
func (m *localizationTypeLanguageIndex) Name() string    { return "localizationTypeLanguageIndex" }

func (m *localizationTypeLanguageIndex) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("localization")

	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "type", Value: 1},
			{Key: "language", Value: 1},
		},
		Options: options.Index().SetName("idxLocalizationTypeLanguage"),
	})
	return err
}
