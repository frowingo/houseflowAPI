package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// authCodeAttemptLocalizations adds the message for an emailed code refused
// because its attempts ran out.
type authCodeAttemptLocalizations struct{}

func (*authCodeAttemptLocalizations) Version() string { return "0068" }
func (*authCodeAttemptLocalizations) Name() string    { return "authCodeAttemptLocalizations" }

func (*authCodeAttemptLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "auth.error.too_many_code_attempts", "Too many attempts. Please wait a few minutes and request a new code."},
		{"tr", "auth.error.too_many_code_attempts", "Çok fazla deneme yapıldı. Lütfen birkaç dakika bekleyip yeni bir kod isteyin."},
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
