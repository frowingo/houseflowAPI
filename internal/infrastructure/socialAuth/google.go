package socialAuth

import (
	"context"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/data/entities"
)

// GoogleJWKSURL publishes the keys Google signs ID tokens with.
const GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

var googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}

// GoogleVerifier checks the ID token of Google Sign-In. The token's audience
// is the iOS client id on iOS and the web client id (serverClientId) on
// Android, so every configured client id is accepted.
type GoogleVerifier struct {
	clientIDs []string
	tokens    *IDTokenVerifier
}

func NewGoogleVerifier(keys *JWKSCache, clientIDs []string, options ...IDTokenOption) *GoogleVerifier {
	return &GoogleVerifier{
		clientIDs: clientIDs,
		tokens:    NewIDTokenVerifier(keys, googleIssuers, clientIDs, []string{"RS256"}, options...),
	}
}

var _ authAbstract.SocialIdentityVerifier = (*GoogleVerifier)(nil)

func (v *GoogleVerifier) Provider() entities.AuthProvider { return entities.ProviderGoogle }

func (v *GoogleVerifier) Verify(ctx context.Context, credential authAbstract.SocialCredential) (authAbstract.VerifiedIdentity, error) {
	claims, err := v.tokens.Verify(ctx, credential.IDToken)
	if err != nil {
		return authAbstract.VerifiedIdentity{}, err
	}
	// A nonce is optional for Google; when either side has one, both must match.
	if tokenNonce := stringClaim(claims, "nonce"); (tokenNonce != "" || credential.Nonce != "") &&
		!equalConstantTime(tokenNonce, credential.Nonce) {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}

	subject, _ := claims.GetSubject()
	return authAbstract.VerifiedIdentity{
		Provider:       entities.ProviderGoogle,
		ProviderUserID: subject,
		ClientID:       matchedAudience(claims, v.clientIDs),
		Email:          stringClaim(claims, "email"),
		EmailVerified:  boolClaim(claims, "email_verified"),
		FirstName:      stringClaim(claims, "given_name"),
		LastName:       stringClaim(claims, "family_name"),
	}, nil
}
