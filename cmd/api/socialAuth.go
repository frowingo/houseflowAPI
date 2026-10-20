package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/infrastructure/socialAuth"
)

// socialAuthAdapters builds the verifiers of the configured sign-in
// providers. A provider without configuration is left out and reported as not
// supported; a configured provider that cannot be built stops the start-up.
func socialAuthAdapters(cfg config.ConfigSocialAuth) ([]authAbstract.SocialIdentityVerifier, []authAbstract.SocialAuthorizationKeeper, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var verifiers []authAbstract.SocialIdentityVerifier
	var keepers []authAbstract.SocialAuthorizationKeeper
	var enabled []string

	if cfg.Google.Enabled() {
		keys := socialAuth.NewJWKSCache(socialAuth.GoogleJWKSURL, client)
		verifiers = append(verifiers, socialAuth.NewGoogleVerifier(keys, cfg.Google.ClientIDs))
		enabled = append(enabled, "google")
	}
	if cfg.Apple.Enabled() {
		cipher, err := socialAuth.NewTokenCipher(cfg.TokenEncryptionKey)
		if err != nil {
			return nil, nil, fmt.Errorf("apple sign-in: %w", err)
		}
		keeper, err := socialAuth.NewAppleAuthorizationKeeper(cfg.Apple.TeamID, cfg.Apple.KeyID, cfg.Apple.PrivateKey, cipher, client)
		if err != nil {
			return nil, nil, fmt.Errorf("apple sign-in: %w", err)
		}
		keys := socialAuth.NewJWKSCache(socialAuth.AppleJWKSURL, client)
		verifiers = append(verifiers, socialAuth.NewAppleVerifier(keys, cfg.Apple.ClientIDs))
		keepers = append(keepers, keeper)
		enabled = append(enabled, "apple")
	}
	log.Printf("social sign-in providers enabled: %v", enabled)
	return verifiers, keepers, nil
}
