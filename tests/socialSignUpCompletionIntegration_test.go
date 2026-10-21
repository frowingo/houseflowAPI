package tests

import (
	"crypto/hmac"
	"crypto/sha256"
	"testing"
	"time"

	authCommands "houseflowApi/internal/application/auth/commands"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
)

func (f *socialAuthFixture) snapchatSignIn(t *testing.T) (dtos.SocialAuthResponseModel, error) {
	t.Helper()
	return f.handler.Handle(f.ctx, authCommands.SocialAuthCommand{
		Provider: entities.ProviderSnapchat, Lastname: "Chat", Language: "tr",
	})
}

func (f *socialAuthFixture) pendingSnapchatSignUp(t *testing.T) string {
	t.Helper()
	result, err := f.snapchatSignIn(t)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsEmail || result.PendingToken == "" || result.Token != "" || result.IsNewUser {
		t.Fatalf("email-less sign-in=%+v, want needsEmail with a pending token", result)
	}
	return result.PendingToken
}

func (f *socialAuthFixture) requestCode(t *testing.T, pendingToken, email string) string {
	t.Helper()
	f.mail.verificationCode = ""
	if _, err := f.sendCode.Handle(f.ctx, authCommands.SendSocialSignUpCodeCommand{PendingToken: pendingToken, Email: email}); err != nil {
		t.Fatal(err)
	}
	if f.mail.verificationCode == "" {
		t.Fatal("no code mailed")
	}
	return f.mail.verificationCode
}

func (f *socialAuthFixture) completeSignUp(pendingToken, email, code string) (dtos.SocialAuthResponseModel, error) {
	return f.complete.Handle(f.ctx, authCommands.CompleteSocialSignUpCommand{
		PendingToken: pendingToken, Email: email, Code: code, Lastname: "Chat", Language: "tr",
	})
}

func TestEmaillessSocialSignUpCreatesVerifiedAccountAfterCode(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)
	if users := fixture.count(t, "User", bson.M{}); users != 0 {
		t.Fatalf("users=%d before the email step, want 0", users)
	}

	code := fixture.requestCode(t, pendingToken, "  Snap.User@Example.com ")
	if _, err := fixture.completeSignUp(pendingToken, "snap.user@example.com", "AAAA00"); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
		t.Fatalf("wrong code err=%v", err)
	}

	created, err := fixture.completeSignUp(pendingToken, "Snap.User@example.com", code)
	if err != nil {
		t.Fatal(err)
	}
	if !created.IsNewUser || created.Token == "" {
		t.Fatalf("completion=%+v, want a new user with a token", created)
	}
	var user entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"email": "snap.user@example.com"}).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if !user.IsVerifyEmail || user.SignUpProvider != entities.ProviderSnapchat || user.HashPassword != "" ||
		user.Firstname != "Snappy" || user.Lastname != "Chat" || user.Language != "tr" {
		t.Fatalf("stored user=%+v", user)
	}
	var identity entities.UserIdentity
	if err := fixture.db.Collection("UserIdentity").FindOne(fixture.ctx, bson.M{"userId": user.Id.Hex()}).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	if identity.ProviderUserId != "snap-external-1" || identity.ProviderClientId != "snap-client" || identity.EmailVerified {
		t.Fatalf("stored identity=%+v", identity)
	}

	signedIn, err := fixture.snapchatSignIn(t)
	if err != nil {
		t.Fatal(err)
	}
	if signedIn.NeedsEmail || signedIn.IsNewUser || signedIn.Token == "" {
		t.Fatalf("later sign-in=%+v, want our token for the existing account", signedIn)
	}

	// Completing again with the same pending token signs in instead of creating a second account.
	again, err := fixture.completeSignUp(pendingToken, "snap.user@example.com", code)
	if err != nil || again.IsNewUser || again.Token == "" {
		t.Fatalf("repeated completion=%+v err=%v", again, err)
	}
	if users := fixture.count(t, "User", bson.M{}); users != 1 {
		t.Fatalf("users=%d, want 1", users)
	}
}

func TestSocialSignUpCodeIsBoundToEmailAndIdentity(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)
	code := fixture.requestCode(t, pendingToken, "first@example.com")

	if _, err := fixture.completeSignUp(pendingToken, "second@example.com", code); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
		t.Fatalf("code for another email err=%v", err)
	}

	fixture.snapchat.identity.ProviderUserID = "snap-external-2"
	otherPending := fixture.pendingSnapchatSignUp(t)
	if _, err := fixture.completeSignUp(otherPending, "first@example.com", code); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
		t.Fatalf("code for another identity err=%v", err)
	}
	if users := fixture.count(t, "User", bson.M{}); users != 0 {
		t.Fatalf("users=%d, want 0", users)
	}
}

func TestSocialSignUpCompletionRejectsTakenEmail(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)
	code := fixture.requestCode(t, pendingToken, "taken@example.com")

	existing := fixture.seedUser(t, "correct-password")
	if _, err := fixture.db.Collection("User").UpdateOne(fixture.ctx, bson.M{"_id": existing.Id},
		bson.M{"$set": bson.M{"email": "Taken@Example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.sendCode.Handle(fixture.ctx, authCommands.SendSocialSignUpCodeCommand{
		PendingToken: pendingToken, Email: "taken@example.com",
	}); !helpers.IsApplicationError(err, "auth.error.email_registered_with_other_method") {
		t.Fatalf("code for a taken email err=%v", err)
	}
	if _, err := fixture.completeSignUp(pendingToken, "taken@example.com", code); !helpers.IsApplicationError(err, "auth.error.email_registered_with_other_method") {
		t.Fatalf("completion with a taken email err=%v", err)
	}
	if identities := fixture.count(t, "UserIdentity", bson.M{}); identities != 0 {
		t.Fatalf("identities=%d, want 0", identities)
	}
}

func TestPendingSocialTokenCannotBeForgedOrReused(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)

	jwtService := helpers.NewJWTService("social-auth-jwt-secret")
	if _, err := jwtService.ValidateToken(pendingToken); err == nil {
		t.Fatal("pending token accepted as an access token")
	}
	accessToken, err := jwtService.GenerateToken("user@example.com", "user-id", 0, "en")
	if err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha256.New, []byte("social-auth-jwt-secret"))
	mac.Write([]byte("social-pending"))
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"typ": "socialPendingSignUp", "provider": int(entities.ProviderSnapchat), "sub": "snap-external-1",
		"iat": time.Now().Add(-time.Hour).Unix(), "exp": time.Now().Add(-time.Minute).Unix(),
	}).SignedString(mac.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{
		"access token":  accessToken,
		"expired":       expired,
		"tampered":      pendingToken[:len(pendingToken)-3] + "abc",
		"other service": mustIssuePending(t, helpers.NewSocialPendingTokenService("another-secret")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fixture.sendCode.Handle(fixture.ctx, authCommands.SendSocialSignUpCodeCommand{PendingToken: token, Email: "x@example.com"})
			if !helpers.IsApplicationError(err, "auth.error.social_signup_expired") {
				t.Fatalf("err=%v, want auth.error.social_signup_expired", err)
			}
		})
	}
}

func mustIssuePending(t *testing.T, service *helpers.SocialPendingTokenService) string {
	t.Helper()
	token, err := service.Issue(helpers.PendingSocialSignUp{Provider: entities.ProviderSnapchat, ProviderUserID: "snap-external-1"})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAppleWithoutEmailDoesNotStartPendingSignUp(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	fixture.apple.identity.Email = ""
	result, err := fixture.appleSignIn(t)
	if !helpers.IsApplicationError(err, "auth.error.social_email_required") || result.PendingToken != "" {
		t.Fatalf("result=%+v err=%v, want social_email_required", result, err)
	}
}
