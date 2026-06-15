package config

import "testing"

func TestMustLoadConfigUsesEnvInProductionWithoutConfigFile(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("MONGO_URI", "mongodb://example:27017")
	t.Setenv("MONGO_DB", "houseflow_test")
	t.Setenv("JWT_SECRET", "jwt-secret")
	t.Setenv("RESET_CODE_SECRET", "reset-secret")
	t.Setenv("RESET_CODE_VALIDITY_MINUTES", "7")

	cfg, err := MustLoadConfig()
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
