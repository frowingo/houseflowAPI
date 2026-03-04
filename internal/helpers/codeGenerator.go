package helpers

import (
	"crypto/rand"
	"math/big"
)

const alphanumericChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateInviteCode generates a random alphanumeric code of specified length
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
