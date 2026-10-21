package tests

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/socialAuth"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testFacebookAppID     = "1234567890"
	testFacebookAppSecret = "facebook-app-secret"
	testFacebookToken     = "user-access-token"
)

type fakeGraphServer struct {
	*httptest.Server
	mutex       sync.Mutex
	debugStatus int
	debugAppID  string
	debugValid  bool
	debugUserID string
	meStatus    int
	me          map[string]string
	paths       []string
	proofs      []string
}

func newFakeGraphServer(t *testing.T) *fakeGraphServer {
	t.Helper()
	server := &fakeGraphServer{
		debugStatus: http.StatusOK, debugAppID: testFacebookAppID, debugValid: true, debugUserID: "fb-user-1",
		meStatus: http.StatusOK,
		me:       map[string]string{"id": "fb-user-1", "email": "Fb.User@Example.com", "first_name": "Fiona", "last_name": "Book"},
	}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mutex.Lock()
		defer server.mutex.Unlock()
		server.paths = append(server.paths, r.URL.Path)
		query := r.URL.Query()
		switch r.URL.Path {
		case "/v23.0/debug_token":
			if query.Get("access_token") != testFacebookAppID+"|"+testFacebookAppSecret || query.Get("input_token") != testFacebookToken {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(server.debugStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"app_id": server.debugAppID, "is_valid": server.debugValid, "user_id": server.debugUserID,
			}})
		case "/v23.0/me":
			server.proofs = append(server.proofs, query.Get("appsecret_proof"))
			if query.Get("access_token") != testFacebookToken {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(server.meStatus)
			_ = json.NewEncoder(w).Encode(server.me)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *fakeGraphServer) update(change func(*fakeGraphServer)) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	change(s)
}

func newTestFacebookVerifier(t *testing.T, graph *fakeGraphServer, jwks *testJWKSServer, clock *testClock) *socialAuth.FacebookVerifier {
	t.Helper()
	keys := newTestJWKSCache(jwks, clock)
	return socialAuth.NewFacebookVerifier(keys, testFacebookAppID, testFacebookAppSecret, graph.Client(),
		socialAuth.WithFacebookGraphURL(graph.URL),
		socialAuth.WithFacebookIDTokenOptions(socialAuth.WithIDTokenClock(clock.Now)))
}

func TestFacebookVerifierChecksClassicAccessToken(t *testing.T) {
	clock := &testClock{now: time.Now()}
	graph := newFakeGraphServer(t)
	verifier := newTestFacebookVerifier(t, graph, newTestJWKSServer(t, newRSATestKey(t, "fb-1")), clock)

	identity, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{AccessToken: testFacebookToken})
	if err != nil {
		t.Fatal(err)
	}
	want := authAbstract.VerifiedIdentity{
		Provider: entities.ProviderFacebook, ProviderUserID: "fb-user-1", ClientID: testFacebookAppID,
		Email: "Fb.User@Example.com", EmailVerified: true, FirstName: "Fiona", LastName: "Book",
	}
	if identity != want {
		t.Fatalf("identity=%+v, want %+v", identity, want)
	}
	mac := hmac.New(sha256.New, []byte(testFacebookAppSecret))
	mac.Write([]byte(testFacebookToken))
	if len(graph.proofs) != 1 || graph.proofs[0] != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("appsecret_proof=%v", graph.proofs)
	}
}

func TestFacebookVerifierWithoutEmail(t *testing.T) {
	clock := &testClock{now: time.Now()}
	graph := newFakeGraphServer(t)
	graph.update(func(s *fakeGraphServer) { s.me = map[string]string{"id": "fb-user-1", "first_name": "Phone"} })
	verifier := newTestFacebookVerifier(t, graph, newTestJWKSServer(t, newRSATestKey(t, "fb-1")), clock)

	identity, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{AccessToken: testFacebookToken})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Email != "" || identity.EmailVerified {
		t.Fatalf("identity=%+v, want no email", identity)
	}
}

func TestFacebookVerifierRejectsBadAccessTokens(t *testing.T) {
	clock := &testClock{now: time.Now()}
	jwks := newTestJWKSServer(t, newRSATestKey(t, "fb-1"))
	testCases := map[string]func(*fakeGraphServer){
		"token of another app":     func(s *fakeGraphServer) { s.debugAppID = "999" },
		"invalid token":            func(s *fakeGraphServer) { s.debugValid = false },
		"debug_token rejects":      func(s *fakeGraphServer) { s.debugStatus = http.StatusBadRequest },
		"profile of another user":  func(s *fakeGraphServer) { s.me["id"] = "fb-user-2" },
		"profile request rejected": func(s *fakeGraphServer) { s.meStatus = http.StatusUnauthorized },
	}
	for name, change := range testCases {
		t.Run(name, func(t *testing.T) {
			graph := newFakeGraphServer(t)
			graph.update(change)
			_, err := newTestFacebookVerifier(t, graph, jwks, clock).Verify(context.Background(),
				authAbstract.SocialCredential{AccessToken: testFacebookToken})
			assertInvalidSocialToken(t, err)
		})
	}

	graph := newFakeGraphServer(t)
	graph.update(func(s *fakeGraphServer) { s.debugStatus = http.StatusInternalServerError })
	_, err := newTestFacebookVerifier(t, graph, jwks, clock).Verify(context.Background(),
		authAbstract.SocialCredential{AccessToken: testFacebookToken})
	if !helpers.IsApplicationError(err, "auth.error.provider_unavailable") {
		t.Fatalf("graph outage err=%v, want auth.error.provider_unavailable", err)
	}

	_, err = newTestFacebookVerifier(t, graph, jwks, clock).Verify(context.Background(), authAbstract.SocialCredential{})
	assertInvalidSocialToken(t, err)
}

func limitedLoginClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://www.facebook.com", "aud": testFacebookAppID, "sub": "fb-limited-1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": "sdk-nonce",
		"email": "limited@example.com", "given_name": "Lim", "family_name": "Ited",
	}
}

func TestFacebookVerifierChecksLimitedLoginToken(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "fb-1")
	graph := newFakeGraphServer(t)
	verifier := newTestFacebookVerifier(t, graph, newTestJWKSServer(t, key), clock)

	identity, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{
		IDToken: key.sign(t, limitedLoginClaims(clock.Now())), Nonce: "sdk-nonce",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := authAbstract.VerifiedIdentity{
		Provider: entities.ProviderFacebook, ProviderUserID: "fb-limited-1", ClientID: testFacebookAppID,
		Email: "limited@example.com", EmailVerified: true, FirstName: "Lim", LastName: "Ited",
	}
	if identity != want {
		t.Fatalf("identity=%+v, want %+v", identity, want)
	}
	if len(graph.paths) != 0 {
		t.Fatalf("limited login called the graph API: %v", graph.paths)
	}

	otherApp := limitedLoginClaims(clock.Now())
	otherApp["aud"] = "999"
	for name, credential := range map[string]authAbstract.SocialCredential{
		"missing nonce":   {IDToken: key.sign(t, limitedLoginClaims(clock.Now()))},
		"wrong nonce":     {IDToken: key.sign(t, limitedLoginClaims(clock.Now())), Nonce: "other"},
		"token of an app": {IDToken: key.sign(t, otherApp), Nonce: "sdk-nonce"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), credential)
			assertInvalidSocialToken(t, err)
		})
	}
}

func facebookSignedRequest(t *testing.T, secret string, payload map[string]any, padded bool) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoding := base64.RawURLEncoding
	if padded {
		encoding = base64.URLEncoding
	}
	encodedPayload := encoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encodedPayload))
	return encoding.EncodeToString(mac.Sum(nil)) + "." + encodedPayload
}

func TestFacebookSignedRequestVerification(t *testing.T) {
	clock := &testClock{now: time.Now()}
	verifier := newTestFacebookVerifier(t, newFakeGraphServer(t), newTestJWKSServer(t, newRSATestKey(t, "fb-1")), clock)
	payload := map[string]any{"algorithm": "HMAC-SHA256", "user_id": "fb-user-1", "issued_at": time.Now().Unix()}

	for _, padded := range []bool{false, true} {
		userID, err := verifier.VerifyDataDeletionRequest(facebookSignedRequest(t, testFacebookAppSecret, payload, padded))
		if err != nil || userID != "fb-user-1" {
			t.Fatalf("padded=%v: user=%q err=%v", padded, userID, err)
		}
	}

	valid := facebookSignedRequest(t, testFacebookAppSecret, payload, false)
	testCases := map[string]string{
		"empty":           "",
		"no separator":    "abc",
		"other secret":    facebookSignedRequest(t, "other-secret", payload, false),
		"wrong algorithm": facebookSignedRequest(t, testFacebookAppSecret, map[string]any{"algorithm": "HMAC-SHA1", "user_id": "x"}, false),
		"no user":         facebookSignedRequest(t, testFacebookAppSecret, map[string]any{"algorithm": "HMAC-SHA256"}, false),
		"tampered":        valid[:len(valid)-2] + "AA",
	}
	for name, signedRequest := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.VerifyDataDeletionRequest(signedRequest); !helpers.IsApplicationError(err, "auth.error.invalid_signed_request") {
				t.Fatalf("err=%v, want auth.error.invalid_signed_request", err)
			}
		})
	}
}
