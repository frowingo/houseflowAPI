package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type imageAssetIndexes struct{}

func (m *imageAssetIndexes) Version() string { return "0019" }
func (m *imageAssetIndexes) Name() string    { return "imageAssetIndexes" }

func (m *imageAssetIndexes) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("ImageAsset")
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "fileName", Value: 1}},
			Options: options.Index().SetName("idxImageAssetFileName"),
		},
		{
			Keys:    bson.D{{Key: "fileUrl", Value: 1}},
			Options: options.Index().SetName("idxImageAssetFileUrl"),
		},
	})
	return err
}
