package chore

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"reflect"
	"strings"
	"testing"

	"houseflowApi/internal/config"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/services"
)

// mapLocalizer serves messages from a map the way the localization cache
// does: "%s" arguments, a missing message falls back to English, then to
// its key.
type mapLocalizer map[string]map[string]string

func (l mapLocalizer) LocalizeMessage(_ context.Context, language, keyOrMessage string) string {
	key, args := helpers.SplitLocalizationMessage(keyOrMessage)
	value, ok := l[language][key]
	if !ok {
		value, ok = l["en"][key]
	}
	if !ok {
		return keyOrMessage
	}
	if len(args) > 0 {
		values := make([]any, len(args))
		for index, arg := range args {
			values[index] = arg
		}
		value = fmt.Sprintf(value, values...)
	}
	return value
}

func renderCodeEmail(t *testing.T, data services.CodeEmailTemplateData) string {
	t.Helper()
	body, err := services.RenderCodeEmail(data)
	if err != nil {
		t.Fatalf("render code email: %v", err)
	}
	return body
}

func TestCodeEmailTemplateRendersContent(t *testing.T) {
	body := renderCodeEmail(t, services.LocalizeCodeEmail(context.Background(), nil, services.CodeEmailResetCode, "en", "A", 3))

	for _, expected := range []string{
		`<html lang="en">`,
		"Password Reset Code",
		"We received a request to reset your password.",
		">A</td>",
		"This code expires in <strong",
		">3 minutes</strong>.",
		"If you did not request this password reset",
		"Never share this code with anyone.",
		"This is an automated message, please do not reply.",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("rendered template does not contain %q", expected)
		}
	}

	single := services.LocalizeCodeEmail(context.Background(), nil, services.CodeEmailVerificationCode, "en", "A", 1)
	if single.ExpiresValue != "1 minute" {
		t.Fatalf("one minute reads %q", single.ExpiresValue)
	}
}

func TestCodeEmailTemplatePutsEveryCharacterInItsOwnBox(t *testing.T) {
	code := []string{"K", "7", "Q", "M", "2", "X"}
	body := renderCodeEmail(t, services.LocalizeCodeEmail(context.Background(), nil, services.CodeEmailResetCode, "en", strings.Join(code, ""), 10))

	if got := strings.Count(body, `class="fc-box"`); got != len(code) {
		t.Fatalf("rendered %d code boxes, want %d", got, len(code))
	}
	if got := strings.Count(body, `class="fc-box-gap"`); got != len(code)-1 {
		t.Fatalf("rendered %d gaps between code boxes, want %d", got, len(code)-1)
	}
	rest := body
	for _, char := range code {
		box := strings.Index(rest, `class="fc-box"`)
		if box < 0 {
			t.Fatalf("no box left for %q", char)
		}
		rest = rest[box:]
		end := strings.Index(rest, "</td>")
		if cell := rest[:end]; !strings.HasSuffix(cell, ">"+char) {
			t.Fatalf("box %q does not hold exactly that character: %q", char, cell)
		}
		rest = rest[end:]
	}
}

func TestCodeEmailIsRenderedInTheRecipientLanguage(t *testing.T) {
	localizer := mapLocalizer{"tr": {
		"email.verification_code.subject": "E-posta Doğrulama Kodu",
		"email.verification_code.intro":   "Aşağıdaki kodu kullanın.",
		"email.verification_code.ignore":  "Görmezden gelebilirsiniz.",
		"email.code.expires":              "Bu kod %s sonra geçersiz olacak.",
		"email.code.minutes":              "%s dakika",
		"email.code.security_note":        "Bu kodu kimseyle paylaşmayın.",
		"email.footer.automated":          "Bu otomatik bir e-postadır.",
	}}

	content := services.LocalizeCodeEmail(context.Background(), localizer, services.CodeEmailVerificationCode, "tr-TR", "K7QM2X", 10)
	if content.Language != "tr" || content.Heading != "E-posta Doğrulama Kodu" || content.ExpiresValue != "10 dakika" {
		t.Fatalf("content = %+v", content)
	}
	body := renderCodeEmail(t, content)
	for _, expected := range []string{
		`<html lang="tr">`,
		"E-posta Doğrulama Kodu",
		"Aşağıdaki kodu kullanın.",
		"Bu kod <strong",
		">10 dakika</strong> sonra geçersiz olacak.",
		"Görmezden gelebilirsiniz.",
		"Bu kodu kimseyle paylaşmayın.",
		"Bu otomatik bir e-postadır.",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("rendered template does not contain %q", expected)
		}
	}
	if strings.Contains(body, "email.") {
		t.Fatal("rendered template shows a localization key")
	}
}

func TestCodeEmailFallsBackToEnglish(t *testing.T) {
	english := services.LocalizeCodeEmail(context.Background(), nil, services.CodeEmailResetCode, "en", "K7QM2X", 10)
	for _, language := range []string{"", "de", "tr"} {
		// mapLocalizer{} knows no message at all, like an empty database.
		got := services.LocalizeCodeEmail(context.Background(), mapLocalizer{}, services.CodeEmailResetCode, language, "K7QM2X", 10)
		if language != "tr" && got.Language != "en" {
			t.Fatalf("language %q rendered as %q, want en", language, got.Language)
		}
		got.Language = english.Language
		if !reflect.DeepEqual(got, english) {
			t.Fatalf("language %q without messages = %+v, want the English copy %+v", language, got, english)
		}
	}
}

const testFromAddress = "no-reply@iworf.com"

func buildCodeEmail(t *testing.T, message services.CodeEmailMessage) *mail.Message {
	t.Helper()
	raw, err := services.BuildCodeEmailMessage(message)
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("message has a line of %d characters", len(line))
		}
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	return parsed
}

func nextPart(t *testing.T, reader *multipart.Reader, wantType string) (*multipart.Part, map[string]string) {
	t.Helper()
	part, err := reader.NextPart()
	if err != nil {
		t.Fatalf("%s part: %v", wantType, err)
	}
	mediaType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil || mediaType != wantType {
		t.Fatalf("part Content-Type = %q, want %s (%v)", part.Header.Get("Content-Type"), wantType, err)
	}
	return part, params
}

func TestCodeEmailSubjectIsEncodedForNonASCII(t *testing.T) {
	message := buildCodeEmail(t, services.CodeEmailMessage{
		FromAddress: testFromAddress, To: "user@example.com", Subject: "FausCrew - Şifre Sıfırlama Kodu", HTMLBody: "<p>x</p>",
	})
	encoded := message.Header.Get("Subject")
	if !strings.HasPrefix(encoded, "=?utf-8?") {
		t.Fatalf("Subject = %q, want an RFC 2047 encoded word", encoded)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(encoded)
	if err != nil || subject != "FausCrew - Şifre Sıfırlama Kodu" {
		t.Fatalf("decoded Subject = %q (%v)", subject, err)
	}
}

func TestCodeEmailCarriesDateAndMessageIDOnTheSenderDomain(t *testing.T) {
	build := func() *mail.Message {
		return buildCodeEmail(t, services.CodeEmailMessage{
			FromAddress: testFromAddress, To: "user@example.com", Subject: "FausCrew - Code", HTMLBody: "<p>x</p>",
		})
	}
	first, second := build(), build()
	if _, err := first.Header.Date(); err != nil {
		t.Fatalf("Date = %q (%v)", first.Header.Get("Date"), err)
	}
	id := first.Header.Get("Message-ID")
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@iworf.com>") {
		t.Fatalf("Message-ID = %q, want one on the sender domain", id)
	}
	if id == second.Header.Get("Message-ID") {
		t.Fatal("two messages share a Message-ID")
	}
	if got := first.Header.Get("From"); got != "FausCrew <"+testFromAddress+">" {
		t.Fatalf("From = %q", got)
	}
}

func TestCodeEmailTextCopyHoldsTheCode(t *testing.T) {
	content := services.LocalizeCodeEmail(context.Background(), nil, services.CodeEmailResetCode, "en", "K7QM2X", 10)
	text := services.RenderCodeEmailText(content)
	for _, expected := range []string{
		content.Heading, content.Intro, "\nK7QM2X\n", "This code expires in 10 minutes.",
		content.IgnoreMessage, content.SecurityNote, content.AutomatedNote,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("text copy does not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "<") {
		t.Fatalf("text copy holds markup:\n%s", text)
	}
}

func TestCodeEmailMessageEmbedsItsImages(t *testing.T) {
	templateContent, err := config.ReadStaticFile("code-email.html")
	if err != nil {
		t.Fatalf("read code email template: %v", err)
	}
	var images []services.InlineImage
	for _, file := range services.CodeEmailImageFiles {
		if !strings.Contains(string(templateContent), `src="cid:`+file.ContentID+`"`) {
			t.Fatalf("template does not load cid:%s", file.ContentID)
		}
		data, err := config.ReadStaticFile(file.File)
		if err != nil {
			t.Fatalf("read %s: %v", file.File, err)
		}
		images = append(images, services.InlineImage{ContentID: file.ContentID, FileName: file.File, Data: data})
	}

	htmlBody := "<p>" + strings.Repeat("K7QM2X ", 400) + "</p>"
	textBody := "Şifre Sıfırlama Kodu\n\n" + strings.Repeat("K7QM2X ", 400) + "\n"
	message := buildCodeEmail(t, services.CodeEmailMessage{
		FromAddress: testFromAddress, To: "user@example.com", Subject: "FausCrew - Password Reset Code",
		HTMLBody: htmlBody, TextBody: textBody, Images: images,
	})
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v)", message.Header.Get("Content-Type"), err)
	}

	// Plain text first, then the HTML with its images: clients show the last
	// alternative they can render.
	alternatives := multipart.NewReader(message.Body, params["boundary"])
	textPart, _ := nextPart(t, alternatives, "text/plain")
	// Text parts travel with CRLF line breaks, as SMTP wants.
	if decoded, err := io.ReadAll(textPart); err != nil || strings.ReplaceAll(string(decoded), "\r\n", "\n") != textBody {
		t.Fatalf("text part does not round-trip (%v)", err)
	}
	relatedPart, relatedParams := nextPart(t, alternatives, "multipart/related")
	related := multipart.NewReader(relatedPart, relatedParams["boundary"])
	htmlPart, _ := nextPart(t, related, "text/html")
	if decoded, err := io.ReadAll(htmlPart); err != nil || string(decoded) != htmlBody {
		t.Fatalf("html part does not round-trip (%v)", err)
	}
	for _, image := range images {
		imagePart, _ := nextPart(t, related, "image/png")
		if got := imagePart.Header.Get("Content-ID"); got != "<"+image.ContentID+">" {
			t.Fatalf("Content-ID = %q, want <%s>", got, image.ContentID)
		}
		encoded, err := io.ReadAll(imagePart)
		if err != nil {
			t.Fatalf("read image part: %v", err)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(encoded), "\r\n", ""))
		if err != nil || !bytes.Equal(decoded, image.Data) {
			t.Fatalf("image part %s does not decode to its file (%v)", image.ContentID, err)
		}
	}
	if _, err := related.NextPart(); err != io.EOF {
		t.Fatalf("related part has extra parts (%v)", err)
	}
	if _, err := alternatives.NextPart(); err != io.EOF {
		t.Fatalf("message has extra alternatives (%v)", err)
	}
}
