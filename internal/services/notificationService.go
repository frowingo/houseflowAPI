package services

import (
	"bytes"
	"fmt"
	"houseflowApi/internal/config"
	"html/template"
	"net/smtp"
	"strings"
)

const MailSMTPHost = "smtp.gmail.com"
const MailSMTPPort = 587
const MailSMTPUsername = "houseflow37@gmail.com"
const MailFromAddress = "no-reply@houseflow.com"

type NotificationService struct{}

type codeEmailTemplateData struct {
	Heading         string
	Intro           string
	CodeChars       []string
	ValidityMinutes int
	IgnoreMessage   string
}

func NewNotificationService() *NotificationService {
	return &NotificationService{}
}

func (r *NotificationService) SendResetCodeEmail(toEmail, code string, validityMinutes int) error {
	return r.sendCodeEmail(
		toEmail,
		code,
		validityMinutes,
		"Password Reset Code",
		"We received a request to reset your password. Use the code below to continue.",
		"If you did not request this password reset, you can safely ignore this email.",
	)
}

func (r *NotificationService) SendEmailVerificationCode(toEmail, code string, validityMinutes int) error {
	return r.sendCodeEmail(
		toEmail,
		code,
		validityMinutes,
		"Email Verification Code",
		"Use the code below to verify your email address and complete your HouseFlow registration.",
		"If you did not create a HouseFlow account, you can safely ignore this email.",
	)
}

func (r *NotificationService) sendCodeEmail(toEmail, code string, validityMinutes int, subject, intro, ignoreMessage string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	smtpPassword := cfg.Internal.SMTP.Password
	if smtpPassword == "" {
		return fmt.Errorf("smtp password is missing")
	}

	smtpAddress := fmt.Sprintf("%s:%d", MailSMTPHost, MailSMTPPort)
	auth := smtp.PlainAuth("", MailSMTPUsername, smtpPassword, MailSMTPHost)

	htmlBody, err := renderCodeEmailTemplate(codeEmailTemplateData{
		Heading:         subject,
		Intro:           intro,
		CodeChars:       strings.Split(code, ""),
		ValidityMinutes: validityMinutes,
		IgnoreMessage:   ignoreMessage,
	})
	if err != nil {
		return err
	}

	message := []byte(
		"From: " + MailFromAddress + "\r\n" +
			"To: " + toEmail + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/html; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: 8bit\r\n\r\n" +
			htmlBody + "\r\n",
	)

	return smtp.SendMail(smtpAddress, auth, MailFromAddress, []string{toEmail}, message)
}

func renderCodeEmailTemplate(data codeEmailTemplateData) (string, error) {
	templateContent, err := config.ReadStaticFile("code-email.html")
	if err != nil {
		return "", fmt.Errorf("read code email template: %w", err)
	}

	tmpl, err := template.New("code-email").Parse(string(templateContent))
	if err != nil {
		return "", fmt.Errorf("parse code email template: %w", err)
	}

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		return "", fmt.Errorf("render code email template: %w", err)
	}

	return body.String(), nil
}
