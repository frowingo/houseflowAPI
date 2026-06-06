package migrations

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type imageAssetNewFields struct{}

func (m *imageAssetNewFields) Version() string { return "0020" }
func (m *imageAssetNewFields) Name() string    { return "imageAssetNewFields" }

func (m *imageAssetNewFields) Up(ctx context.Context, db *mongo.Database) error {
	col := db.Collection("ImageAsset")

	// category ve publicId alanı olmayan dokümanlara boş string ata
	_, err := col.UpdateMany(
		ctx,
		bson.M{"category": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"category": ""}},
	)
	if err != nil {
		return err
	}

	_, err = col.UpdateMany(
		ctx,
		bson.M{"publicId": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"publicId": ""}},
	)
	if err != nil {
		return err
	}

	// isActive alanı olmayan mevcut dokümanlara varsayılan true ata
	_, err = col.UpdateMany(
		ctx,
		bson.M{"isActive": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"isActive": true}},
	)
	if err != nil {
		return err
	}

	// isActive üzerinde index oluştur (getImages filtresi için)
	_, err = col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "isActive", Value: 1}},
		Options: options.Index().SetName("idxImageAssetIsActive"),
	})
	return err
}
