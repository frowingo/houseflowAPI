package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// codeEmailLocalizations adds the texts of the password reset and email
// verification code emails (services.LocalizeCodeEmail). The English copy
// matches the fallback the email service carries; values already stored are
// kept, so an edit made through the localization API survives.
type codeEmailLocalizations struct{}

func (m *codeEmailLocalizations) Version() string { return "0072" }
func (m *codeEmailLocalizations) Name() string    { return "codeEmailLocalizations" }

func (m *codeEmailLocalizations) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC()
	messages := []struct {
		language string
		key      string
		value    string
	}{
		{"en", "email.reset_code.subject", "Password Reset Code"},
		{"tr", "email.reset_code.subject", "Şifre Sıfırlama Kodu"},
		{"en", "email.reset_code.intro", "We received a request to reset your password. Use the code below to continue."},
		{"tr", "email.reset_code.intro", "Şifrenizi sıfırlamak için bir istek aldık. Devam etmek için aşağıdaki kodu kullanın."},
		{"en", "email.reset_code.ignore", "If you did not request this password reset, you can safely ignore this email."},
		{"tr", "email.reset_code.ignore", "Bu şifre sıfırlama isteğini siz yapmadıysanız bu e-postayı görmezden gelebilirsiniz."},
		{"en", "email.verification_code.subject", "Email Verification Code"},
		{"tr", "email.verification_code.subject", "E-posta Doğrulama Kodu"},
		{"en", "email.verification_code.intro", "Use the code below to verify your email address and complete your FausCrew registration."},
		{"tr", "email.verification_code.intro", "E-posta adresinizi doğrulamak ve FausCrew kaydınızı tamamlamak için aşağıdaki kodu kullanın."},
		{"en", "email.verification_code.ignore", "If you did not create a FausCrew account, you can safely ignore this email."},
		{"tr", "email.verification_code.ignore", "Bir FausCrew hesabı oluşturmadıysanız bu e-postayı görmezden gelebilirsiniz."},
		// %s marks an argument (the localization cache fills it with
		// fmt.Sprintf); in email.code.expires it is the duration, set in bold.
		{"en", "email.code.expires", "This code expires in %s."},
		{"tr", "email.code.expires", "Bu kod %s sonra geçersiz olacak."},
		{"en", "email.code.minute", "%s minute"},
		{"tr", "email.code.minute", "%s dakika"},
		{"en", "email.code.minutes", "%s minutes"},
		{"tr", "email.code.minutes", "%s dakika"},
		{"en", "email.code.security_note", "Never share this code with anyone. The FausCrew team will never ask for it."},
		{"tr", "email.code.security_note", "Bu kodu kimseyle paylaşmayın. FausCrew ekibi bu kodu asla sizden istemez."},
		{"en", "email.footer.automated", "This is an automated message, please do not reply."},
		{"tr", "email.footer.automated", "Bu otomatik bir e-postadır, lütfen yanıtlamayın."},
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
