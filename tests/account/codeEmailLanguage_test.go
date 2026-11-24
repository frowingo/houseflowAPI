package account

import (
	"testing"

	authCommands "houseflowApi/internal/application/auth/commands"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
)

func TestCodeEmailsUseTheAccountLanguage(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "language@example.com"
	if _, err := cqrs.Send[string](fixture.Ctx, fixture.Sender, authCommands.SignUpCommand{
		Email: email, Password: "initial-password", Firstname: "Dil", Lastname: "Tercihi", Language: "tr-TR",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ForgotPasswordCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	if fixture.EmailSender.ResetLanguage != "tr" {
		t.Fatalf("reset code mailed in %q, want tr", fixture.EmailSender.ResetLanguage)
	}
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.SendEmailVerificationCodeCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	if fixture.EmailSender.VerificationLanguage != "tr" {
		t.Fatalf("verification code mailed in %q, want tr", fixture.EmailSender.VerificationLanguage)
	}

	// Accounts stored without a language get English.
	if _, err := fixture.DB.Collection("User").UpdateOne(fixture.Ctx, bson.M{"email": email},
		bson.M{"$set": bson.M{"language": ""}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ForgotPasswordCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	if fixture.EmailSender.ResetLanguage != "en" {
		t.Fatalf("reset code for an account without a language mailed in %q, want en", fixture.EmailSender.ResetLanguage)
	}
}

func TestSocialSignUpCodeUsesTheRequestLanguage(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)

	if _, err := fixture.sendCode.Handle(fixture.Ctx, authCommands.SendSocialSignUpCodeCommand{
		PendingToken: pendingToken, Email: "new@example.com", Language: "tr",
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.mail.VerificationLanguage != "tr" {
		t.Fatalf("social sign-up code mailed in %q, want tr", fixture.mail.VerificationLanguage)
	}

	if _, err := fixture.sendCode.Handle(fixture.Ctx, authCommands.SendSocialSignUpCodeCommand{
		PendingToken: pendingToken, Email: "new@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.mail.VerificationLanguage != "en" {
		t.Fatalf("social sign-up code without a request language mailed in %q, want en", fixture.mail.VerificationLanguage)
	}
}
