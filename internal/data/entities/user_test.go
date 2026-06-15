package entities

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserJSONDoesNotExposePasswordHash(t *testing.T) {
	user := User{
		Email:        "user@example.com",
		HashPassword: "hashed-secret",
	}

	payload, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}

	body := string(payload)
	if strings.Contains(body, "hashed-secret") || strings.Contains(body, "password") {
		t.Fatalf("user json exposes password data: %s", body)
	}
}
