package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type choreReviewPhotoUpload struct{}

func (m *choreReviewPhotoUpload) Version() string { return "0056" }
func (m *choreReviewPhotoUpload) Name() string    { return "choreReviewPhotoUpload" }

func (m *choreReviewPhotoUpload) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		key      string
		language string
		value    string
	}{
		{"chore.error.review_photo_required", "en", "A photo is required to submit this chore for review."},
		{"chore.error.review_photo_required", "tr", "Bu görevi incelemeye göndermek için fotoğraf gereklidir."},
		{"chore.error.review_photo_single_transition_required", "en", "A photo upload must contain exactly one chore moving to review."},
		{"chore.error.review_photo_single_transition_required", "tr", "Fotoğraf yükleme isteği, incelemeye geçen yalnızca bir görev içermelidir."},
		{"chore.error.review_photo_invalid", "en", "The review photo is empty or invalid."},
		{"chore.error.review_photo_invalid", "tr", "İnceleme fotoğrafı boş veya geçersiz."},
		{"chore.error.review_photo_too_large", "en", "The review photo cannot be larger than 3 MB."},
		{"chore.error.review_photo_too_large", "tr", "İnceleme fotoğrafı 3 MB'den büyük olamaz."},
		{"chore.error.review_photo_type_unsupported", "en", "The review photo must be a JPEG, PNG, or WebP image."},
		{"chore.error.review_photo_type_unsupported", "tr", "İnceleme fotoğrafı JPEG, PNG veya WebP formatında olmalıdır."},
		{"chore.error.review_photo_storage_unavailable", "en", "Review photo storage is temporarily unavailable."},
		{"chore.error.review_photo_storage_unavailable", "tr", "İnceleme fotoğrafı depolama hizmeti geçici olarak kullanılamıyor."},
		{"chore.error.review_photo_upload_failed", "en", "The review photo could not be uploaded. Please try again."},
		{"chore.error.review_photo_upload_failed", "tr", "İnceleme fotoğrafı yüklenemedi. Lütfen tekrar deneyin."},
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
