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

type socialAuthAdapterSet struct {
	verifiers         []authAbstract.SocialIdentityVerifier
	keepers           []authAbstract.SocialAuthorizationKeeper
	deletionVerifiers []authAbstract.DataDeletionRequestVerifier
	notifications     []authAbstract.ProviderNotificationVerifier
}

// socialAuthAdapters builds the adapters of the configured sign-in providers.
// A provider without configuration is left out and reported as not
// supported; a configured provider that cannot be built stops the start-up.
func socialAuthAdapters(cfg config.ConfigSocialAuth) (socialAuthAdapterSet, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var adapters socialAuthAdapterSet
	var enabled []string

	if cfg.Google.Enabled() {
		keys := socialAuth.NewJWKSCache(socialAuth.GoogleJWKSURL, client)
		adapters.verifiers = append(adapters.verifiers, socialAuth.NewGoogleVerifier(keys, cfg.Google.ClientIDs))
		enabled = append(enabled, "google")
	}
	if cfg.Apple.Enabled() {
		cipher, err := socialAuth.NewTokenCipher(cfg.TokenEncryptionKey)
		if err != nil {
			return socialAuthAdapterSet{}, fmt.Errorf("apple sign-in: %w", err)
		}
		keeper, err := socialAuth.NewAppleAuthorizationKeeper(cfg.Apple.TeamID, cfg.Apple.KeyID, cfg.Apple.PrivateKey, cipher, client)
		if err != nil {
			return socialAuthAdapterSet{}, fmt.Errorf("apple sign-in: %w", err)
		}
		keys := socialAuth.NewJWKSCache(socialAuth.AppleJWKSURL, client)
		apple := socialAuth.NewAppleVerifier(keys, cfg.Apple.ClientIDs)
		adapters.verifiers = append(adapters.verifiers, apple)
		adapters.notifications = append(adapters.notifications, apple)
		adapters.keepers = append(adapters.keepers, keeper)
		enabled = append(enabled, "apple")
	}
	if cfg.Facebook.Enabled() {
		keys := socialAuth.NewJWKSCache(socialAuth.FacebookLimitedLoginJWKSURL, client)
		facebook := socialAuth.NewFacebookVerifier(keys, cfg.Facebook.AppID, cfg.Facebook.AppSecret, client,
			socialAuth.WithFacebookGraphVersion(cfg.Facebook.GraphVersion))
		adapters.verifiers = append(adapters.verifiers, facebook)
		adapters.deletionVerifiers = append(adapters.deletionVerifiers, facebook)
		enabled = append(enabled, "facebook")
	}
	if cfg.Snapchat.Enabled() {
		adapters.verifiers = append(adapters.verifiers, socialAuth.NewSnapchatVerifier(cfg.Snapchat.ClientID, client))
		enabled = append(enabled, "snapchat")
	}
	log.Printf("social sign-in providers enabled: %v", enabled)
	return adapters, nil
}
