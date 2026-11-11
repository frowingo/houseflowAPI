package account

import (
	"sync"
	"testing"

	authCommands "houseflowApi/internal/application/auth/commands"
	authPolicies "houseflowApi/internal/application/auth/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
)

const invalidResetCode = "auth.error.invalid_or_expired_reset_code"

func requestResetCode(t *testing.T, fixture *appkit.ConcurrencyFixture, email string) string {
	t.Helper()
	if _, err := cqrs.Send[string](fixture.Ctx, fixture.Sender, authCommands.SignUpCommand{
		Email: email, Password: "initial-password", Firstname: "Code", Lastname: "User",
	}); err != nil {
		t.Fatal(err)
	}
	fixture.EmailSender.ResetCode = ""
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ForgotPasswordCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	if fixture.EmailSender.ResetCode == "" {
		t.Fatal("forgot password did not send a reset code")
	}
	return fixture.EmailSender.ResetCode
}

func resetPassword(fixture *appkit.ConcurrencyFixture, email, code string) error {
	_, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ResetPasswordCommand{
		Email: email, Code: code, NewPassword: "updated-password",
	})
	return err
}

func TestResetCodeIsRefusedAfterTooManyWrongAttempts(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "reset-attempts@example.com"
	code := requestResetCode(t, fixture, email)

	for attempt := 1; attempt <= authPolicies.MaxCodeAttempts; attempt++ {
		if err := resetPassword(fixture, email, "AAAA00"); !helpers.IsApplicationError(err, invalidResetCode) {
			t.Fatalf("wrong attempt %d err=%v", attempt, err)
		}
	}
	// The attempts are counted per email, whatever its casing.
	if err := resetPassword(fixture, "  Reset-Attempts@Example.com ", code); !helpers.IsApplicationError(err, invalidResetCode) {
		t.Fatalf("right code after the attempts ran out err=%v, want %s", err, invalidResetCode)
	}
	if _, err := cqrs.Send[string](fixture.Ctx, fixture.Sender, authCommands.LoginCommand{
		Email: email, Password: "updated-password",
	}); err == nil {
		t.Fatal("password changed although the attempts ran out")
	}
}

func TestResetCodeCannotBeReusedAfterReset(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "reset-reuse@example.com"
	code := requestResetCode(t, fixture, email)

	if err := resetPassword(fixture, email, code); err != nil {
		t.Fatal(err)
	}
	if err := resetPassword(fixture, email, code); !helpers.IsApplicationError(err, invalidResetCode) {
		t.Fatalf("reused code err=%v, want %s", err, invalidResetCode)
	}

	// Email verification mails the same code and keeps its own attempts.
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ValidateEmailCommand{
		Email: email, Code: code,
	}); err != nil {
		t.Fatalf("email verification after a reset err=%v", err)
	}
}

func TestEmailVerificationCodeIsRefusedAfterTooManyWrongAttempts(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "verify-attempts@example.com"
	requestResetCode(t, fixture, email)
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.SendEmailVerificationCodeCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	code := fixture.EmailSender.VerificationCode

	validate := func(code string) error {
		_, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ValidateEmailCommand{Email: email, Code: code})
		return err
	}
	for attempt := 1; attempt <= authPolicies.MaxCodeAttempts; attempt++ {
		if err := validate("AAAA00"); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
			t.Fatalf("wrong attempt %d err=%v", attempt, err)
		}
	}
	if err := validate(code); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
		t.Fatalf("right code after the attempts ran out err=%v", err)
	}
}

func TestSocialSignUpCodeIsRefusedAfterTooManyWrongAttempts(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)
	code := fixture.requestCode(t, pendingToken, "snap.attempts@example.com")

	for attempt := 1; attempt <= authPolicies.MaxCodeAttempts; attempt++ {
		if _, err := fixture.completeSignUp(pendingToken, "snap.attempts@example.com", "AAAA00"); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
			t.Fatalf("wrong attempt %d err=%v", attempt, err)
		}
	}
	if _, err := fixture.completeSignUp(pendingToken, "snap.attempts@example.com", code); !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code") {
		t.Fatalf("right code after the attempts ran out err=%v", err)
	}
	if users := fixture.count(t, "User", bson.M{}); users != 0 {
		t.Fatalf("users=%d, want 0", users)
	}
}

func TestParallelAttemptsCannotExceedTheLimit(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "verify-parallel@example.com"
	requestResetCode(t, fixture, email)
	if _, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.SendEmailVerificationCodeCommand{Email: email}); err != nil {
		t.Fatal(err)
	}
	code := fixture.EmailSender.VerificationCode

	// Every request carries the right code; only MaxCodeAttempts may be compared.
	const requests = 20
	results := make([]error, requests)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range results {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			_, results[i] = cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ValidateEmailCommand{Email: email, Code: code})
		}(i)
	}
	close(start)
	workers.Wait()

	accepted := 0
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case !helpers.IsApplicationError(err, "auth.error.invalid_or_expired_email_verification_code"):
			t.Fatalf("unexpected err=%v", err)
		}
	}
	if accepted == 0 || accepted > authPolicies.MaxCodeAttempts {
		t.Fatalf("accepted=%d of %d, want 1..%d", accepted, requests, authPolicies.MaxCodeAttempts)
	}
}
