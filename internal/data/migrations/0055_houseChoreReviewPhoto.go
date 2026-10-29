package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// houseChoreReviewPhoto prepares the house setting and the per-review-round
// private object-key map used by the chore review photo flow.
type houseChoreReviewPhoto struct{}

func (m *houseChoreReviewPhoto) Version() string { return "0055" }
func (m *houseChoreReviewPhoto) Name() string    { return "houseChoreReviewPhoto" }

func (m *houseChoreReviewPhoto) Up(ctx context.Context, db *mongo.Database) error {
	if _, err := db.Collection("House").UpdateMany(ctx,
		bson.M{"isChoreReviewPhotoRequired": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"isChoreReviewPhotoRequired": false}},
	); err != nil {
		return err
	}
	if _, err := db.Collection("Chore").UpdateMany(ctx,
		bson.M{"reviewPhotoKeys": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"reviewPhotoKeys": bson.M{}}},
	); err != nil {
		return err
	}

	now := time.Now().UTC()
	messages := []struct {
		language string
		value    string
	}{
		{"en", "The chore review photo requirement can be changed at most twice within 24 hours."},
		{"tr", "Görev inceleme fotoğrafı zorunluluğu 24 saat içinde en fazla iki kez değiştirilebilir."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      "house.error.review_photo_setting_update_limit",
		}, bson.M{"$setOnInsert": bson.M{
			"value":     message.value,
			"createdOn": now,
			"updatedOn": now,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
