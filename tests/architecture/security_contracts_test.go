package architecture

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"houseflowApi/external/validator"
	"houseflowApi/internal/config"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"

	"github.com/golang-jwt/jwt/v5"
)

func TestMustLoadConfigUsesEnvInProductionWithoutConfigFile(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("MONGO_URI", "mongodb://example:27017")
	t.Setenv("MONGO_DB", "houseflow_test")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "7")
	t.Setenv("JOIN_CODE_SECRET", "join-code-secret")
	t.Setenv("SMTP_PASSWORD", "smtp-secret")
	t.Setenv("AWS_ENDPOINT_URL_S3", "https://t3.storage.dev")
	t.Setenv("AWS_REGION", "auto")
	t.Setenv("BUCKET_NAME", "houseflow-test")
	t.Setenv("AWS_ACCESS_KEY_ID", "tid_test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "tsec_test")

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
	if cfg.Internal.HouseJoinCode.Secret != "join-code-secret" {
		t.Fatal("house join code environment secret was not applied")
	}
	if cfg.Internal.HouseJoinCode.ValidityMinutes != 2 {
		t.Fatalf("unexpected house join code validity: %d", cfg.Internal.HouseJoinCode.ValidityMinutes)
	}
	if cfg.Internal.SMTP.Password != "smtp-secret" {
		t.Fatal("SMTP password environment override was not applied")
	}
	if cfg.External.Tigris.Endpoint != "https://t3.storage.dev" || cfg.External.Tigris.Region != "auto" ||
		cfg.External.Tigris.Bucket != "houseflow-test" || cfg.External.Tigris.AccessKeyID != "tid_test" ||
		cfg.External.Tigris.SecretAccessKey != "tsec_test" {
		t.Fatalf("Tigris environment overrides were not applied: %+v", cfg.External.Tigris)
	}
}

func TestValidateTigrisRequiresCompleteConfiguration(t *testing.T) {
	if err := config.ValidateTigris(config.ConfigTigris{}); err == nil {
		t.Fatal("missing Tigris config must be invalid")
	}
	if err := config.ValidateTigris(config.ConfigTigris{Bucket: "partial"}); err == nil {
		t.Fatal("partial Tigris config must be invalid")
	}
	if err := config.ValidateTigris(config.ConfigTigris{
		Endpoint: "not-a-url", Region: "auto", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret",
	}); err == nil {
		t.Fatal("invalid Tigris endpoint must be rejected")
	}
	if err := config.ValidateTigris(config.ConfigTigris{
		Endpoint: "https://t3.storage.dev", Region: "auto", Bucket: "bucket",
		AccessKeyID: "access", SecretAccessKey: "secret",
	}); err != nil {
		t.Fatalf("complete Tigris config must be valid: %v", err)
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

func TestJWTServiceRequiresHS256AndExpiry(t *testing.T) {
	const secret = "jwt-contract-secret"
	service := helpers.NewJWTService(secret)
	sign := func(method jwt.SigningMethod, claims jwt.MapClaims) string {
		t.Helper()
		token, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	expiresAt := time.Now().Add(time.Hour).Unix()

	if _, err := service.ValidateToken(sign(jwt.SigningMethodHS512, jwt.MapClaims{"sub": "user-id", "exp": expiresAt})); err == nil {
		t.Fatal("HS512 token unexpectedly validated")
	}
	if _, err := service.ValidateToken(sign(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "user-id"})); err == nil {
		t.Fatal("token without exp unexpectedly validated")
	}
	claims, err := service.ValidateToken(sign(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "user-id", "exp": expiresAt}))
	if err != nil {
		t.Fatalf("token without iat: %v", err)
	}
	if claims.Subject != "user-id" || !claims.IssuedAt.IsZero() {
		t.Fatalf("claims=%+v, want subject and zero issuedAt", claims)
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

func TestFreeTextRequestFieldsAreBounded(t *testing.T) {
	v := validator.NewValidator()
	houseID := strings.Repeat("a", 24)
	longURL := "https://example.test/" + strings.Repeat("a", 2048)
	empty, oneLetter, longName := "", "J", strings.Repeat("a", 51)

	invalid := map[string]any{
		"announcement title":       dtos.CreateAnnouncementModel{HouseId: houseID, Title: strings.Repeat("a", 201), Description: "ok"},
		"announcement description": dtos.CreateAnnouncementModel{HouseId: houseID, Title: "ok", Description: strings.Repeat("a", 2001)},
		"user first name":          dtos.UpdateUserModel{Firstname: &longName},
		"user last name":           dtos.UpdateUserModel{Lastname: &longName},
		"user image url":           dtos.UpdateUserModel{ImageURL: &longURL},
		"house profile image":      dtos.UpdateHouseProfileModel{HouseProfileImage: &longURL},
	}
	for name, model := range invalid {
		err := v.Validate(model)
		if err == nil || !strings.Contains(err.Error(), "validation.error.max") {
			t.Errorf("%s: err=%v, want validation.error.max", name, err)
		}
	}

	// Profile names stay optional and keep accepting what social sign-up stores.
	if err := v.Validate(dtos.UpdateUserModel{Firstname: &oneLetter, Lastname: &empty}); err != nil {
		t.Errorf("short profile names were rejected: %v", err)
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

func TestHouseResponsesAndInviteEntityDoNotExposeInviteSecrets(t *testing.T) {
	housePayload, err := json.Marshal(dtos.HouseResponseModel{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(housePayload), "inviteCode") {
		t.Fatalf("house response exposes an invite code: %s", housePayload)
	}

	invitePayload, err := json.Marshal(entities.HouseInviteCode{
		CodeDigest: "sensitive-digest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(invitePayload), "sensitive-digest") || strings.Contains(string(invitePayload), "codeDigest") {
		t.Fatalf("invite entity exposes its digest: %s", invitePayload)
	}
}
