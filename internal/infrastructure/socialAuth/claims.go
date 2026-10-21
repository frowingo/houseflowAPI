package socialAuth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

func stringClaim(claims jwt.MapClaims, name string) string {
	value, _ := claims[name].(string)
	return strings.TrimSpace(value)
}

// boolClaim reads a boolean that some providers (Apple) send as "true"/"false".
func boolClaim(claims jwt.MapClaims, name string) bool {
	switch value := claims[name].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true")
	default:
		return false
	}
}

// matchedAudience returns the first of our client ids the token was issued for.
func matchedAudience(claims jwt.MapClaims, clientIDs []string) string {
	audiences, err := claims.GetAudience()
	if err != nil {
		return ""
	}
	for _, audience := range audiences {
		if slices.Contains(clientIDs, audience) {
			return audience
		}
	}
	return ""
}

func equalConstantTime(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
