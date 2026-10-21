package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/socialAuth"
)

func newFakeSnapchatServer(t *testing.T, status int, me map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me" || r.URL.Query().Get("query") != "{me{externalId displayName}}" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer snap-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": me}})
	}))
	t.Cleanup(server.Close)
	return server
}

func verifySnapchat(t *testing.T, server *httptest.Server, accessToken string) (authAbstract.VerifiedIdentity, error) {
	t.Helper()
	verifier := socialAuth.NewSnapchatVerifier("snap-client", server.Client(), socialAuth.WithSnapchatKitURL(server.URL))
	return verifier.Verify(context.Background(), authAbstract.SocialCredential{AccessToken: accessToken})
}

func TestSnapchatVerifierReadsExternalID(t *testing.T) {
	server := newFakeSnapchatServer(t, http.StatusOK, map[string]string{"externalId": "CAESIexternal", "displayName": " Snappy "})
	identity, err := verifySnapchat(t, server, "snap-token")
	if err != nil {
		t.Fatal(err)
	}
	want := authAbstract.VerifiedIdentity{
		Provider: entities.ProviderSnapchat, ProviderUserID: "CAESIexternal", ClientID: "snap-client", FirstName: "Snappy",
	}
	if identity != want {
		t.Fatalf("identity=%+v, want %+v", identity, want)
	}
}

func TestSnapchatVerifierRejectsBadTokens(t *testing.T) {
	ok := newFakeSnapchatServer(t, http.StatusOK, map[string]string{"externalId": "CAESIexternal"})
	if _, err := verifySnapchat(t, ok, ""); !helpers.IsApplicationError(err, "auth.error.invalid_social_token") {
		t.Fatalf("missing token err=%v", err)
	}
	if _, err := verifySnapchat(t, ok, "expired-token"); !helpers.IsApplicationError(err, "auth.error.invalid_social_token") {
		t.Fatalf("rejected token err=%v", err)
	}
	noID := newFakeSnapchatServer(t, http.StatusOK, map[string]string{"displayName": "No Id"})
	if _, err := verifySnapchat(t, noID, "snap-token"); !helpers.IsApplicationError(err, "auth.error.invalid_social_token") {
		t.Fatalf("missing externalId err=%v", err)
	}
	down := newFakeSnapchatServer(t, http.StatusBadGateway, nil)
	if _, err := verifySnapchat(t, down, "snap-token"); !helpers.IsApplicationError(err, "auth.error.provider_unavailable") {
		t.Fatalf("outage err=%v, want auth.error.provider_unavailable", err)
	}
}
