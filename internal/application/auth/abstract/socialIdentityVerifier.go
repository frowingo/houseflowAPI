package abstract

import (
	"context"

	"houseflowApi/internal/data/entities"
)

// SocialCredential is what the client obtained from a provider SDK. Which
// fields are set depends on the provider.
type SocialCredential struct {
	IDToken           string
	AccessToken       string
	Nonce             string
	AuthorizationCode string
}

// VerifiedIdentity is the provider account a credential proved. Email is
// empty when the provider did not share one.
type VerifiedIdentity struct {
	Provider       entities.AuthProvider
	ProviderUserID string
	// ClientID is our client id the provider issued the credential for.
	ClientID       string
	Email          string
	EmailVerified  bool
	IsPrivateRelay bool
	FirstName      string
	LastName       string
}

// SocialIdentityVerifier is the outbound port that checks a provider
// credential. It never trusts identity values sent by the client: everything
// it returns comes from the verified token or from the provider itself.
type SocialIdentityVerifier interface {
	Provider() entities.AuthProvider
	Verify(ctx context.Context, credential SocialCredential) (VerifiedIdentity, error)
}

// SocialAuthorizationKeeper keeps what is needed to revoke a provider
// authorization when the account is deleted (Apple requires this). Keep runs
// once, when the account is created; its result is opaque (already
// encrypted) and is stored as is.
type SocialAuthorizationKeeper interface {
	Provider() entities.AuthProvider
	Keep(ctx context.Context, identity VerifiedIdentity, credential SocialCredential) (string, error)
	Revoke(ctx context.Context, keptToken string, clientID string) error
}

// DataDeletionRequestVerifier checks a data deletion request a provider sends
// when a user removes our app (Facebook's signed_request) and returns the
// provider user id it is about.
type DataDeletionRequestVerifier interface {
	Provider() entities.AuthProvider
	VerifyDataDeletionRequest(signedRequest string) (string, error)
}

// ProviderAccountEventType is what a provider reports about one of its users.
type ProviderAccountEventType string

const (
	// ProviderConsentRevoked: the user stopped using the provider to sign in to our app.
	ProviderConsentRevoked ProviderAccountEventType = "consentRevoked"
	// ProviderAccountDeleted: the user deleted their account at the provider.
	ProviderAccountDeleted ProviderAccountEventType = "accountDeleted"
	// ProviderEmailChanged: the provider started or stopped forwarding to the relay email.
	ProviderEmailChanged ProviderAccountEventType = "emailChanged"
	// ProviderEventUnknown: an event type this server does not handle.
	ProviderEventUnknown ProviderAccountEventType = "unknown"
)

type ProviderAccountEvent struct {
	Type           ProviderAccountEventType
	ProviderUserID string
}

// ProviderNotificationVerifier checks a notification a provider pushes to us
// (Apple's server-to-server notifications) and returns the event it carries.
type ProviderNotificationVerifier interface {
	Provider() entities.AuthProvider
	VerifyNotification(ctx context.Context, payload string) (ProviderAccountEvent, error)
}
