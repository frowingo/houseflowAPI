package helpers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const inviteCodeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const codeLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
const codeDigits = "0123456789"

func GenerateInviteCode(length int) (string, error) {
	result := make([]byte, length)
	charsetLength := big.NewInt(int64(len(inviteCodeChars)))

	for i := 0; i < length; i++ {
		randomIndex, err := rand.Int(rand.Reader, charsetLength)
		if err != nil {
			return "", err
		}
		result[i] = inviteCodeChars[randomIndex.Int64()]
	}

	return string(result), nil
}

func NormalizeInviteCode(code string) string {
	replacer := strings.NewReplacer("-", "", " ", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(code)))
}

func GenerateInviteCodeDigest(code, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("house-invite:v1|" + NormalizeInviteCode(code)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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

	letters := []byte(codeLetters)
	digits := []byte(codeDigits)

	result := make([]byte, 0, 6)
	for i := 0; i < 4; i++ {
		result = append(result, letters[int(sum[i])%len(letters)])
	}
	for i := 0; i < 2; i++ {
		result = append(result, digits[int(sum[4+i])%len(digits)])
	}

	// Shuffle deterministically so reset verification remains stable within the time window.
	for i := len(result) - 1; i > 0; i-- {
		j := int(sum[6+(len(result)-1-i)]) % (i + 1)
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}

func IsResetCodeValid(email, code, secret string, validityMinutes int) bool {
	currentWindow := ResetCodeWindow(validityMinutes)
	previousWindow := currentWindow.Add(-time.Duration(validityMinutes) * time.Minute)

	currentCode := GenerateResetCode(email, secret, currentWindow)
	previousCode := GenerateResetCode(email, secret, previousWindow)

	return code == currentCode || code == previousCode
}
