package socialAuth

import (
	"context"
	"encoding/json"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/data/entities"
)

// AppleJWKSURL publishes the keys Apple signs identity tokens with.
const AppleJWKSURL = "https://appleid.apple.com/auth/keys"

const appleIssuer = "https://appleid.apple.com"

// AppleVerifier checks the identity token of Sign in with Apple. The client
// sends the raw nonce; Apple was given its SHA-256 hex digest, which is what
// the token carries. A nonce is required so a captured token cannot be
// replayed.
//
// Apple puts the email in the token on every sign-in, but the user's name only
// reaches the client on the first authorization and never the token, so the
// name comes from the request instead.
type AppleVerifier struct {
	clientIDs []string
	tokens    *IDTokenVerifier
}

func NewAppleVerifier(keys *JWKSCache, clientIDs []string, options ...IDTokenOption) *AppleVerifier {
	return &AppleVerifier{
		clientIDs: clientIDs,
		tokens:    NewIDTokenVerifier(keys, []string{appleIssuer}, clientIDs, []string{"RS256"}, options...),
	}
}

var _ authAbstract.SocialIdentityVerifier = (*AppleVerifier)(nil)

func (v *AppleVerifier) Provider() entities.AuthProvider { return entities.ProviderApple }

func (v *AppleVerifier) Verify(ctx context.Context, credential authAbstract.SocialCredential) (authAbstract.VerifiedIdentity, error) {
	if credential.Nonce == "" {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
	claims, err := v.tokens.Verify(ctx, credential.IDToken)
	if err != nil {
		return authAbstract.VerifiedIdentity{}, err
	}
	if !equalConstantTime(stringClaim(claims, "nonce"), sha256Hex(credential.Nonce)) {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}

	subject, _ := claims.GetSubject()
	email := stringClaim(claims, "email")
	return authAbstract.VerifiedIdentity{
		Provider:       entities.ProviderApple,
		ProviderUserID: subject,
		ClientID:       matchedAudience(claims, v.clientIDs),
		Email:          email,
		// Apple only hands out addresses it has verified, including relay ones.
		EmailVerified:  email != "" && (boolClaim(claims, "email_verified") || claims["email_verified"] == nil),
		IsPrivateRelay: boolClaim(claims, "is_private_email"),
	}, nil
}

var _ authAbstract.ProviderNotificationVerifier = (*AppleVerifier)(nil)

// VerifyNotification checks the payload of a Sign in with Apple
// server-to-server notification: a JWT signed with Apple's identity keys,
// issued to one of our client ids, whose "events" claim is a JSON string.
func (v *AppleVerifier) VerifyNotification(ctx context.Context, payload string) (authAbstract.ProviderAccountEvent, error) {
	claims, err := v.tokens.verifySigned(ctx, payload)
	if err != nil {
		return authAbstract.ProviderAccountEvent{}, err
	}
	var event struct {
		Type    string `json:"type"`
		Subject string `json:"sub"`
	}
	rawEvent := []byte(stringClaim(claims, "events"))
	if object, isObject := claims["events"].(map[string]any); isObject {
		// Tolerate the event as a JSON object rather than a string.
		rawEvent, _ = json.Marshal(object)
	}
	if err := json.Unmarshal(rawEvent, &event); err != nil || event.Subject == "" {
		return authAbstract.ProviderAccountEvent{}, invalidSocialToken()
	}

	eventType := authAbstract.ProviderEventUnknown
	switch event.Type {
	case "consent-revoked":
		eventType = authAbstract.ProviderConsentRevoked
	// Apple's documentation says account-delete; deliveries have been seen as account-deleted.
	case "account-delete", "account-deleted":
		eventType = authAbstract.ProviderAccountDeleted
	case "email-disabled", "email-enabled":
		eventType = authAbstract.ProviderEmailChanged
	}
	return authAbstract.ProviderAccountEvent{Type: eventType, ProviderUserID: event.Subject}, nil
}
