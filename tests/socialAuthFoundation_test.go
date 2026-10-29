package tests

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"houseflowApi/internal/config"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
)

func TestNormalizeEmailAndLookupFilter(t *testing.T) {
	if got := helpers.NormalizeEmail("  Emre@Example.COM "); got != "emre@example.com" {
		t.Fatalf("NormalizeEmail=%q", got)
	}
	if got := helpers.EmailLookupFilter(" emre@example.com"); !reflect.DeepEqual(got, bson.M{"email": "emre@example.com"}) {
		t.Fatalf("lowercase lookup=%v", got)
	}
	want := bson.M{"email": bson.M{"$in": bson.A{"emre@example.com", "Emre@Example.com"}}}
	if got := helpers.EmailLookupFilter("Emre@Example.com "); !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed case lookup=%v, want %v", got, want)
	}
}

func setRequiredConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("MONGO_URI", "mongodb://localhost:27017")
	t.Setenv("MONGO_DB", "houseflow_test")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "5")
	t.Setenv("JOIN_CODE_SECRET", "join-secret")
	t.Setenv("SMTP_PASSWORD", "smtp-password")
	t.Setenv("AWS_ENDPOINT_URL_S3", "https://t3.storage.dev")
	t.Setenv("AWS_REGION", "auto")
	t.Setenv("BUCKET_NAME", "houseflow-test")
	t.Setenv("AWS_ACCESS_KEY_ID", "tid_test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "tsec_test")
	for _, name := range []string{
		"GOOGLE_CLIENT_IDS", "APPLE_CLIENT_IDS", "APPLE_TEAM_ID", "APPLE_KEY_ID", "APPLE_PRIVATE_KEY",
		"FACEBOOK_APP_ID", "FACEBOOK_APP_SECRET", "FACEBOOK_GRAPH_VERSION", "SNAPCHAT_CLIENT_ID", "SOCIAL_TOKEN_ENC_KEY",
	} {
		t.Setenv(name, "")
	}
}

func TestSocialAuthProvidersAreDisabledWithoutConfig(t *testing.T) {
	setRequiredConfigEnv(t)
	loaded, err := config.MustLoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	social := loaded.External.SocialAuth
	if social.Google.Enabled() || social.Apple.Enabled() || social.Facebook.Enabled() || social.Snapchat.Enabled() {
		t.Fatalf("providers enabled without config: %+v", social)
	}
}

func TestSocialAuthConfigReadsEnvironment(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("GOOGLE_CLIENT_IDS", " ios.apps.googleusercontent.com, ,web.apps.googleusercontent.com ")
	t.Setenv("APPLE_CLIENT_IDS", "com.houseflow.app")
	t.Setenv("APPLE_TEAM_ID", "TEAM123")
	t.Setenv("APPLE_KEY_ID", "KEY123")
	t.Setenv("APPLE_PRIVATE_KEY", `-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----`)
	t.Setenv("SOCIAL_TOKEN_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("FACEBOOK_APP_ID", "fb-app")
	t.Setenv("FACEBOOK_APP_SECRET", "fb-secret")
	t.Setenv("FACEBOOK_GRAPH_VERSION", "v24.0")
	t.Setenv("SNAPCHAT_CLIENT_ID", "snap-client")

	loaded, err := config.MustLoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	social := loaded.External.SocialAuth
	if want := []string{"ios.apps.googleusercontent.com", "web.apps.googleusercontent.com"}; !reflect.DeepEqual(social.Google.ClientIDs, want) {
		t.Fatalf("google client ids=%v, want %v", social.Google.ClientIDs, want)
	}
	if !social.Google.Enabled() || !social.Apple.Enabled() || !social.Facebook.Enabled() || !social.Snapchat.Enabled() {
		t.Fatalf("configured providers not enabled: %+v", social)
	}
	if social.Facebook.GraphVersion != "v24.0" {
		t.Fatalf("facebook graph version=%q", social.Facebook.GraphVersion)
	}
	if strings.Count(social.Apple.PrivateKey, "\n") != 2 {
		t.Fatalf("apple private key line breaks not restored: %q", social.Apple.PrivateKey)
	}
}

func TestSocialAuthConfigRejectsPartialProviders(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	completeApple := config.ConfigAppleAuth{ClientIDs: []string{"com.houseflow.app"}, TeamID: "T", KeyID: "K", PrivateKey: "P"}
	testCases := map[string]config.ConfigSocialAuth{
		"apple without private key":  {Apple: config.ConfigAppleAuth{ClientIDs: []string{"com.houseflow.app"}, TeamID: "T", KeyID: "K"}, TokenEncryptionKey: validKey},
		"apple key without clients":  {Apple: config.ConfigAppleAuth{TeamID: "T", KeyID: "K", PrivateKey: "P"}, TokenEncryptionKey: validKey},
		"apple without encryption":   {Apple: completeApple},
		"apple with short key":       {Apple: completeApple, TokenEncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 16))},
		"facebook without secret":    {Facebook: config.ConfigFacebookAuth{AppID: "fb-app"}},
		"facebook secret without id": {Facebook: config.ConfigFacebookAuth{AppSecret: "fb-secret"}},
	}
	for name, social := range testCases {
		t.Run(name, func(t *testing.T) {
			if err := config.ValidateSocialAuth(social); err == nil {
				t.Fatal("partial provider config accepted")
			}
		})
	}
	if err := config.ValidateSocialAuth(config.ConfigSocialAuth{Apple: completeApple, TokenEncryptionKey: validKey}); err != nil {
		t.Fatalf("complete apple config rejected: %v", err)
	}
}
