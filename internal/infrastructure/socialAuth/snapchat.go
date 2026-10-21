package socialAuth

import (
	"context"
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
	snapchatKitURL          = "https://kit.snapchat.com"
	snapchatMeQuery         = "{me{externalId displayName}}"
	maxSnapchatResponseSize = 64 << 10
)

// SnapchatVerifier checks a Login Kit access token by reading the user's
// externalId from the Login Kit "me" endpoint.
//
// Login Kit has no documented way to ask which app a token was issued to, but
// externalId is scoped to the app: a token issued to another app yields that
// app's id for the user, never the one stored for our app. Login Kit shares no
// email, so a Snapchat sign-up always goes through the email step.
type SnapchatVerifier struct {
	clientID string
	client   *http.Client
	kitURL   string
}

type SnapchatOption func(*SnapchatVerifier)

// WithSnapchatKitURL points the verifier at another Login Kit host; tests use it.
func WithSnapchatKitURL(kitURL string) SnapchatOption {
	return func(verifier *SnapchatVerifier) { verifier.kitURL = strings.TrimRight(kitURL, "/") }
}

func NewSnapchatVerifier(clientID string, client *http.Client, options ...SnapchatOption) *SnapchatVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	verifier := &SnapchatVerifier{clientID: clientID, client: client, kitURL: snapchatKitURL}
	for _, option := range options {
		option(verifier)
	}
	return verifier
}

var _ authAbstract.SocialIdentityVerifier = (*SnapchatVerifier)(nil)

func (v *SnapchatVerifier) Provider() entities.AuthProvider { return entities.ProviderSnapchat }

func (v *SnapchatVerifier) Verify(ctx context.Context, credential authAbstract.SocialCredential) (authAbstract.VerifiedIdentity, error) {
	if credential.AccessToken == "" {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
	endpoint := v.kitURL + "/v1/me?" + url.Values{"query": {snapchatMeQuery}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return authAbstract.VerifiedIdentity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	response, err := v.client.Do(request)
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			err = urlError.Err
		}
		return authAbstract.VerifiedIdentity{}, helpers.NewUnavailableError("auth.error.provider_unavailable", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests:
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	default:
		return authAbstract.VerifiedIdentity{}, helpers.NewUnavailableError("auth.error.provider_unavailable",
			fmt.Errorf("snapchat login kit responded with status %d", response.StatusCode))
	}

	var body struct {
		Data struct {
			Me struct {
				ExternalID  string `json:"externalId"`
				DisplayName string `json:"displayName"`
			} `json:"me"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxSnapchatResponseSize)).Decode(&body); err != nil {
		return authAbstract.VerifiedIdentity{}, helpers.NewUnavailableError("auth.error.provider_unavailable", err)
	}
	externalID := strings.TrimSpace(body.Data.Me.ExternalID)
	if externalID == "" {
		return authAbstract.VerifiedIdentity{}, invalidSocialToken()
	}
	return authAbstract.VerifiedIdentity{
		Provider:       entities.ProviderSnapchat,
		ProviderUserID: externalID,
		ClientID:       v.clientID,
		FirstName:      strings.TrimSpace(body.Data.Me.DisplayName),
	}, nil
}
