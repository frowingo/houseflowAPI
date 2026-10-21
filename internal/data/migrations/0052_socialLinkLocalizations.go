package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// socialLinkLocalizations adds the messages of linking and unlinking sign-in
// providers on an existing account.
type socialLinkLocalizations struct{}

func (m *socialLinkLocalizations) Version() string { return "0052" }
func (m *socialLinkLocalizations) Name() string    { return "socialLinkLocalizations" }

func (m *socialLinkLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "auth.error.provider_already_connected", "Another account of this provider is already connected to your account."},
		{"tr", "auth.error.provider_already_connected", "Hesabınıza bu sağlayıcıdan başka bir hesap zaten bağlı."},
		{"en", "auth.error.provider_not_linked", "This sign-in method is not connected to your account."},
		{"tr", "auth.error.provider_not_linked", "Bu giriş yöntemi hesabınıza bağlı değil."},
		{"en", "auth.error.last_sign_in_method", "This is your only way to sign in. Set a password or connect another provider first."},
		{"tr", "auth.error.last_sign_in_method", "Bu, hesabınıza girmenin tek yolu. Önce bir şifre belirleyin ya da başka bir sağlayıcı bağlayın."},
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
