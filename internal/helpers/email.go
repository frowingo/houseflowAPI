package helpers

import (
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// NormalizeEmail returns the canonical form email addresses are stored with.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// EmailLookupFilter matches a user by the canonical email. Accounts created
// before normalization may be stored with their original casing, so the
// address as typed is matched too.
func EmailLookupFilter(email string) bson.M {
	normalized := NormalizeEmail(email)
	trimmed := strings.TrimSpace(email)
	if trimmed == normalized {
		return bson.M{"email": normalized}
	}
	return bson.M{"email": bson.M{"$in": bson.A{normalized, trimmed}}}
}
