package tests

import (
	"testing"
	"time"

	authCommands "houseflowApi/internal/application/auth/commands"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func setAuthTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "social-auth-jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "social-auth-reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "5")
}

func TestPasswordlessLoginDoesNotCountFailedAttempts(t *testing.T) {
	setAuthTestEnv(t)
	fixture := newConcurrencyFixture(t)
	now := time.Now().UTC()
	result, err := fixture.db.Collection("User").InsertOne(fixture.ctx, entities.User{
		Firstname: "Social", Lastname: "User", Email: "social@example.com",
		Language: "en", HouseIds: []string{}, IsActive: true, CreatedOn: now, UpdatedOn: now, LastLogin: now,
		SignUpProvider: entities.ProviderGoogle,
	})
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 12; attempt++ {
		_, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.LoginCommand{
			Email: "social@example.com", Password: "guessed-password",
		})
		if !helpers.IsApplicationError(err, "auth.error.use_social_login") {
			t.Fatalf("attempt %d: err=%v, want auth.error.use_social_login", attempt, err)
		}
	}

	var stored entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"_id": result.InsertedID}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.FailedLoginAttempts != 0 || !stored.IsActive {
		t.Fatalf("failedLoginAttempts=%d isActive=%v, want 0 and active", stored.FailedLoginAttempts, stored.IsActive)
	}
}

func TestEmailIsNormalizedOnSignUpAndLookup(t *testing.T) {
	setAuthTestEnv(t)
	fixture := newConcurrencyFixture(t)

	if _, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.SignUpCommand{
		Email: "  Mixed.Case@Example.COM ", Password: "initial-password", Firstname: "Mixed", Lastname: "Case",
	}); err != nil {
		t.Fatal(err)
	}
	var stored entities.User
	if err := fixture.db.Collection("User").FindOne(fixture.ctx, bson.M{"email": "mixed.case@example.com"}).Decode(&stored); err != nil {
		t.Fatalf("signed up user not stored with normalized email: %v", err)
	}
	if stored.SignUpProvider != entities.ProviderHouseflow {
		t.Fatalf("signUpProvider=%d, want houseflow", stored.SignUpProvider)
	}

	if _, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.LoginCommand{
		Email: "MIXED.case@example.com", Password: "initial-password",
	}); err != nil {
		t.Fatalf("login with different casing: %v", err)
	}
	_, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.SignUpCommand{
		Email: "mixed.CASE@example.com", Password: "other-password", Firstname: "Mixed", Lastname: "Case",
	})
	if !helpers.IsApplicationError(err, "auth.error.user_already_exists") {
		t.Fatalf("second signup with different casing err=%v, want auth.error.user_already_exists", err)
	}
}

func TestLegacyMixedCaseEmailStillLogsIn(t *testing.T) {
	setAuthTestEnv(t)
	fixture := newConcurrencyFixture(t)
	hashedPassword, err := helpers.HashPassword("legacy-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := fixture.db.Collection("User").InsertOne(fixture.ctx, entities.User{
		Firstname: "Legacy", Lastname: "User", Email: "Legacy.User@Example.com", HashPassword: hashedPassword,
		Language: "en", HouseIds: []string{}, IsActive: true, CreatedOn: now, UpdatedOn: now, LastLogin: now,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := cqrs.Send[string](fixture.ctx, fixture.sender, authCommands.LoginCommand{
		Email: "Legacy.User@Example.com", Password: "legacy-password",
	}); err != nil {
		t.Fatalf("legacy account login with stored casing: %v", err)
	}
}

func TestUserIdentityProviderAccountIsUnique(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	now := time.Now().UTC()
	identity := entities.UserIdentity{
		UserId: "user-1", Provider: entities.ProviderApple, ProviderUserId: "apple-sub-1",
		ProviderEmail: "relay@privaterelay.appleid.com", EmailVerified: true, IsPrivateRelay: true,
		CreatedOn: now, LastLoginOn: now,
	}
	collection := fixture.db.Collection("UserIdentity")
	if _, err := collection.InsertOne(fixture.ctx, identity); err != nil {
		t.Fatal(err)
	}

	identity.UserId = "user-2"
	if _, err := collection.InsertOne(fixture.ctx, identity); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("second user for the same provider account err=%v, want duplicate key", err)
	}

	identity.Provider = entities.ProviderGoogle
	if _, err := collection.InsertOne(fixture.ctx, identity); err != nil {
		t.Fatalf("same id under another provider rejected: %v", err)
	}

	if _, err := collection.InsertOne(fixture.ctx, bson.M{
		"userId": "user-3", "provider": int(entities.ProviderHouseflow), "providerUserId": "x", "createdOn": now,
	}); err == nil {
		t.Fatal("identity with the houseflow provider accepted by the schema")
	}
}
