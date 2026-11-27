package adaptors

import (
	"fmt"
	"log"
	"net/http"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/config"
	"houseflowApi/internal/infrastructure/socialAuth"
)

type SocialAuthAdapters struct {
	Verifiers         []authAbstract.SocialIdentityVerifier
	Keepers           []authAbstract.SocialAuthorizationKeeper
	DeletionVerifiers []authAbstract.DataDeletionRequestVerifier
	Notifications     []authAbstract.ProviderNotificationVerifier
}

// NewSocialAuthAdapters builds the adapters of the configured sign-in providers.
// A provider without configuration is left out and reported as not
// supported; a configured provider that cannot be built stops the start-up.
func NewSocialAuthAdapters(cfg config.ConfigSocialAuth) (SocialAuthAdapters, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var adapters SocialAuthAdapters
	var enabled []string

	if cfg.Google.Enabled() {
		keys := socialAuth.NewJWKSCache(socialAuth.GoogleJWKSURL, client)
		adapters.Verifiers = append(adapters.Verifiers, socialAuth.NewGoogleVerifier(keys, cfg.Google.ClientIDs))
		enabled = append(enabled, "google")
	}
	if cfg.Apple.Enabled() {
		cipher, err := socialAuth.NewTokenCipher(cfg.TokenEncryptionKey)
		if err != nil {
			return SocialAuthAdapters{}, fmt.Errorf("apple sign-in: %w", err)
		}
		keeper, err := socialAuth.NewAppleAuthorizationKeeper(cfg.Apple.TeamID, cfg.Apple.KeyID, cfg.Apple.PrivateKey, cipher, client)
		if err != nil {
			return SocialAuthAdapters{}, fmt.Errorf("apple sign-in: %w", err)
		}
		keys := socialAuth.NewJWKSCache(socialAuth.AppleJWKSURL, client)
		apple := socialAuth.NewAppleVerifier(keys, cfg.Apple.ClientIDs)
		adapters.Verifiers = append(adapters.Verifiers, apple)
		adapters.Notifications = append(adapters.Notifications, apple)
		adapters.Keepers = append(adapters.Keepers, keeper)
		enabled = append(enabled, "apple")
	}
	if cfg.Facebook.Enabled() {
		keys := socialAuth.NewJWKSCache(socialAuth.FacebookLimitedLoginJWKSURL, client)
		facebook := socialAuth.NewFacebookVerifier(keys, cfg.Facebook.AppID, cfg.Facebook.AppSecret, client,
			socialAuth.WithFacebookGraphVersion(cfg.Facebook.GraphVersion))
		adapters.Verifiers = append(adapters.Verifiers, facebook)
		adapters.DeletionVerifiers = append(adapters.DeletionVerifiers, facebook)
		enabled = append(enabled, "facebook")
	}
	if cfg.Snapchat.Enabled() {
		adapters.Verifiers = append(adapters.Verifiers, socialAuth.NewSnapchatVerifier(cfg.Snapchat.ClientID, client))
		enabled = append(enabled, "snapchat")
	}
	log.Printf("social sign-in providers enabled: %v", enabled)
	return adapters, nil
}
