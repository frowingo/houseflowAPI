package socialAuth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

const (
	defaultJWKSTTL             = 6 * time.Hour
	defaultJWKSRefreshInterval = time.Minute
	maxJWKSBodyBytes           = 1 << 20
)

var (
	// ErrUnknownSigningKey means the token names a key the provider does not publish.
	ErrUnknownSigningKey = errors.New("unknown signing key")
	// ErrKeySetUnavailable means the key set could not be fetched and no copy is cached.
	ErrKeySetUnavailable = errors.New("provider key set unavailable")
)

// JWKSCache keeps a provider's published signing keys. Keys are fetched on
// first use and again after the TTL; a token signed with an unknown key id
// triggers an early refresh so key rotation is picked up, at most once per
// refresh interval so forged key ids cannot make us hammer the provider.
// When a refresh fails the previous keys stay in use.
type JWKSCache struct {
	url             string
	client          *http.Client
	ttl             time.Duration
	refreshInterval time.Duration
	now             func() time.Time

	mutex       sync.Mutex
	keys        map[string]crypto.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

type JWKSOption func(*JWKSCache)

func WithJWKSTTL(ttl time.Duration) JWKSOption {
	return func(cache *JWKSCache) { cache.ttl = ttl }
}

func WithJWKSRefreshInterval(interval time.Duration) JWKSOption {
	return func(cache *JWKSCache) { cache.refreshInterval = interval }
}

func WithJWKSClock(now func() time.Time) JWKSOption {
	return func(cache *JWKSCache) { cache.now = now }
}

func NewJWKSCache(url string, client *http.Client, options ...JWKSOption) *JWKSCache {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	cache := &JWKSCache{
		url:             url,
		client:          client,
		ttl:             defaultJWKSTTL,
		refreshInterval: defaultJWKSRefreshInterval,
		now:             time.Now,
	}
	for _, option := range options {
		option(cache)
	}
	return cache
}

// Key returns the public key published under keyID.
func (c *JWKSCache) Key(ctx context.Context, keyID string) (crypto.PublicKey, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := c.now()
	key, known := c.keys[keyID]
	expired := c.keys == nil || now.Sub(c.fetchedAt) >= c.ttl
	if known && !expired {
		return key, nil
	}
	if c.keys != nil && now.Sub(c.lastAttempt) < c.refreshInterval {
		if known {
			return key, nil
		}
		return nil, ErrUnknownSigningKey
	}

	previousAttempt := c.lastAttempt
	c.lastAttempt = now
	keys, err := c.fetch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// The caller gave up; that says nothing about the provider.
			c.lastAttempt = previousAttempt
		}
		if known {
			return key, nil
		}
		if c.keys == nil {
			return nil, fmt.Errorf("%w: %v", ErrKeySetUnavailable, err)
		}
		return nil, ErrUnknownSigningKey
	}
	c.keys = keys
	c.fetchedAt = now
	if key, known = keys[keyID]; known {
		return key, nil
	}
	return nil, ErrUnknownSigningKey
}

type jsonWebKey struct {
	KeyType string `json:"kty"`
	KeyID   string `json:"kid"`
	Use     string `json:"use"`
	N       string `json:"n"`
	E       string `json:"e"`
	Curve   string `json:"crv"`
	X       string `json:"x"`
	Y       string `json:"y"`
}

func (c *JWKSCache) fetch(ctx context.Context) (map[string]crypto.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("key set responded with status %d", response.StatusCode)
	}

	var document struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxJWKSBodyBytes)).Decode(&document); err != nil {
		return nil, err
	}

	keys := make(map[string]crypto.PublicKey, len(document.Keys))
	for _, webKey := range document.Keys {
		if webKey.KeyID == "" || (webKey.Use != "" && webKey.Use != "sig") {
			continue
		}
		key, err := parseJSONWebKey(webKey)
		if err != nil {
			// One malformed or unsupported key must not hide the others.
			continue
		}
		keys[webKey.KeyID] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("key set contains no usable signing key")
	}
	return keys, nil
}

func parseJSONWebKey(webKey jsonWebKey) (crypto.PublicKey, error) {
	switch webKey.KeyType {
	case "RSA":
		modulus, err := decodeBase64URLInt(webKey.N)
		if err != nil {
			return nil, err
		}
		exponent, err := decodeBase64URLInt(webKey.E)
		if err != nil {
			return nil, err
		}
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > 1<<31-1 {
			return nil, errors.New("rsa exponent out of range")
		}
		if modulus.BitLen() < 2048 {
			return nil, errors.New("rsa key shorter than 2048 bits")
		}
		return &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch webKey.Curve {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve %q", webKey.Curve)
		}
		x, err := decodeBase64URLInt(webKey.X)
		if err != nil {
			return nil, err
		}
		y, err := decodeBase64URLInt(webKey.Y)
		if err != nil {
			return nil, err
		}
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("ec point is not on the curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", webKey.KeyType)
	}
}

func decodeBase64URLInt(value string) (*big.Int, error) {
	if value == "" {
		return nil, errors.New("empty key parameter")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(raw), nil
}
