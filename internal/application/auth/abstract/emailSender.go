package abstract

// EmailSender is the outbound notification port used by authentication commands.
type EmailSender interface {
	SendResetCodeEmail(toEmail, code string, validityMinutes int) error
	SendEmailVerificationCode(toEmail, code string, validityMinutes int) error
}
