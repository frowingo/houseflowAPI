package abstract

// EmailSender is the outbound notification port used by authentication commands.
// language is the recipient's language (helpers.NormalizeLanguage); an empty or
// unsupported one is sent in English.
type EmailSender interface {
	SendResetCodeEmail(toEmail, code string, validityMinutes int, language string) error
	SendEmailVerificationCode(toEmail, code string, validityMinutes int, language string) error
}
