package tests

import (
	"testing"

	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
)

func TestAuthCommandsAndQueryCompleteAuthenticationFlow(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("MONGO_URI", "mongodb://mongodb:27017/?replicaSet=rs0")
	t.Setenv("MONGO_DB", "houseflow_test")
	t.Setenv("JWT_SECRET", "auth-cqrs-jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "auth-cqrs-reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "5")

	fixture := newConcurrencyFixture(t)
	email := "auth-cqrs@example.com"

	token, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.SignUpCommand{
		Email: email, Password: "initial-password", Firstname: "Auth", Lastname: "User",
	})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("signup returned an empty token")
	}

	authenticatedUser, err := cqrs.Send[*dtos.UserResultModel](fixture.ctx, fixture.sender, authQueries.ValidateAuthQuery{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if authenticatedUser.Email != email {
		t.Fatalf("authenticated email=%q, want %q", authenticatedUser.Email, email)
	}

	historyCount, err := fixture.db.Collection("UserInfoHistory").CountDocuments(fixture.ctx, bson.M{"userId": authenticatedUser.Id})
	if err != nil {
		t.Fatal(err)
	}
	if historyCount != 3 {
		t.Fatalf("signup history count=%d, want 3", historyCount)
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, authCommands.ForgotPasswordCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	if fixture.emailSender.resetCode == "" {
		t.Fatal("forgot password did not send a reset code")
	}
	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, authCommands.ResetPasswordCommand{
		Email: email, Code: fixture.emailSender.resetCode, NewPassword: "updated-password",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.LoginCommand{
		Email: email, Password: "updated-password",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, authCommands.SendEmailVerificationCodeCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	if fixture.emailSender.verificationCode == "" {
		t.Fatal("email verification did not send a code")
	}
	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, authCommands.ValidateEmailCommand{
		Email: email, Code: fixture.emailSender.verificationCode,
	}); err != nil {
		t.Fatal(err)
	}

	var storedUser entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"email": email}).Decode(&storedUser); err != nil {
		t.Fatal(err)
	}
	if !storedUser.IsVerifyEmail {
		t.Fatal("email was not marked as verified")
	}
}
