package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// socialAuthLocalizations adds the messages of social sign-in (Google, Apple,
// Facebook, Snapchat). Existing values are never overwritten.
type socialAuthLocalizations struct{}

func (m *socialAuthLocalizations) Version() string { return "0050" }
func (m *socialAuthLocalizations) Name() string    { return "socialAuthLocalizations" }

func (m *socialAuthLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "auth.error.use_social_login", "This account has no password. Sign in with the provider you signed up with, or set a password with \"Forgot password\"."},
		{"tr", "auth.error.use_social_login", "Bu hesabın şifresi yok. Kayıt olduğunuz yöntemle giriş yapın ya da \"Şifremi unuttum\" ile şifre belirleyin."},
		{"en", "auth.error.provider_not_supported", "This sign-in method is not available."},
		{"tr", "auth.error.provider_not_supported", "Bu giriş yöntemi kullanılamıyor."},
		{"en", "auth.error.invalid_social_token", "Sign-in could not be verified. Please try again."},
		{"tr", "auth.error.invalid_social_token", "Giriş doğrulanamadı. Lütfen tekrar deneyin."},
		{"en", "auth.error.email_registered_with_other_method", "This email is already registered with another sign-in method. Sign in with that method first."},
		{"tr", "auth.error.email_registered_with_other_method", "Bu email başka bir giriş yöntemiyle kayıtlı. Önce o yöntemle giriş yapın."},
		{"en", "auth.error.social_signup_expired", "Your sign-up session has expired. Please start again."},
		{"tr", "auth.error.social_signup_expired", "Kayıt oturumunuzun süresi doldu. Lütfen baştan başlayın."},
		{"en", "auth.error.social_email_required", "An email address is required to sign up."},
		{"tr", "auth.error.social_email_required", "Kayıt olmak için bir email adresi gerekli."},
		{"en", "auth.error.provider_unavailable", "The sign-in service is temporarily unavailable. Please try again."},
		{"tr", "auth.error.provider_unavailable", "Giriş servisine şu an ulaşılamıyor. Lütfen tekrar deneyin."},
		{"en", "auth.error.provider_already_linked", "This account is already linked to another user."},
		{"tr", "auth.error.provider_already_linked", "Bu hesap başka bir kullanıcıya bağlı."},
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
