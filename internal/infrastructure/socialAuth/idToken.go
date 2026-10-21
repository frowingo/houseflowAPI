package socialAuth

import (
	"context"
	"errors"
	"slices"
	"time"

	"houseflowApi/internal/helpers"

	"github.com/golang-jwt/jwt/v5"
)

const defaultIDTokenLeeway = time.Minute

// IDTokenVerifier checks an OpenID Connect ID token (Google, Apple, Facebook
// Limited Login): signature against the provider's key set, allowed signing
// methods, issuer, audience and expiry. Provider-specific claims such as the
// nonce or email_verified are left to the caller.
type IDTokenVerifier struct {
	keys      *JWKSCache
	issuers   []string
	audiences []string
	methods   []string
	leeway    time.Duration
	now       func() time.Time
}

type IDTokenOption func(*IDTokenVerifier)

func WithIDTokenClock(now func() time.Time) IDTokenOption {
	return func(verifier *IDTokenVerifier) { verifier.now = now }
}

func NewIDTokenVerifier(keys *JWKSCache, issuers, audiences, methods []string, options ...IDTokenOption) *IDTokenVerifier {
	verifier := &IDTokenVerifier{
		keys:      keys,
		issuers:   issuers,
		audiences: audiences,
		methods:   methods,
		leeway:    defaultIDTokenLeeway,
		now:       time.Now,
	}
	for _, option := range options {
		option(verifier)
	}
	return verifier
}

// Verify returns the token claims. An invalid token yields an unauthorized
// application error; an unreachable key set yields an unavailable one so the
// client can retry.
func (v *IDTokenVerifier) Verify(ctx context.Context, rawToken string) (jwt.MapClaims, error) {
	claims, err := v.verifySigned(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return nil, invalidSocialToken()
	}
	return claims, nil
}

// verifySigned checks everything but the subject, for provider tokens that
// carry no top-level sub (Apple's server-to-server notifications).
func (v *IDTokenVerifier) verifySigned(ctx context.Context, rawToken string) (jwt.MapClaims, error) {
	if rawToken == "" {
		return nil, invalidSocialToken()
	}

	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (interface{}, error) {
		keyID, _ := token.Header["kid"].(string)
		if keyID == "" {
			return nil, ErrUnknownSigningKey
		}
		return v.keys.Key(ctx, keyID)
	},
		jwt.WithValidMethods(v.methods),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(v.leeway),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		if errors.Is(err, ErrKeySetUnavailable) {
			return nil, helpers.NewUnavailableError("auth.error.provider_unavailable", err)
		}
		return nil, invalidSocialToken()
	}

	issuer, err := claims.GetIssuer()
	if err != nil || !slices.Contains(v.issuers, issuer) {
		return nil, invalidSocialToken()
	}
	audiences, err := claims.GetAudience()
	if err != nil || !slices.ContainsFunc(audiences, func(audience string) bool {
		return slices.Contains(v.audiences, audience)
	}) {
		return nil, invalidSocialToken()
	}
	return claims, nil
}

func invalidSocialToken() error {
	return helpers.NewUnauthorizedError("auth.error.invalid_social_token")
}
