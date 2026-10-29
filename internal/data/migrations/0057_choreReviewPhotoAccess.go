package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreReviewPhotoAccess struct{}

func (m *choreReviewPhotoAccess) Version() string { return "0057" }
func (m *choreReviewPhotoAccess) Name() string    { return "choreReviewPhotoAccess" }

func (m *choreReviewPhotoAccess) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		key      string
		language string
		value    string
	}{
		{"chore.error.invalid_review_round", "en", "The review round is invalid."},
		{"chore.error.invalid_review_round", "tr", "İnceleme turu geçersiz."},
		{"chore.error.review_photo_not_found", "en", "No review photo exists for this round."},
		{"chore.error.review_photo_not_found", "tr", "Bu inceleme turu için fotoğraf bulunamadı."},
		{"chore.error.review_photo_url_failed", "en", "The review photo cannot be opened right now. Please try again."},
		{"chore.error.review_photo_url_failed", "tr", "İnceleme fotoğrafı şu anda açılamıyor. Lütfen tekrar deneyin."},
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      message.key,
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
