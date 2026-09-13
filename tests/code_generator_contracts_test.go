package tests

import (
	"strings"
	"testing"
	"time"

	"houseflowApi/internal/helpers"
)

func TestIsResetCodeValidAcceptsCurrentAndPreviousWindows(t *testing.T) {
	const (
		email           = "user@example.com"
		secret          = "test-secret"
		validityMinutes = 3
	)

	currentWindow := helpers.ResetCodeWindow(validityMinutes)
	currentCode := helpers.GenerateResetCode(email, secret, currentWindow)
	previousCode := helpers.GenerateResetCode(email, secret, currentWindow.Add(-validityMinutes*time.Minute))

	if !helpers.IsResetCodeValid(email, currentCode, secret, validityMinutes) {
		t.Fatal("current window code should be valid")
	}
	if !helpers.IsResetCodeValid(email, previousCode, secret, validityMinutes) {
		t.Fatal("previous window code should be valid")
	}
	if helpers.IsResetCodeValid(email, "INVALID", secret, validityMinutes) {
		t.Fatal("an unrelated code should be invalid")
	}
}

func TestInviteCodeGenerationAndDigestNormalization(t *testing.T) {
	code, err := helpers.GenerateInviteCode(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 {
		t.Fatalf("invite code length = %d, want 8", len(code))
	}
	for _, character := range code {
		if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", character) {
			t.Fatalf("invite code contains ambiguous or unsupported character %q", character)
		}
	}

	upperDigest := helpers.GenerateInviteCodeDigest(code, "join-secret")
	lowerDigest := helpers.GenerateInviteCodeDigest(strings.ToLower(code), "join-secret")
	if upperDigest != lowerDigest {
		t.Fatal("invite code digest should be case-insensitive")
	}
	if upperDigest == helpers.GenerateInviteCodeDigest(code, "different-secret") {
		t.Fatal("invite code digest should be bound to its secret")
	}
}
