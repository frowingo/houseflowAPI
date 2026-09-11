package tests

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
)

func TestMustLoadConfigUsesEnvInProductionWithoutConfigFile(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("MONGO_URI", "mongodb://example:27017")
	t.Setenv("MONGO_DB", "houseflow_test")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "7")
	t.Setenv("SMTP_PASSWORD", "smtp-secret")

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
	if cfg.Internal.SMTP.Password != "smtp-secret" {
		t.Fatal("SMTP password environment override was not applied")
	}
}

func TestJWTServiceKeepsStartupSecret(t *testing.T) {
	service := helpers.NewJWTService("startup-jwt-secret")
	token, err := service.GenerateToken("user@example.com", "user-id", int(entities.Normal), "en")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	t.Setenv("JWT_SECRET", "changed-after-startup")
	claims, err := service.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate token with startup secret: %v", err)
	}
	if claims.Subject != "user-id" {
		t.Fatalf("token subject = %q, want user-id", claims.Subject)
	}

	if _, err := helpers.NewJWTService("changed-after-startup").ValidateToken(token); err == nil {
		t.Fatal("token unexpectedly validated with a different secret")
	}
}

func TestIsAuthResponseUsesHouseListContract(t *testing.T) {
	response := dtos.IsAuthResponseModel{
		Success: true,
		Data: &dtos.AuthUserResultModel{
			HouseList: []dtos.AuthHouseModel{{
				HouseID: "house-id", HouseName: "Home", HouseProfile: "profile-url",
			}},
		},
	}

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal isAuth response: %v", err)
	}
	body := string(payload)
	if !strings.Contains(body, `"houseList":[{"houseId":"house-id","houseName":"Home","houseProfile":"profile-url"}]`) {
		t.Fatalf("isAuth response does not contain houseList contract: %s", body)
	}
	if strings.Contains(body, `"houseIds"`) {
		t.Fatalf("isAuth response still exposes houseIds: %s", body)
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
