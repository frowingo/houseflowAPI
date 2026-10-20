package socialAuth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

const tokenCipherVersion = "v1."

// TokenCipher encrypts provider tokens kept at rest with AES-256-GCM. The
// output is versioned so the key can be rotated later.
type TokenCipher struct {
	aead cipher.AEAD
}

// NewTokenCipher takes a base64 encoded 32 byte key.
func NewTokenCipher(encodedKey string) (*TokenCipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("token encryption key must be a base64 encoded 32 byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TokenCipher{aead: aead}, nil
}

func (c *TokenCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return tokenCipherVersion + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *TokenCipher) Decrypt(encrypted string) (string, error) {
	encoded, ok := strings.CutPrefix(encrypted, tokenCipherVersion)
	if !ok {
		return "", errors.New("unknown token cipher version")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	nonceSize := c.aead.NonceSize()
	if len(sealed) < nonceSize {
		return "", errors.New("encrypted token is too short")
	}
	plaintext, err := c.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
