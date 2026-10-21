package tests

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	testGoogleClientID = "ios.apps.googleusercontent.com"
	testAppleClientID  = "com.houseflow.app"
)

func newTestJWKSCache(server *testJWKSServer, clock *testClock) *socialAuth.JWKSCache {
	return socialAuth.NewJWKSCache(server.URL, server.Client(), socialAuth.WithJWKSClock(clock.Now))
}

func googleClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": testGoogleClientID, "sub": "google-sub-1",
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
		"email": "Person@Gmail.com", "email_verified": true, "given_name": "Ada", "family_name": "Lovelace",
	}
}

func appleClaims(now time.Time, rawNonce string) jwt.MapClaims {
	digest := sha256.Sum256([]byte(rawNonce))
	return jwt.MapClaims{
		"iss": "https://appleid.apple.com", "aud": testAppleClientID, "sub": "apple-sub-1",
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
		"nonce": hex.EncodeToString(digest[:]), "email": "abc123@privaterelay.appleid.com",
		"email_verified": "true", "is_private_email": "true",
	}
}

func TestGoogleVerifierReturnsIdentityFromToken(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "google-1")
	verifier := socialAuth.NewGoogleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{"web.apps.googleusercontent.com", testGoogleClientID}, socialAuth.WithIDTokenClock(clock.Now))

	identity, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{IDToken: key.sign(t, googleClaims(clock.Now()))})
	if err != nil {
		t.Fatal(err)
	}
	want := authAbstract.VerifiedIdentity{
		Provider: entities.ProviderGoogle, ProviderUserID: "google-sub-1", ClientID: testGoogleClientID,
		Email: "Person@Gmail.com", EmailVerified: true, FirstName: "Ada", LastName: "Lovelace",
	}
	if identity != want {
		t.Fatalf("identity=%+v, want %+v", identity, want)
	}
}

func TestGoogleVerifierChecksNonceWhenPresent(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "google-1")
	verifier := socialAuth.NewGoogleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{testGoogleClientID}, socialAuth.WithIDTokenClock(clock.Now))

	withNonce := googleClaims(clock.Now())
	withNonce["nonce"] = "nonce-1"
	testCases := []struct {
		name    string
		token   string
		nonce   string
		wantErr bool
	}{
		{"matching nonce", key.sign(t, withNonce), "nonce-1", false},
		{"different nonce", key.sign(t, withNonce), "nonce-2", true},
		{"token nonce but none sent", key.sign(t, withNonce), "", true},
		{"nonce sent but token has none", key.sign(t, googleClaims(clock.Now())), "nonce-1", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{IDToken: testCase.token, Nonce: testCase.nonce})
			if testCase.wantErr {
				assertInvalidSocialToken(t, err)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGoogleVerifierRejectsAppleIssuer(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "google-1")
	verifier := socialAuth.NewGoogleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{testGoogleClientID}, socialAuth.WithIDTokenClock(clock.Now))
	claims := googleClaims(clock.Now())
	claims["iss"] = "https://appleid.apple.com"
	_, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{IDToken: key.sign(t, claims)})
	assertInvalidSocialToken(t, err)
}

func TestAppleVerifierRequiresHashedNonce(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "apple-1")
	verifier := socialAuth.NewAppleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{testAppleClientID, "com.houseflow.services"}, socialAuth.WithIDTokenClock(clock.Now))
	token := key.sign(t, appleClaims(clock.Now(), "raw-nonce"))

	identity, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{IDToken: token, Nonce: "raw-nonce"})
	if err != nil {
		t.Fatal(err)
	}
	want := authAbstract.VerifiedIdentity{
		Provider: entities.ProviderApple, ProviderUserID: "apple-sub-1", ClientID: testAppleClientID,
		Email: "abc123@privaterelay.appleid.com", EmailVerified: true, IsPrivateRelay: true,
	}
	if identity != want {
		t.Fatalf("identity=%+v, want %+v", identity, want)
	}

	unhashed := appleClaims(clock.Now(), "raw-nonce")
	unhashed["nonce"] = "raw-nonce"
	for name, credential := range map[string]authAbstract.SocialCredential{
		"missing nonce":        {IDToken: token},
		"wrong nonce":          {IDToken: token, Nonce: "other-nonce"},
		"hashed nonce sent":    {IDToken: token, Nonce: appleClaims(clock.Now(), "raw-nonce")["nonce"].(string)},
		"unhashed token nonce": {IDToken: key.sign(t, unhashed), Nonce: "raw-nonce"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), credential)
			assertInvalidSocialToken(t, err)
		})
	}
}

func TestAppleVerifierReadsBooleanClaims(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "apple-1")
	verifier := socialAuth.NewAppleVerifier(newTestJWKSCache(newTestJWKSServer(t, key), clock),
		[]string{testAppleClientID}, socialAuth.WithIDTokenClock(clock.Now))

	claims := appleClaims(clock.Now(), "n")
	claims["email"] = "person@icloud.com"
	claims["email_verified"] = "false"
	claims["is_private_email"] = false
	identity, err := verifier.Verify(context.Background(), authAbstract.SocialCredential{IDToken: key.sign(t, claims), Nonce: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if identity.EmailVerified || identity.IsPrivateRelay {
		t.Fatalf("identity=%+v, want unverified non-relay email", identity)
	}
}

func newTestTokenCipher(t *testing.T) *socialAuth.TokenCipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := socialAuth.NewTokenCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestTokenCipherRoundTripAndTamperDetection(t *testing.T) {
	cipher := newTestTokenCipher(t)
	encrypted, err := cipher.Encrypt("refresh-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if decrypted, err := cipher.Decrypt(encrypted); err != nil || decrypted != "refresh-token-value" {
		t.Fatalf("decrypted=%q err=%v", decrypted, err)
	}
	if again, _ := cipher.Encrypt("refresh-token-value"); again == encrypted {
		t.Fatal("encryption is deterministic")
	}

	tampered := []byte(encrypted)
	tampered[len(tampered)-2] ^= 1
	if _, err := cipher.Decrypt(string(tampered)); err == nil {
		t.Fatal("tampered token decrypted")
	}
	if _, err := newTestTokenCipher(t).Decrypt(encrypted); err == nil {
		t.Fatal("token decrypted with another key")
	}
	if _, err := socialAuth.NewTokenCipher(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("16 byte key accepted")
	}
}

type fakeAppleServer struct {
	*httptest.Server
	mutex        sync.Mutex
	tokenStatus  int
	tokenSubject string
	requests     []url.Values
	paths        []string
}

func newFakeAppleServer(t *testing.T, signingKey *ecdsa.PrivateKey) *fakeAppleServer {
	t.Helper()
	server := &fakeAppleServer{tokenStatus: http.StatusOK, tokenSubject: "apple-sub-1"}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		server.mutex.Lock()
		server.requests = append(server.requests, r.PostForm)
		server.paths = append(server.paths, r.URL.Path)
		status, subject := server.tokenStatus, server.tokenSubject
		server.mutex.Unlock()

		assertAppleClientSecret(t, r.PostForm.Get("client_secret"), r.PostForm.Get("client_id"), signingKey)
		switch r.URL.Path {
		case "/auth/token":
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			idToken, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": subject}).SignedString(jwt.UnsafeAllowNoneSignatureType)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "access", "refresh_token": "apple-refresh-token", "id_token": idToken,
			})
		case "/auth/revoke":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func assertAppleClientSecret(t *testing.T, secret, clientID string, signingKey *ecdsa.PrivateKey) {
	t.Helper()
	claims := jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(secret, &claims, func(*jwt.Token) (interface{}, error) {
		return &signingKey.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience("https://appleid.apple.com"),
		jwt.WithIssuer("TEAM123"), jwt.WithSubject(clientID), jwt.WithExpirationRequired())
	if err != nil {
		t.Errorf("client secret invalid: %v", err)
		return
	}
	if token.Header["kid"] != "KEY123" {
		t.Errorf("client secret kid=%v, want KEY123", token.Header["kid"])
	}
}

func newApplePrivateKeyPEM(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func newTestAppleKeeper(t *testing.T) (*socialAuth.AppleAuthorizationKeeper, *fakeAppleServer, *socialAuth.TokenCipher) {
	t.Helper()
	signingKey, keyPEM := newApplePrivateKeyPEM(t)
	server := newFakeAppleServer(t, signingKey)
	cipher := newTestTokenCipher(t)
	keeper, err := socialAuth.NewAppleAuthorizationKeeper("TEAM123", "KEY123", keyPEM, cipher, server.Client(),
		socialAuth.WithAppleBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return keeper, server, cipher
}

var testAppleIdentity = authAbstract.VerifiedIdentity{
	Provider: entities.ProviderApple, ProviderUserID: "apple-sub-1", ClientID: testAppleClientID,
}

func TestAppleKeeperExchangesAndRevokesAuthorization(t *testing.T) {
	keeper, server, cipher := newTestAppleKeeper(t)

	kept, err := keeper.Keep(context.Background(), testAppleIdentity, authAbstract.SocialCredential{AuthorizationCode: "code-1"})
	if err != nil {
		t.Fatal(err)
	}
	if refreshToken, err := cipher.Decrypt(kept); err != nil || refreshToken != "apple-refresh-token" {
		t.Fatalf("kept token decrypts to %q (err %v)", refreshToken, err)
	}
	exchange := server.requests[0]
	if server.paths[0] != "/auth/token" || exchange.Get("code") != "code-1" || exchange.Get("grant_type") != "authorization_code" ||
		exchange.Get("client_id") != testAppleClientID {
		t.Fatalf("exchange request %s %v", server.paths[0], exchange)
	}

	if err := keeper.Revoke(context.Background(), kept, testAppleClientID); err != nil {
		t.Fatal(err)
	}
	revoke := server.requests[1]
	if server.paths[1] != "/auth/revoke" || revoke.Get("token") != "apple-refresh-token" ||
		revoke.Get("token_type_hint") != "refresh_token" || revoke.Get("client_id") != testAppleClientID {
		t.Fatalf("revoke request %s %v", server.paths[1], revoke)
	}
}

func TestAppleKeeperRejectsBadExchanges(t *testing.T) {
	keeper, server, _ := newTestAppleKeeper(t)
	ctx := context.Background()

	if _, err := keeper.Keep(ctx, testAppleIdentity, authAbstract.SocialCredential{}); !helpers.IsApplicationError(err, "auth.error.invalid_social_token") {
		t.Fatalf("missing code err=%v", err)
	}

	server.tokenSubject = "someone-else"
	_, err := keeper.Keep(ctx, testAppleIdentity, authAbstract.SocialCredential{AuthorizationCode: "code"})
	assertInvalidSocialToken(t, err)

	server.tokenSubject = "apple-sub-1"
	server.tokenStatus = http.StatusBadRequest
	_, err = keeper.Keep(ctx, testAppleIdentity, authAbstract.SocialCredential{AuthorizationCode: "used-code"})
	assertInvalidSocialToken(t, err)

	server.tokenStatus = http.StatusInternalServerError
	if _, err := keeper.Keep(ctx, testAppleIdentity, authAbstract.SocialCredential{AuthorizationCode: "code"}); !helpers.IsApplicationError(err, "auth.error.provider_unavailable") {
		t.Fatalf("apple outage err=%v, want auth.error.provider_unavailable", err)
	}

	if err := keeper.Revoke(ctx, "v1.not-a-token", testAppleClientID); err == nil {
		t.Fatal("revoke with a corrupt kept token succeeded")
	}
}

func TestAppleKeeperRejectsInvalidPrivateKey(t *testing.T) {
	cipher := newTestTokenCipher(t)
	if _, err := socialAuth.NewAppleAuthorizationKeeper("TEAM", "KEY", "not a pem", cipher, nil); err == nil {
		t.Fatal("invalid private key accepted")
	}
}
