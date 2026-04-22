package helpers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"
)

const alphanumericChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GenerateInviteCode(length int) (string, error) {
	result := make([]byte, length)
	charsetLength := big.NewInt(int64(len(alphanumericChars)))

	for i := 0; i < length; i++ {
		randomIndex, err := rand.Int(rand.Reader, charsetLength)
		if err != nil {
			return "", err
		}
		result[i] = alphanumericChars[randomIndex.Int64()]
	}

	return string(result), nil
}

func ResetCodeWindow(validityMinutes int) time.Time {
	now := time.Now().UTC()
	windowSeconds := int64(validityMinutes) * 60
	windowStart := (now.Unix() / windowSeconds) * windowSeconds
	return time.Unix(windowStart, 0).UTC()
}

func GenerateResetCode(email, secret string, window time.Time) string {
	payload := fmt.Sprintf("%s|%d", email, window.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sum := mac.Sum(nil)

	charset := []byte(alphanumericChars)
	result := make([]byte, 6)
	for i := 0; i < 6; i++ {
		result[i] = charset[int(sum[i])%len(charset)]
	}
	return string(result)
}
