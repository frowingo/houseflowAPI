package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	authAbstract "houseflowApi/internal/application/auth/abstract"
	jobsAbstract "houseflowApi/internal/application/jobs/abstract"
	"houseflowApi/internal/application/notification"
	notificationAbstract "houseflowApi/internal/application/notification/abstract"
	"houseflowApi/internal/config"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
)

const MailFromName = "FausCrew"

// A stalled SMTP server must not hold the request or the mail job.
const (
	smtpDialTimeout = 10 * time.Second
	smtpSendTimeout = 20 * time.Second
)

const notificationRetention = 30 * 24 * time.Hour

// NotificationEvents tells the recipients' open app sessions that a
// notification arrived.
type NotificationEvents interface {
	NotificationCreated(ctx context.Context, houseID string, userIDs []string)
}

// NotificationDelivery holds what Notify needs. Push delivery itself runs in
// background jobs (notification.Delivery).
type NotificationDelivery struct {
	Notifications databaseAbstract.DbRepository[entities.Notification]
	Houses        databaseAbstract.DbRepository[entities.House]
	Events        NotificationEvents
	Jobs          jobsAbstract.JobQueue
}

type NotificationService struct {
	smtp     config.ConfigSMTP
	delivery NotificationDelivery
	// localizer renders the code emails in the recipient's language; without
	// one they are sent in English.
	localizer helpers.MessageLocalizer
}

type NotificationServiceOption func(*NotificationService)

// WithEmailLocalizer renders the code emails with the localization messages.
func WithEmailLocalizer(localizer helpers.MessageLocalizer) NotificationServiceOption {
	return func(service *NotificationService) { service.localizer = localizer }
}

func NewNotificationService(smtpConfig config.ConfigSMTP, delivery NotificationDelivery, options ...NotificationServiceOption) *NotificationService {
	service := &NotificationService{smtp: smtpConfig, delivery: delivery}
	for _, option := range options {
		option(service)
	}
	return service
}

// Notify stores a house notification, signals the recipients' open app
// sessions, and queues the push notifications to their devices. Only the
// storage can fail the call; the signal and the push are best effort.
func (r *NotificationService) Notify(ctx context.Context, request notificationAbstract.NotificationRequest) (*entities.Notification, error) {
	if r.delivery.Notifications == nil {
		return nil, errors.New("notification delivery is not configured")
	}
	recipients := request.RecipientUserIDs
	if len(recipients) == 0 {
		houseID, err := helpers.ToMongoId(request.HouseID)
		if err != nil {
			return nil, helpers.NewLocalizedError("house.error.invalid_house_id")
		}
		house, err := r.delivery.Houses.FindByID(ctx, houseID)
		if err != nil {
			return nil, err
		}
		recipients = house.MemberIds
	}
	recipients = withoutUser(recipients, request.SenderUserID)

	now := time.Now().UTC()
	entity := entities.Notification{
		Type:             request.Type,
		HouseId:          request.HouseID,
		SenderUserId:     request.SenderUserID,
		RecipientUserIds: request.RecipientUserIDs,
		Title:            request.Title,
		Message:          request.Message,
		Localized:        request.Localized,
		Args:             notification.SanitizeArgs(request.Args),
		CreatedAt:        now,
		ExpireAt:         now.Add(notificationRetention),
	}
	if request.ChoreID != "" {
		choreID := request.ChoreID
		entity.ChoreId = &choreID
	}
	created, err := r.delivery.Notifications.Insert(ctx, entity)
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return created, nil
	}

	if r.delivery.Events != nil {
		r.delivery.Events.NotificationCreated(ctx, created.HouseId, recipients)
	}
	if r.delivery.Jobs != nil {
		// The request may end right after this; the job must still be queued.
		if err := notification.EnqueueFanout(context.WithoutCancel(ctx), r.delivery.Jobs, created.Id.Hex(), recipients); err != nil {
			log.Printf("push notifications for %s were not queued: %v", created.Id.Hex(), err)
		}
	}
	return created, nil
}

func withoutUser(userIDs []string, excludedUserID string) []string {
	result := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID != "" && userID != excludedUserID {
			result = append(result, userID)
		}
	}
	return result
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

// Code emails carry the password reset and email verification codes. They
// are rendered from code-email.html in the recipient's language, with texts
// from the localization messages (keys below), and sent with their images
// inline.

// CodeEmailKind selects the texts of a code email.
type CodeEmailKind string

const (
	CodeEmailResetCode        CodeEmailKind = "reset_code"
	CodeEmailVerificationCode CodeEmailKind = "verification_code"
)

// codeEmailFallbacks is the English copy. It is used when there is no
// localizer or a message is missing in every language, so an email never
// shows a raw key. Migration 0072 stores the same texts with their Turkish
// translations. Arguments are marked "%s", which the localization cache
// fills with fmt.Sprintf.
var codeEmailFallbacks = map[string]string{
	"email.reset_code.subject":        "Password Reset Code",
	"email.reset_code.intro":          "We received a request to reset your password. Use the code below to continue.",
	"email.reset_code.ignore":         "If you did not request this password reset, you can safely ignore this email.",
	"email.verification_code.subject": "Email Verification Code",
	"email.verification_code.intro":   "Use the code below to verify your email address and complete your FausCrew registration.",
	"email.verification_code.ignore":  "If you did not create a FausCrew account, you can safely ignore this email.",
	"email.code.expires":              "This code expires in %s.",
	"email.code.minute":               "%s minute",
	"email.code.minutes":              "%s minutes",
	"email.code.security_note":        "Never share this code with anyone. The FausCrew team will never ask for it.",
	"email.footer.automated":          "This is an automated message, please do not reply.",
}

// CodeEmailLocalizationKeys lists the messages the code emails read.
func CodeEmailLocalizationKeys() []string {
	keys := make([]string, 0, len(codeEmailFallbacks))
	for key := range codeEmailFallbacks {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// CodeEmailTemplateData is what code-email.html renders.
type CodeEmailTemplateData struct {
	// Language is the email's language, supported and normalized.
	Language  string
	Heading   string
	Intro     string
	CodeChars []string
	// The expiry line is ExpiresBefore + bold ExpiresValue + ExpiresAfter,
	// so each language can put the duration where its grammar wants it.
	ExpiresBefore string
	ExpiresValue  string
	ExpiresAfter  string
	IgnoreMessage string
	SecurityNote  string
	AutomatedNote string
}

// LocalizeCodeEmail builds a code email's content in a language. An empty or
// unsupported language, and a message the language lacks, fall back to
// English.
func LocalizeCodeEmail(ctx context.Context, localizer helpers.MessageLocalizer, kind CodeEmailKind, language, code string, validityMinutes int) CodeEmailTemplateData {
	language = helpers.NormalizeLanguage(language)
	if !helpers.IsSupportedLanguage(language) {
		language = helpers.DefaultLanguage
	}
	text := func(key string, args ...string) string {
		if localizer != nil {
			value := localizer.LocalizeMessage(ctx, language, helpers.LocalizationMessage(key, args...))
			// A localizer without the message hands back its key, alone or
			// with the arguments ("key: 10", "key||10"); no text starts so.
			if value != "" && !strings.HasPrefix(value, key) {
				return value
			}
		}
		value := codeEmailFallbacks[key]
		for _, arg := range args {
			value = strings.Replace(value, "%s", arg, 1)
		}
		return value
	}

	// Read without an argument, so the "%s" marking the duration stays.
	expiresBefore, expiresAfter, found := strings.Cut(text("email.code.expires"), "%s")
	if !found {
		expiresBefore, expiresAfter, _ = strings.Cut(codeEmailFallbacks["email.code.expires"], "%s")
	}
	minutesKey := "email.code.minutes"
	if validityMinutes == 1 {
		minutesKey = "email.code.minute"
	}

	prefix := "email." + string(kind) + "."
	return CodeEmailTemplateData{
		Language:      language,
		Heading:       text(prefix + "subject"),
		Intro:         text(prefix + "intro"),
		CodeChars:     strings.Split(code, ""),
		ExpiresBefore: expiresBefore,
		ExpiresValue:  text(minutesKey, strconv.Itoa(validityMinutes)),
		ExpiresAfter:  expiresAfter,
		IgnoreMessage: text(prefix + "ignore"),
		SecurityNote:  text("email.code.security_note"),
		AutomatedNote: text("email.footer.automated"),
	}
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

// RenderCodeEmail renders code-email.html.
func RenderCodeEmail(data CodeEmailTemplateData) (string, error) {
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

func (r *NotificationService) SendResetCodeEmail(toEmail, code string, validityMinutes int, language string) error {
	return r.sendCodeEmail(CodeEmailResetCode, toEmail, code, validityMinutes, language)
}

func (r *NotificationService) SendEmailVerificationCode(toEmail, code string, validityMinutes int, language string) error {
	return r.sendCodeEmail(CodeEmailVerificationCode, toEmail, code, validityMinutes, language)
}

// codeEmailLocalizeTimeout bounds the localization lookup; a translation the
// cache lacks is read from the database once.
const codeEmailLocalizeTimeout = 5 * time.Second

func (r *NotificationService) sendCodeEmail(kind CodeEmailKind, toEmail, code string, validityMinutes int, language string) error {
	if r.smtp.Password == "" {
		return fmt.Errorf("smtp password is missing")
	}

	ctx, cancel := context.WithTimeout(context.Background(), codeEmailLocalizeTimeout)
	content := LocalizeCodeEmail(ctx, r.localizer, kind, language, code, validityMinutes)
	cancel()
	htmlBody, err := RenderCodeEmail(content)
	if err != nil {
		return err
	}
	images, err := codeEmailImages()
	if err != nil {
		return err
	}
	message, err := BuildCodeEmailMessage(CodeEmailMessage{
		FromAddress: r.smtp.FromAddress,
		To:          toEmail,
		Subject:     MailFromName + " - " + content.Heading,
		HTMLBody:    htmlBody,
		TextBody:    RenderCodeEmailText(content),
		Images:      images,
	})
	if err != nil {
		return err
	}

	smtpAddress := net.JoinHostPort(r.smtp.Host, strconv.Itoa(r.smtp.Port))
	auth := smtp.PlainAuth("", r.smtp.Username, r.smtp.Password, r.smtp.Host)
	return sendMail(smtpAddress, r.smtp.Host, auth, r.smtp.FromAddress, toEmail, message)
}

// RenderCodeEmailText is the plain-text copy sent beside the HTML; mail
// filters score an HTML-only message as more likely spam, and some clients
// show only text.
func RenderCodeEmailText(data CodeEmailTemplateData) string {
	var text strings.Builder
	text.WriteString(data.Heading + "\n\n")
	text.WriteString(data.Intro + "\n\n")
	text.WriteString(strings.Join(data.CodeChars, "") + "\n\n")
	text.WriteString(data.ExpiresBefore + data.ExpiresValue + data.ExpiresAfter + "\n\n")
	text.WriteString(data.IgnoreMessage + "\n")
	text.WriteString(data.SecurityNote + "\n\n")
	text.WriteString("-- \n" + MailFromName + " - Make living together fun\n")
	text.WriteString(data.AutomatedNote + "\n")
	return text.String()
}

// InlineImage is a PNG an email's HTML loads with src="cid:<ContentID>".
type InlineImage struct {
	ContentID string
	FileName  string
	Data      []byte
}

// CodeEmailImageFiles maps the Content-IDs code-email.html loads images from
// to the static files that hold them. The hero is HTML; only the roof-F mark
// in its badge is an image, since Gmail draws no SVG.
var CodeEmailImageFiles = []struct{ ContentID, File string }{
	{ContentID: "fauscrew-logo", File: "email-logo.png"},
}

var codeEmailImages = sync.OnceValues(func() ([]InlineImage, error) {
	images := make([]InlineImage, 0, len(CodeEmailImageFiles))
	for _, file := range CodeEmailImageFiles {
		data, err := config.ReadStaticFile(file.File)
		if err != nil {
			return nil, fmt.Errorf("read code email image %s: %w", file.File, err)
		}
		images = append(images, InlineImage{ContentID: file.ContentID, FileName: file.File, Data: data})
	}
	return images, nil
})

// CodeEmailMessage is what BuildCodeEmailMessage puts in a code email.
type CodeEmailMessage struct {
	FromAddress string
	To          string
	Subject     string
	HTMLBody    string
	TextBody    string
	Images      []InlineImage
}

// BuildCodeEmailMessage builds a multipart/alternative message: the plain
// text first, then the HTML with its images in a multipart/related part. The
// images travel inline because mail clients block or drop remote and SVG
// images. Text parts are quoted-printable so no line passes the SMTP line
// length limit, and the subject is RFC 2047 encoded so non-ASCII text
// (Turkish) survives. Date and Message-ID are set here, since a message
// without them scores as spam.
func BuildCodeEmailMessage(message CodeEmailMessage) ([]byte, error) {
	var body bytes.Buffer
	alternative := multipart.NewWriter(&body)

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	textHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	if err := writeQuotedPrintablePart(alternative, textHeader, message.TextBody); err != nil {
		return nil, err
	}

	var related bytes.Buffer
	relatedWriter := multipart.NewWriter(&related)
	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	if err := writeQuotedPrintablePart(relatedWriter, htmlHeader, message.HTMLBody); err != nil {
		return nil, err
	}
	for _, image := range message.Images {
		imageHeader := textproto.MIMEHeader{}
		imageHeader.Set("Content-Type", "image/png")
		imageHeader.Set("Content-Transfer-Encoding", "base64")
		imageHeader.Set("Content-ID", "<"+image.ContentID+">")
		imageHeader.Set("Content-Disposition", `inline; filename="`+image.FileName+`"`)
		imagePart, err := relatedWriter.CreatePart(imageHeader)
		if err != nil {
			return nil, err
		}
		encoded := base64.StdEncoding.EncodeToString(image.Data)
		for len(encoded) > 0 {
			line := encoded[:min(76, len(encoded))]
			encoded = encoded[len(line):]
			if _, err := io.WriteString(imagePart, line+"\r\n"); err != nil {
				return nil, err
			}
		}
	}
	if err := relatedWriter.Close(); err != nil {
		return nil, err
	}
	relatedHeader := textproto.MIMEHeader{}
	relatedHeader.Set("Content-Type", `multipart/related; type="text/html"; boundary="`+relatedWriter.Boundary()+`"`)
	relatedPart, err := alternative.CreatePart(relatedHeader)
	if err != nil {
		return nil, err
	}
	if _, err := relatedPart.Write(related.Bytes()); err != nil {
		return nil, err
	}
	if err := alternative.Close(); err != nil {
		return nil, err
	}

	messageID, err := newMessageID(message.FromAddress)
	if err != nil {
		return nil, err
	}
	header := "From: " + MailFromName + " <" + message.FromAddress + ">\r\n" +
		"To: " + message.To + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n" +
		"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: " + messageID + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		`Content-Type: multipart/alternative; boundary="` + alternative.Boundary() + "\"\r\n\r\n"
	return append([]byte(header), body.Bytes()...), nil
}

func writeQuotedPrintablePart(writer *multipart.Writer, header textproto.MIMEHeader, content string) error {
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	encoder := quotedprintable.NewWriter(part)
	if _, err := encoder.Write([]byte(content)); err != nil {
		return err
	}
	return encoder.Close()
}

// newMessageID returns a unique Message-ID on the sender's domain.
func newMessageID(fromAddress string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	domain := fromAddress[strings.LastIndex(fromAddress, "@")+1:]
	return "<" + hex.EncodeToString(random) + "@" + domain + ">", nil
}

var (
	_ authAbstract.EmailSender      = (*NotificationService)(nil)
	_ notificationAbstract.Notifier = (*NotificationService)(nil)
)
