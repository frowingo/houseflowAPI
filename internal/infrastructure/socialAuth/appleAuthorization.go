package socialAuth

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"github.com/golang-jwt/jwt/v5"
)

const (
	appleBaseURL               = "https://appleid.apple.com"
	appleClientSecretLifetime  = 5 * time.Minute
	maxAppleResponseBodyBytes  = 64 << 10
	appleRefreshTokenTypeHint  = "refresh_token"
	appleAuthorizationCodeType = "authorization_code"
)

// AppleAuthorizationKeeper exchanges the one-time authorization code of a new
// Apple account for a refresh token and revokes it when the account is
// deleted, as App Store Review Guideline 5.1.1(v) requires. Requests to Apple
// are authenticated with a client secret: a short-lived ES256 JWT signed with
// the team's Sign in with Apple key.
type AppleAuthorizationKeeper struct {
	teamID     string
	keyID      string
	privateKey *ecdsa.PrivateKey
	cipher     *TokenCipher
	client     *http.Client
	baseURL    string
	now        func() time.Time
}

type AppleAuthorizationOption func(*AppleAuthorizationKeeper)

// WithAppleBaseURL points the keeper at another Apple endpoint; tests use it.
func WithAppleBaseURL(baseURL string) AppleAuthorizationOption {
	return func(keeper *AppleAuthorizationKeeper) { keeper.baseURL = strings.TrimRight(baseURL, "/") }
}

func NewAppleAuthorizationKeeper(
	teamID, keyID, privateKeyPEM string,
	cipher *TokenCipher,
	client *http.Client,
	options ...AppleAuthorizationOption,
) (*AppleAuthorizationKeeper, error) {
	privateKey, err := parseApplePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	keeper := &AppleAuthorizationKeeper{
		teamID:     teamID,
		keyID:      keyID,
		privateKey: privateKey,
		cipher:     cipher,
		client:     client,
		baseURL:    appleBaseURL,
		now:        time.Now,
	}
	for _, option := range options {
		option(keeper)
	}
	return keeper, nil
}

func parseApplePrivateKey(privateKeyPEM string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("apple private key is not PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple private key: %w", err)
	}
	privateKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apple private key is not an EC key")
	}
	return privateKey, nil
}

var _ authAbstract.SocialAuthorizationKeeper = (*AppleAuthorizationKeeper)(nil)

func (k *AppleAuthorizationKeeper) Provider() entities.AuthProvider { return entities.ProviderApple }

func (k *AppleAuthorizationKeeper) Keep(
	ctx context.Context,
	identity authAbstract.VerifiedIdentity,
	credential authAbstract.SocialCredential,
) (string, error) {
	if credential.AuthorizationCode == "" || identity.ClientID == "" {
		return "", invalidSocialToken()
	}
	clientSecret, err := k.clientSecret(identity.ClientID)
	if err != nil {
		return "", err
	}

	var response struct {
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	status, err := k.post(ctx, "/auth/token", url.Values{
		"client_id":     {identity.ClientID},
		"client_secret": {clientSecret},
		"code":          {credential.AuthorizationCode},
		"grant_type":    {appleAuthorizationCodeType},
	}, &response)
	if err != nil {
		return "", helpers.NewUnavailableError("auth.error.provider_unavailable", err)
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		// invalid_grant: the code is expired, used or was issued to someone else.
		return "", invalidSocialToken()
	}
	if status != http.StatusOK || response.RefreshToken == "" {
		return "", helpers.NewUnavailableError("auth.error.provider_unavailable", fmt.Errorf("apple token endpoint responded with status %d", status))
	}
	// The code must belong to the account the identity token proved. The
	// response comes straight from Apple over TLS, so its signature is not
	// checked again.
	if !sameAppleSubject(response.IDToken, identity.ProviderUserID) {
		return "", invalidSocialToken()
	}
	return k.cipher.Encrypt(response.RefreshToken)
}

func sameAppleSubject(idToken, subject string) bool {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(idToken, claims); err != nil {
		return false
	}
	tokenSubject, err := claims.GetSubject()
	return err == nil && tokenSubject != "" && tokenSubject == subject
}

// Revoke tells Apple to drop the authorization. Failures are logged here
// because account deletion goes ahead regardless.
func (k *AppleAuthorizationKeeper) Revoke(ctx context.Context, keptToken string, clientID string) error {
	err := k.revoke(ctx, keptToken, clientID)
	if err != nil {
		log.Printf("apple authorization revoke failed: %v", err)
	}
	return err
}

func (k *AppleAuthorizationKeeper) revoke(ctx context.Context, keptToken string, clientID string) error {
	if keptToken == "" || clientID == "" {
		return errors.New("no apple authorization kept")
	}
	refreshToken, err := k.cipher.Decrypt(keptToken)
	if err != nil {
		return fmt.Errorf("decrypt apple refresh token: %w", err)
	}
	clientSecret, err := k.clientSecret(clientID)
	if err != nil {
		return err
	}
	status, err := k.post(ctx, "/auth/revoke", url.Values{
		"client_id":       {clientID},
		"client_secret":   {clientSecret},
		"token":           {refreshToken},
		"token_type_hint": {appleRefreshTokenTypeHint},
	}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("apple revoke endpoint responded with status %d", status)
	}
	return nil
}

func (k *AppleAuthorizationKeeper) clientSecret(clientID string) (string, error) {
	now := k.now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer:    k.teamID,
		Subject:   clientID,
		Audience:  jwt.ClaimStrings{appleIssuer},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(appleClientSecretLifetime)),
	})
	token.Header["kid"] = k.keyID
	return token.SignedString(k.privateKey)
}

// post sends a form to Apple and decodes a successful JSON body into result.
func (k *AppleAuthorizationKeeper) post(ctx context.Context, path string, form url.Values, result any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, k.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := k.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	body := io.LimitReader(response.Body, maxAppleResponseBodyBytes)
	if response.StatusCode == http.StatusOK && result != nil {
		if err := json.NewDecoder(body).Decode(result); err != nil {
			return 0, fmt.Errorf("decode apple response: %w", err)
		}
	}
	return response.StatusCode, nil
}
