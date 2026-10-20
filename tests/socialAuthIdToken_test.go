package tests

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/socialAuth"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIDTokenIssuer   = "https://issuer.example"
	testIDTokenAudience = "com.houseflow.app"
)

type testSigningKey struct {
	id      string
	method  jwt.SigningMethod
	private crypto.Signer
}

func newRSATestKey(t *testing.T, id string) testSigningKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return testSigningKey{id: id, method: jwt.SigningMethodRS256, private: key}
}

func newECTestKey(t *testing.T, id string) testSigningKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testSigningKey{id: id, method: jwt.SigningMethodES256, private: key}
}

func (k testSigningKey) jwk() map[string]string {
	encode := func(value *big.Int, size int) string {
		raw := value.Bytes()
		if size > len(raw) {
			raw = append(make([]byte, size-len(raw)), raw...)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	switch public := k.private.Public().(type) {
	case *rsa.PublicKey:
		return map[string]string{"kty": "RSA", "kid": k.id, "use": "sig", "alg": "RS256",
			"n": encode(public.N, 0), "e": encode(big.NewInt(int64(public.E)), 0)}
	case *ecdsa.PublicKey:
		return map[string]string{"kty": "EC", "kid": k.id, "use": "sig", "alg": "ES256", "crv": "P-256",
			"x": encode(public.X, 32), "y": encode(public.Y, 32)}
	}
	panic("unsupported test key")
}

func (k testSigningKey) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(k.method, claims)
	token.Header["kid"] = k.id
	signed, err := token.SignedString(k.private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

type testJWKSServer struct {
	*httptest.Server
	mutex    sync.Mutex
	keys     []testSigningKey
	failing  bool
	requests atomic.Int32
}

func newTestJWKSServer(t *testing.T, keys ...testSigningKey) *testJWKSServer {
	t.Helper()
	server := &testJWKSServer{keys: keys}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		server.requests.Add(1)
		server.mutex.Lock()
		defer server.mutex.Unlock()
		if server.failing {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		document := map[string][]map[string]string{"keys": {}}
		for _, key := range server.keys {
			document["keys"] = append(document["keys"], key.jwk())
		}
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *testJWKSServer) setKeys(keys ...testSigningKey) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.keys = keys
}

func (s *testJWKSServer) setFailing(failing bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.failing = failing
}

type testClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *testClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(duration)
}

func validIDTokenClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIDTokenIssuer, "aud": testIDTokenAudience, "sub": "provider-user-1",
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
	}
}

func newTestIDTokenVerifier(server *testJWKSServer, clock *testClock) *socialAuth.IDTokenVerifier {
	keys := socialAuth.NewJWKSCache(server.URL, server.Client(),
		socialAuth.WithJWKSClock(clock.Now),
		socialAuth.WithJWKSTTL(time.Hour),
		socialAuth.WithJWKSRefreshInterval(time.Minute),
	)
	return socialAuth.NewIDTokenVerifier(keys,
		[]string{testIDTokenIssuer, "issuer.example"},
		[]string{"other-client", testIDTokenAudience},
		[]string{"RS256", "ES256"},
		socialAuth.WithIDTokenClock(clock.Now),
	)
}

func assertInvalidSocialToken(t *testing.T, err error) {
	t.Helper()
	if !helpers.IsApplicationError(err, "auth.error.invalid_social_token") {
		t.Fatalf("err=%v, want auth.error.invalid_social_token", err)
	}
	var applicationError *helpers.ApplicationError
	if !errors.As(err, &applicationError) || applicationError.Kind != helpers.ErrorKindUnauthorized {
		t.Fatalf("err=%v, want unauthorized kind", err)
	}
}

func TestIDTokenVerifierAcceptsRSAAndECTokens(t *testing.T) {
	clock := &testClock{now: time.Now()}
	rsaKey, ecKey := newRSATestKey(t, "rsa-1"), newECTestKey(t, "ec-1")
	server := newTestJWKSServer(t, rsaKey, ecKey)
	verifier := newTestIDTokenVerifier(server, clock)

	for _, key := range []testSigningKey{rsaKey, ecKey} {
		claims, err := verifier.Verify(context.Background(), key.sign(t, validIDTokenClaims(clock.Now())))
		if err != nil {
			t.Fatalf("%s: %v", key.id, err)
		}
		if subject, _ := claims.GetSubject(); subject != "provider-user-1" {
			t.Fatalf("%s: subject=%q", key.id, subject)
		}
	}
	if requests := server.requests.Load(); requests != 1 {
		t.Fatalf("key set requests=%d, want 1 (cached)", requests)
	}
}

func TestIDTokenVerifierAcceptsAnyConfiguredAudienceAndIssuer(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "rsa-1")
	verifier := newTestIDTokenVerifier(newTestJWKSServer(t, key), clock)

	claims := validIDTokenClaims(clock.Now())
	claims["iss"] = "issuer.example"
	claims["aud"] = []string{"unrelated", "other-client"}
	if _, err := verifier.Verify(context.Background(), key.sign(t, claims)); err != nil {
		t.Fatal(err)
	}
}

func TestIDTokenVerifierRejectsInvalidTokens(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "rsa-1")
	stranger := newRSATestKey(t, "rsa-1")
	verifier := newTestIDTokenVerifier(newTestJWKSServer(t, key), clock)
	now := clock.Now()

	withClaim := func(name string, value any) string {
		claims := validIDTokenClaims(now)
		if value == nil {
			delete(claims, name)
		} else {
			claims[name] = value
		}
		return key.sign(t, claims)
	}
	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, validIDTokenClaims(now))
	hmacToken.Header["kid"] = "rsa-1"
	hmacSigned, err := hmacToken.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, validIDTokenClaims(now)).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	noKeyID := jwt.NewWithClaims(jwt.SigningMethodRS256, validIDTokenClaims(now))
	noKeyIDSigned, err := noKeyID.SignedString(key.private)
	if err != nil {
		t.Fatal(err)
	}

	testCases := map[string]string{
		"empty":                "",
		"garbage":              "not-a-token",
		"wrong issuer":         withClaim("iss", "https://evil.example"),
		"wrong audience":       withClaim("aud", "someone-else"),
		"missing audience":     withClaim("aud", nil),
		"expired":              withClaim("exp", now.Add(-2*time.Minute).Unix()),
		"missing expiry":       withClaim("exp", nil),
		"issued in the future": withClaim("iat", now.Add(5*time.Minute).Unix()),
		"missing subject":      withClaim("sub", nil),
		"forged signature":     stranger.sign(t, validIDTokenClaims(now)),
		"hmac algorithm":       hmacSigned,
		"none algorithm":       unsigned,
		"missing key id":       noKeyIDSigned,
	}
	for name, token := range testCases {
		t.Run(name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), token)
			assertInvalidSocialToken(t, err)
		})
	}
}

func TestIDTokenVerifierAllowsClockSkewWithinLeeway(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "rsa-1")
	verifier := newTestIDTokenVerifier(newTestJWKSServer(t, key), clock)

	claims := validIDTokenClaims(clock.Now())
	claims["exp"] = clock.Now().Add(-30 * time.Second).Unix()
	if _, err := verifier.Verify(context.Background(), key.sign(t, claims)); err != nil {
		t.Fatalf("token expired 30s ago rejected: %v", err)
	}
}

func TestJWKSCachePicksUpRotatedKeysWithThrottledRefresh(t *testing.T) {
	clock := &testClock{now: time.Now()}
	oldKey, newKey := newRSATestKey(t, "old"), newRSATestKey(t, "new")
	server := newTestJWKSServer(t, oldKey)
	verifier := newTestIDTokenVerifier(server, clock)

	if _, err := verifier.Verify(context.Background(), oldKey.sign(t, validIDTokenClaims(clock.Now()))); err != nil {
		t.Fatal(err)
	}

	// An unknown key id inside the refresh interval is rejected without a fetch.
	server.setKeys(oldKey, newKey)
	_, err := verifier.Verify(context.Background(), newKey.sign(t, validIDTokenClaims(clock.Now())))
	assertInvalidSocialToken(t, err)
	if requests := server.requests.Load(); requests != 1 {
		t.Fatalf("key set requests=%d, want 1 inside refresh interval", requests)
	}

	clock.Advance(2 * time.Minute)
	if _, err := verifier.Verify(context.Background(), newKey.sign(t, validIDTokenClaims(clock.Now()))); err != nil {
		t.Fatalf("rotated key rejected after refresh interval: %v", err)
	}
	if requests := server.requests.Load(); requests != 2 {
		t.Fatalf("key set requests=%d, want 2", requests)
	}
}

func TestJWKSCacheKeepsKeysWhenRefreshFails(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "rsa-1")
	server := newTestJWKSServer(t, key)
	verifier := newTestIDTokenVerifier(server, clock)

	if _, err := verifier.Verify(context.Background(), key.sign(t, validIDTokenClaims(clock.Now()))); err != nil {
		t.Fatal(err)
	}
	server.setFailing(true)
	clock.Advance(2 * time.Hour)
	if _, err := verifier.Verify(context.Background(), key.sign(t, validIDTokenClaims(clock.Now()))); err != nil {
		t.Fatalf("cached key rejected while provider is down: %v", err)
	}
}

func TestIDTokenVerifierReportsUnavailableKeySet(t *testing.T) {
	clock := &testClock{now: time.Now()}
	key := newRSATestKey(t, "rsa-1")
	server := newTestJWKSServer(t, key)
	server.setFailing(true)
	verifier := newTestIDTokenVerifier(server, clock)

	_, err := verifier.Verify(context.Background(), key.sign(t, validIDTokenClaims(clock.Now())))
	if !helpers.IsApplicationError(err, "auth.error.provider_unavailable") {
		t.Fatalf("err=%v, want auth.error.provider_unavailable", err)
	}

	// The first fetch never succeeded, so the next request tries again at once.
	server.setFailing(false)
	if _, err := verifier.Verify(context.Background(), key.sign(t, validIDTokenClaims(clock.Now()))); err != nil {
		t.Fatalf("verify after provider recovered: %v", err)
	}
}
