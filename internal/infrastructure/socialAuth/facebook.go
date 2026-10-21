package socialAuth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

const (
	// FacebookLimitedLoginJWKSURL publishes the keys Facebook signs Limited
	// Login ID tokens with.
	FacebookLimitedLoginJWKSURL = "https://limited.facebook.com/.well-known/oauth/openid/jwks/"
	// DefaultFacebookGraphVersion is the Graph API version used unless
	// FACEBOOK_GRAPH_VERSION overrides it.
	DefaultFacebookGraphVersion = "v23.0"

	facebookGraphURL         = "https://graph.facebook.com"
	maxFacebookResponseBytes = 64 << 10
)

var facebookLimitedLoginIssuers = []string{"https://www.facebook.com", "https://limited.facebook.com"}

// FacebookVerifier checks both Facebook Login flavours:
//   - Classic login (Android, iOS with tracking allowed) gives an opaque access
//     token. It is checked with debug_token, which also proves it was issued to
//     our app, and the profile is read with appsecret_proof.
//   - Limited Login (iOS without tracking) gives an OpenID Connect ID token,
//     checked offline against Facebook's key set. The nonce the client passed
//     to the SDK comes back unchanged and is required.
//
// Facebook only shares a confirmed primary email, so a returned email is
// treated as verified. Users who signed up with a phone number or declined
// the email permission come back without one.
type FacebookVerifier struct {
	appID        string
	appSecret    string
	client       *http.Client
	graphURL     string
	graphVersion string
	limited      *IDTokenVerifier
}

type FacebookOption func(*facebookOptions)

type facebookOptions struct {
	graphURL       string
	graphVersion   string
	idTokenOptions []IDTokenOption
}

// WithFacebookGraphURL points the verifier at another Graph host; tests use it.
func WithFacebookGraphURL(graphURL string) FacebookOption {
	return func(options *facebookOptions) { options.graphURL = strings.TrimRight(graphURL, "/") }
}

func WithFacebookGraphVersion(version string) FacebookOption {
	return func(options *facebookOptions) {
		if version != "" {
			options.graphVersion = version
		}
	}
}

func WithFacebookIDTokenOptions(idTokenOptions ...IDTokenOption) FacebookOption {
	return func(options *facebookOptions) {
		options.idTokenOptions = append(options.idTokenOptions, idTokenOptions...)
	}
}

func NewFacebookVerifier(keys *JWKSCache, appID, appSecret string, client *http.Client, options ...FacebookOption) *FacebookVerifier {
	settings := facebookOptions{graphURL: facebookGraphURL, graphVersion: DefaultFacebookGraphVersion}
	for _, option := range options {
		option(&settings)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &FacebookVerifier{
		appID:        appID,
		appSecret:    appSecret,
		client:       client,
		graphURL:     settings.graphURL,
		graphVersion: settings.graphVersion,
		limited: NewIDTokenVerifier(keys, facebookLimitedLoginIssuers, []string{appID}, []string{"RS256"},
			settings.idTokenOptions...),
	}
}

var (
	_ authAbstract.SocialIdentityVerifier      = (*FacebookVerifier)(nil)
	_ authAbstract.DataDeletionRequestVerifier = (*FacebookVerifier)(nil)
)

func (v *FacebookVerifier) Provider() entities.AuthProvider { return entities.ProviderFacebook }

func (v *FacebookVerifier) Verify(ctx context.Context, credential authAbstract.SocialCredential) (authAbstract.VerifiedIdentity, error) {
	switch {
	case credential.IDToken != "":
		return v.verifyLimitedLogin(ctx, credential)
	case credential.AccessToken != "":
		return v.verifyAccessToken(ctx, credential.AccessToken)
	default:
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
}

func (v *FacebookVerifier) verifyLimitedLogin(ctx context.Context, credential authAbstract.SocialCredential) (authAbstract.VerifiedIdentity, error) {
	if credential.Nonce == "" {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
	claims, err := v.limited.Verify(ctx, credential.IDToken)
	if err != nil {
		return authAbstract.VerifiedIdentity{}, err
	}
	if !equalConstantTime(stringClaim(claims, "nonce"), credential.Nonce) {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
	subject, _ := claims.GetSubject()
	email := stringClaim(claims, "email")
	return authAbstract.VerifiedIdentity{
		Provider:       entities.ProviderFacebook,
		ProviderUserID: subject,
		ClientID:       v.appID,
		Email:          email,
		EmailVerified:  email != "",
		FirstName:      stringClaim(claims, "given_name"),
		LastName:       stringClaim(claims, "family_name"),
	}, nil
}

func (v *FacebookVerifier) verifyAccessToken(ctx context.Context, accessToken string) (authAbstract.VerifiedIdentity, error) {
	var debug struct {
		Data struct {
			AppID   string `json:"app_id"`
			IsValid bool   `json:"is_valid"`
			UserID  string `json:"user_id"`
		} `json:"data"`
	}
	if err := v.graphGet(ctx, "/debug_token", url.Values{
		"input_token":  {accessToken},
		"access_token": {v.appID + "|" + v.appSecret},
	}, &debug); err != nil {
		return authAbstract.VerifiedIdentity{}, err
	}
	// A valid token issued to another app must not sign anyone in here.
	if !debug.Data.IsValid || debug.Data.AppID != v.appID || debug.Data.UserID == "" {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}

	var profile struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := v.graphGet(ctx, "/me", url.Values{
		"fields":          {"id,email,first_name,last_name"},
		"access_token":    {accessToken},
		"appsecret_proof": {v.appSecretProof(accessToken)},
	}, &profile); err != nil {
		return authAbstract.VerifiedIdentity{}, err
	}
	if profile.ID != debug.Data.UserID {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
	email := strings.TrimSpace(profile.Email)
	return authAbstract.VerifiedIdentity{
		Provider:       entities.ProviderFacebook,
		ProviderUserID: profile.ID,
		ClientID:       v.appID,
		Email:          email,
		EmailVerified:  email != "",
		FirstName:      strings.TrimSpace(profile.FirstName),
		LastName:       strings.TrimSpace(profile.LastName),
	}, nil
}

func (v *FacebookVerifier) appSecretProof(accessToken string) string {
	mac := hmac.New(sha256.New, []byte(v.appSecret))
	mac.Write([]byte(accessToken))
	return hex.EncodeToString(mac.Sum(nil))
}

// graphGet calls the Graph API. Graph rejects bad or expired tokens with a 4xx
// status; those are invalid credentials. Anything else means Facebook is not
// answering properly.
func (v *FacebookVerifier) graphGet(ctx context.Context, path string, query url.Values, result any) error {
	endpoint := v.graphURL + "/" + v.graphVersion + path + "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := v.client.Do(request)
	if err != nil {
		// The URL holds tokens; keep them out of the error.
		var urlError *url.Error
		if errors.As(err, &urlError) {
			err = urlError.Err
		}
		return helpers.NewUnavailableError("auth.error.provider_unavailable", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests:
		return invalidSocialToken()
	default:
		return helpers.NewUnavailableError("auth.error.provider_unavailable",
			fmt.Errorf("facebook graph responded with status %d", response.StatusCode))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxFacebookResponseBytes)).Decode(result); err != nil {
		return helpers.NewUnavailableError("auth.error.provider_unavailable", err)
	}
	return nil
}

// VerifyDataDeletionRequest checks Facebook's signed_request: a base64url
// HMAC-SHA256 signature of the base64url JSON payload, keyed with the app
// secret. It returns the app-scoped user id the request is about.
func (v *FacebookVerifier) VerifyDataDeletionRequest(signedRequest string) (string, error) {
	encodedSignature, encodedPayload, ok := strings.Cut(strings.TrimSpace(signedRequest), ".")
	if !ok || encodedSignature == "" || encodedPayload == "" {
		return "", invalidSignedRequest()
	}
	signature, err := decodeFacebookBase64(encodedSignature)
	if err != nil {
		return "", invalidSignedRequest()
	}
	mac := hmac.New(sha256.New, []byte(v.appSecret))
	mac.Write([]byte(encodedPayload))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", invalidSignedRequest()
	}

	payloadJSON, err := decodeFacebookBase64(encodedPayload)
	if err != nil {
		return "", invalidSignedRequest()
	}
	var payload struct {
		Algorithm string `json:"algorithm"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return "", invalidSignedRequest()
	}
	if !strings.EqualFold(payload.Algorithm, "HMAC-SHA256") || payload.UserID == "" {
		return "", invalidSignedRequest()
	}
	return payload.UserID, nil
}

// decodeFacebookBase64 accepts base64url with or without padding.
func decodeFacebookBase64(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
}

func invalidSignedRequest() error {
	return helpers.NewUnauthorizedError("auth.error.invalid_signed_request")
}
