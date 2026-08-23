package tests

import (
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
