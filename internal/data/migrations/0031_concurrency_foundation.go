package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type concurrencyFoundation struct{}

func (m *concurrencyFoundation) Version() string { return "0031" }
func (m *concurrencyFoundation) Name() string    { return "concurrencyFoundation" }

func (m *concurrencyFoundation) Up(ctx context.Context, db *mongo.Database) error {
	if _, err := db.Collection("Chore").UpdateMany(ctx,
		bson.M{"version": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"version": int64(0)}}); err != nil {
		return err
	}

	validator := bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": []string{"houseId", "userId", "nextAllowedAt"},
		"properties": bson.M{
			"houseId":       bson.M{"bsonType": "string"},
			"userId":        bson.M{"bsonType": "string"},
			"nextAllowedAt": bson.M{"bsonType": "date"},
		},
	}}
	if err := db.CreateCollection(ctx, "AnnouncementRateLimit", options.CreateCollection().SetValidator(validator)); err != nil && !isCollectionExistsError(err) {
		return err
	}
	guardCollection := db.Collection("AnnouncementRateLimit")
	if _, err := guardCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "nextAllowedAt", Value: 1}},
		Options: options.Index().SetName("ttlAnnouncementRateLimit").SetExpireAfterSeconds(0),
	}); err != nil {
		return err
	}

	cursor, err := db.Collection("Announcement").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.M{
			"_id":             bson.M{"houseId": "$houseId", "userId": "$userId"},
			"latestCreatedOn": bson.M{"$max": "$createdOn"},
		}}},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	var existing []struct {
		ID struct {
			HouseID string `bson:"houseId"`
			UserID  string `bson:"userId"`
		} `bson:"_id"`
		LatestCreatedOn time.Time `bson:"latestCreatedOn"`
	}
	if err := cursor.All(ctx, &existing); err != nil {
		return err
	}
	for _, item := range existing {
		if item.ID.HouseID == "" || item.ID.UserID == "" {
			continue
		}
		if _, err := guardCollection.UpdateOne(ctx,
			bson.M{"_id": item.ID.HouseID + ":" + item.ID.UserID},
			bson.M{"$set": bson.M{
				"houseId": item.ID.HouseID, "userId": item.ID.UserID,
				"nextAllowedAt": item.LatestCreatedOn.Add(24 * time.Hour),
			}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}

	_, err = db.Collection("ImageAsset").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "publicId", Value: 1}},
		Options: options.Index().SetName("uqImageAssetPublicId").SetUnique(true).
			SetPartialFilterExpression(bson.M{"publicId": bson.M{"$gt": ""}}),
	})
	if err != nil {
		return err
	}

	now := time.Now()
	messages := []struct{ language, value string }{
		{language: "en", value: "The record changed while your request was being processed. Please try again."},
		{language: "tr", value: "İşleminiz sırasında kayıt değişti. Lütfen tekrar deneyin."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language, "type": "message", "key": "chore.error.concurrent_update",
		}, bson.M{"$setOnInsert": bson.M{
			"value": message.value, "createdOn": now, "updatedOn": now,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
