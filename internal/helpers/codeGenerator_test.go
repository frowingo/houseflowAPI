package helpers

import (
	"testing"
	"time"
)

func TestIsResetCodeValidAcceptsCurrentAndPreviousWindows(t *testing.T) {
	const (
		email           = "user@example.com"
		secret          = "test-secret"
		validityMinutes = 3
	)

	currentWindow := ResetCodeWindow(validityMinutes)
	currentCode := GenerateResetCode(email, secret, currentWindow)
	previousCode := GenerateResetCode(email, secret, currentWindow.Add(-validityMinutes*time.Minute))

	if !IsResetCodeValid(email, currentCode, secret, validityMinutes) {
		t.Fatal("current window code should be valid")
	}
	if !IsResetCodeValid(email, previousCode, secret, validityMinutes) {
		t.Fatal("previous window code should be valid")
	}
	if IsResetCodeValid(email, "INVALID", secret, validityMinutes) {
		t.Fatal("an unrelated code should be invalid")
	}
}
