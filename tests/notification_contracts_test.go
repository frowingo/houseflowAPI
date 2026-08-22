package tests

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"houseflowApi/internal/config"
)

func TestCodeEmailTemplateRendersContent(t *testing.T) {
	templateContent, err := config.ReadStaticFile("code-email.html")
	if err != nil {
		t.Fatalf("read code email template: %v", err)
	}

	tmpl, err := template.New("code-email").Parse(string(templateContent))
	if err != nil {
		t.Fatalf("parse code email template: %v", err)
	}

	data := struct {
		Heading         string
		Intro           string
		CodeChars       []string
		ValidityMinutes int
		IgnoreMessage   string
	}{
		Heading:         "Email Verification Code",
		Intro:           "Verify your email.",
		CodeChars:       []string{"A"},
		ValidityMinutes: 3,
		IgnoreMessage:   "Ignore this email.",
	}

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		t.Fatalf("render code email template: %v", err)
	}

	for _, expected := range []string{
		"Email Verification Code",
		"Verify your email.",
		">A</td>",
		"3 minutes",
		"Ignore this email.",
	} {
		if !strings.Contains(body.String(), expected) {
			t.Fatalf("rendered template does not contain %q", expected)
		}
	}
}
