package helpers

import (
	"crypto/hmac"
	"crypto/sha256"
	"time"

	"houseflowApi/internal/data/entities"

	"github.com/golang-jwt/jwt/v5"
)

const (
	SocialPendingTokenLifetime = 10 * time.Minute
	socialPendingTokenType     = "socialPendingSignUp"
)

// PendingSocialSignUp is a verified provider identity that still needs an
// email before an account can be created.
type PendingSocialSignUp struct {
	Provider       entities.AuthProvider
	ProviderUserID string
	ClientID       string
	FirstName      string
	LastName       string
}

type socialPendingClaims struct {
	Type      string `json:"typ"`
	Provider  int    `json:"provider"`
	ClientID  string `json:"clientId,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	jwt.RegisteredClaims
}

// SocialPendingTokenService signs the short-lived token that carries a
// verified identity between the social sign-in and the email completion. Its
// key is derived from the JWT secret but differs from it, so a pending token
// is never accepted where an access token is expected.
type SocialPendingTokenService struct {
	key []byte
	now func() time.Time
}

func NewSocialPendingTokenService(apiSecret string) *SocialPendingTokenService {
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte("social-pending"))
	return &SocialPendingTokenService{key: mac.Sum(nil), now: time.Now}
}

func (s *SocialPendingTokenService) Issue(pending PendingSocialSignUp) (string, error) {
	now := s.now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, socialPendingClaims{
		Type:      socialPendingTokenType,
		Provider:  int(pending.Provider),
		ClientID:  pending.ClientID,
		FirstName: pending.FirstName,
		LastName:  pending.LastName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   pending.ProviderUserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(SocialPendingTokenLifetime)),
		},
	})
	return token.SignedString(s.key)
}

// Parse returns the pending sign-up, or auth.error.social_signup_expired when
// the token is invalid or too old.
func (s *SocialPendingTokenService) Parse(rawToken string) (PendingSocialSignUp, error) {
	claims := &socialPendingClaims{}
	_, err := jwt.ParseWithClaims(rawToken, claims, func(*jwt.Token) (interface{}, error) {
		return s.key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithTimeFunc(s.now))
	if err != nil || claims.Type != socialPendingTokenType || claims.Subject == "" || claims.Provider <= 0 {
		return PendingSocialSignUp{}, NewUnauthorizedError("auth.error.social_signup_expired")
	}
	return PendingSocialSignUp{
		Provider:       entities.AuthProvider(claims.Provider),
		ProviderUserID: claims.Subject,
		ClientID:       claims.ClientID,
		FirstName:      claims.FirstName,
		LastName:       claims.LastName,
	}, nil
}
