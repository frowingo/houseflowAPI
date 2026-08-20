package services

import (
	"strings"
	"testing"
)

func TestRenderCodeEmailTemplate(t *testing.T) {
	body, err := renderCodeEmailTemplate(codeEmailTemplateData{
		Heading:         "Email Verification Code",
		Intro:           "Verify your email.",
		CodeChars:       []string{"A"},
		ValidityMinutes: 3,
		IgnoreMessage:   "Ignore this email.",
	})
	if err != nil {
		t.Fatalf("render template: %v", err)
	}

	for _, expected := range []string{
		"Email Verification Code",
		"Verify your email.",
		">A</td>",
		"3 minutes",
		"Ignore this email.",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("rendered template does not contain %q", expected)
		}
	}
}
