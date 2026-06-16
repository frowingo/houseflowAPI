package tests

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/models/dtos"
)

func TestMustLoadConfigUsesEnvInProductionWithoutConfigFile(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("MONGO_URI", "mongodb://example:27017")
	t.Setenv("MONGO_DB", "houseflow_test")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "7")

	cfg, err := config.MustLoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.External.Mongo.ConnectionString != "mongodb://example:27017" {
		t.Fatalf("unexpected mongo uri: %q", cfg.External.Mongo.ConnectionString)
	}
	if cfg.Internal.PasswordReset.ValidityMinutes != 7 {
		t.Fatalf("unexpected reset validity: %d", cfg.Internal.PasswordReset.ValidityMinutes)
	}
}

func TestUserJSONDoesNotExposePasswordHash(t *testing.T) {
	user := entities.User{
		Email:        "user@example.com",
		HashPassword: "hashed-secret",
	}

	payload, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}

	body := string(payload)
	if strings.Contains(body, "hashed-secret") || strings.Contains(body, "password") {
		t.Fatalf("user json exposes password data: %s", body)
	}
}

func TestUpdateUserModelDoesNotExposeVerificationFlags(t *testing.T) {
	modelType := reflect.TypeOf(dtos.UpdateUserModel{})

	if _, ok := modelType.FieldByName("IsVerifyPhone"); ok {
		t.Fatal("UpdateUserModel must not allow clients to update phone verification")
	}
	if _, ok := modelType.FieldByName("IsVerifyEmail"); ok {
		t.Fatal("UpdateUserModel must not allow clients to update email verification")
	}
}
