package tests

import (
	"testing"

	authCommands "houseflowApi/internal/application/auth/commands"
	authQueries "houseflowApi/internal/application/auth/queries"
	housecommands "houseflowApi/internal/application/house/commands"
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

	authenticatedUser, err := cqrs.Send[*dtos.AuthUserResultModel](fixture.ctx, fixture.sender, authQueries.ValidateAuthQuery{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if authenticatedUser.Email != email {
		t.Fatalf("authenticated email=%q, want %q", authenticatedUser.Email, email)
	}
	if len(authenticatedUser.HouseList) != 0 {
		t.Fatalf("new user house list=%+v, want empty", authenticatedUser.HouseList)
	}

	house, err := cqrs.Send[*entities.House](fixture.ctx, fixture.sender, housecommands.CreateHouseCommand{
		OwnerID: authenticatedUser.Id, Name: "Auth House", Type: entities.SharedHouse, MaxMemberCount: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	house.ProfileImage = "https://example.test/house.png"
	if _, err := fixture.db.Collection("House").UpdateOne(
		fixture.ctx, bson.M{"_id": house.Id}, bson.M{"$set": bson.M{"profileImage": house.ProfileImage}},
	); err != nil {
		t.Fatal(err)
	}

	authenticatedUser, err = cqrs.Send[*dtos.AuthUserResultModel](fixture.ctx, fixture.sender, authQueries.ValidateAuthQuery{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if len(authenticatedUser.HouseList) != 1 {
		t.Fatalf("authenticated house list=%+v, want one house", authenticatedUser.HouseList)
	}
	authHouse := authenticatedUser.HouseList[0]
	if authHouse.HouseID != house.Id.Hex() || authHouse.HouseName != house.Name || authHouse.HouseProfile != house.ProfileImage {
		t.Fatalf("authenticated house=%+v, want id/name/profile from house", authHouse)
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
