package services

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	"houseflowApi/internal/config"
)

const MailSMTPHost = "smtp.gmail.com"
const MailSMTPPort = 587
const MailSMTPUsername = "houseflow37@gmail.com"
const MailFromAddress = "no-reply@houseflow.com"

// Mail is sent inside the request; a stalled SMTP server must not hold it.
const (
	smtpDialTimeout = 10 * time.Second
	smtpSendTimeout = 20 * time.Second
)

type NotificationService struct {
	smtpPassword string
}

type codeEmailTemplateData struct {
	Heading         string
	Intro           string
	CodeChars       []string
	ValidityMinutes int
	IgnoreMessage   string
}

func NewNotificationService(smtpPassword string) *NotificationService {
	return &NotificationService{smtpPassword: smtpPassword}
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
	if r.smtpPassword == "" {
		return fmt.Errorf("smtp password is missing")
	}

	smtpAddress := fmt.Sprintf("%s:%d", MailSMTPHost, MailSMTPPort)
	auth := smtp.PlainAuth("", MailSMTPUsername, r.smtpPassword, MailSMTPHost)

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

	return sendMail(smtpAddress, MailSMTPHost, auth, MailFromAddress, toEmail, message)
}

// sendMail does what smtp.SendMail does, on a connection with a deadline.
func sendMail(address, host string, auth smtp.Auth, from, to string, message []byte) error {
	conn, err := (&net.Dialer{Timeout: smtpDialTimeout}).Dial("tcp", address)
	if err != nil {
		return err
	}
	if err := conn.SetDeadline(time.Now().Add(smtpSendTimeout)); err != nil {
		conn.Close()
		return err
	}
	// NewClient closes conn itself when the server greeting fails.
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if ok, _ := client.Extension("AUTH"); !ok {
		return errors.New("smtp: server doesn't support AUTH")
	}
	if err := client.Auth(auth); err != nil {
		return err
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

var codeEmailTemplate = sync.OnceValues(func() (*template.Template, error) {
	templateContent, err := config.ReadStaticFile("code-email.html")
	if err != nil {
		return nil, fmt.Errorf("read code email template: %w", err)
	}
	tmpl, err := template.New("code-email").Parse(string(templateContent))
	if err != nil {
		return nil, fmt.Errorf("parse code email template: %w", err)
	}
	return tmpl, nil
})

func renderCodeEmailTemplate(data codeEmailTemplateData) (string, error) {
	tmpl, err := codeEmailTemplate()
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		return "", fmt.Errorf("render code email template: %w", err)
	}

	return body.String(), nil
}

var _ authAbstract.EmailSender = (*NotificationService)(nil)
