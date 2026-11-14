package account

import (
	"strconv"
	"sync"
	"testing"
	"time"

	authCommands "houseflowApi/internal/application/auth/commands"
	authPolicies "houseflowApi/internal/application/auth/policies"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/tests/testkit/appkit"

	"go.mongodb.org/mongo-driver/bson"
)

const (
	invalidResetCode        = "auth.error.invalid_or_expired_reset_code"
	invalidVerificationCode = "auth.error.invalid_or_expired_email_verification_code"
	tooManyCodeAttempts     = "auth.error.too_many_code_attempts"
)

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
	return resetPasswordTo(fixture, email, code, "updated-password")
}

func resetPasswordTo(fixture *appkit.ConcurrencyFixture, email, code, password string) error {
	_, err := cqrs.Send[cqrs.NoResult](fixture.Ctx, fixture.Sender, authCommands.ResetPasswordCommand{
		Email: email, Code: code, NewPassword: password,
	})
	return err
}

func login(fixture *appkit.ConcurrencyFixture, email, password string) error {
	_, err := cqrs.Send[string](fixture.Ctx, fixture.Sender, authCommands.LoginCommand{Email: email, Password: password})
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
	if err := resetPassword(fixture, "  Reset-Attempts@Example.com ", code); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("right code after the attempts ran out err=%v, want %s", err, tooManyCodeAttempts)
	}
	// Running out of attempts is not a consumed code: the current password
	// with the right code is refused like any other attempt.
	if err := resetPasswordTo(fixture, email, code, "initial-password"); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("right code and current password after the attempts ran out err=%v", err)
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
	if err := resetPasswordTo(fixture, email, code, "another-password"); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("reused code err=%v, want %s", err, tooManyCodeAttempts)
	}
	if err := login(fixture, email, "another-password"); err == nil {
		t.Fatal("a reused code changed the password")
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
		if err := validate("AAAA00"); !helpers.IsApplicationError(err, invalidVerificationCode) {
			t.Fatalf("wrong attempt %d err=%v", attempt, err)
		}
	}
	if err := validate(code); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("right code after the attempts ran out err=%v", err)
	}
}

func TestSocialSignUpCodeIsRefusedAfterTooManyWrongAttempts(t *testing.T) {
	fixture := newSocialAuthFixture(t)
	pendingToken := fixture.pendingSnapchatSignUp(t)
	code := fixture.requestCode(t, pendingToken, "snap.attempts@example.com")

	for attempt := 1; attempt <= authPolicies.MaxCodeAttempts; attempt++ {
		if _, err := fixture.completeSignUp(pendingToken, "snap.attempts@example.com", "AAAA00"); !helpers.IsApplicationError(err, invalidVerificationCode) {
			t.Fatalf("wrong attempt %d err=%v", attempt, err)
		}
	}
	if _, err := fixture.completeSignUp(pendingToken, "snap.attempts@example.com", code); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
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
		case !helpers.IsApplicationError(err, tooManyCodeAttempts):
			t.Fatalf("unexpected err=%v", err)
		}
	}
	if accepted == 0 || accepted > authPolicies.MaxCodeAttempts {
		t.Fatalf("accepted=%d of %d, want 1..%d", accepted, requests, authPolicies.MaxCodeAttempts)
	}
}

func TestRepeatedResetIsAcceptedWithoutReenablingTheCode(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "reset-repeat@example.com"
	code := requestResetCode(t, fixture, email)

	if err := resetPassword(fixture, email, code); err != nil {
		t.Fatal(err)
	}
	// The response was lost and the client sends the same request again.
	if err := resetPassword(fixture, email, code); err != nil {
		t.Fatalf("repeated reset err=%v", err)
	}
	if err := resetPasswordTo(fixture, email, code, "another-password"); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("consumed code with another password err=%v, want %s", err, tooManyCodeAttempts)
	}
	if err := resetPassword(fixture, email, "AAAA00"); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("wrong code with the applied password err=%v, want %s", err, tooManyCodeAttempts)
	}
	// The repeat changed nothing: the code stays consumed.
	if err := resetPasswordTo(fixture, email, code, "another-password"); !helpers.IsApplicationError(err, tooManyCodeAttempts) {
		t.Fatalf("code after a repeated reset err=%v", err)
	}
	if err := login(fixture, email, "updated-password"); err != nil {
		t.Fatalf("login with the reset password err=%v", err)
	}
	if err := login(fixture, email, "another-password"); err == nil {
		t.Fatal("a consumed code changed the password")
	}

	// Repeats are answered only a few times per window.
	refused := false
	for attempt := 0; attempt < 2*authPolicies.MaxCodeAttempts && !refused; attempt++ {
		refused = helpers.IsApplicationError(resetPassword(fixture, email, code), tooManyCodeAttempts)
	}
	if !refused {
		t.Fatal("repeats of a consumed code were never refused")
	}
}

func TestRepeatedResetWithPreviousWindowCode(t *testing.T) {
	fixture := appkit.NewConcurrencyFixture(t)
	email := "reset-repeat-previous@example.com"
	requestResetCode(t, fixture, email)

	// A reset applied in the previous window: its code and window are
	// consumed and the password changed.
	const validityMinutes = 5
	previous := helpers.ResetCodeWindow(validityMinutes).Add(-validityMinutes * time.Minute)
	code := helpers.GenerateResetCode(email, "test-reset-secret", previous)
	if _, err := fixture.DB.Collection("AuthCodeAttempt").InsertOne(fixture.Ctx, bson.M{
		"_id":   authPolicies.CodePurposeResetPassword + "|" + email + "|" + strconv.FormatInt(previous.Unix(), 10),
		"count": authPolicies.MaxCodeAttempts, "consumed": true, "expiresAt": time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := helpers.HashPassword("updated-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.DB.Collection("User").UpdateOne(fixture.Ctx, bson.M{"email": email}, bson.M{"$set": bson.M{"password": hash}}); err != nil {
		t.Fatal(err)
	}

	if err := resetPassword(fixture, email, code); err != nil {
		t.Fatalf("repeated reset with the previous window's code err=%v", err)
	}
	// An expired, consumed code is an invalid code, not too many attempts.
	if err := resetPasswordTo(fixture, email, code, "another-password"); !helpers.IsApplicationError(err, invalidResetCode) {
		t.Fatalf("consumed previous window code err=%v, want %s", err, invalidResetCode)
	}
}
