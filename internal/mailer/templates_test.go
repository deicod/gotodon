package mailer

import (
	"strings"
	"testing"
)

var testInstance = InstanceInfo{Title: "Example Social", Domain: "social.example.com"}

func TestRenderConfirmationInstructions(t *testing.T) {
	mail := Mail{
		Kind: KindConfirmationInstructions,
		To:   "user@example.org",
		Params: map[string]string{
			"URL":     "https://social.example.com/auth/confirmation?confirmation_token=abc",
			"Pending": "false",
		},
	}
	subject, body, err := Render(mail, testInstance)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if subject != "Example Social: Confirmation instructions for social.example.com" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, "https://social.example.com/auth/confirmation?confirmation_token=abc") {
		t.Errorf("body missing confirmation link:\n%s", body)
	}
	if !strings.Contains(body, "You have created an account on social.example.com") {
		t.Errorf("body missing open-registration explanation:\n%s", body)
	}
	if strings.Contains(body, "review your application") {
		t.Errorf("body contains pending explanation for non-pending signup:\n%s", body)
	}
}

func TestRenderConfirmationInstructionsPending(t *testing.T) {
	mail := Mail{
		Kind: KindConfirmationInstructions,
		To:   "user@example.org",
		Params: map[string]string{
			"URL":     "https://social.example.com/x",
			"Pending": "true",
		},
	}
	_, body, err := Render(mail, testInstance)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(body, "review your application") {
		t.Errorf("body missing pending explanation:\n%s", body)
	}
}

func TestRenderAllKinds(t *testing.T) {
	params := map[string]string{
		"URL":       "https://social.example.com/x",
		"Pending":   "false",
		"NewEmail":  "new@example.org",
		"IP":        "192.0.2.1",
		"UserAgent": "TestBrowser/1.0",
		"Timestamp": "Jun 9, 2026 12:00 UTC",
		"ReportID":  "42",
		"Reporter":  "@alice",
		"Target":    "@bob@remote.example",
		"Username":  "carol",
		"Email":     "carol@example.org",
	}
	for _, kind := range Kinds() {
		mail := Mail{Kind: kind, To: "user@example.org", Params: params}
		subject, body, err := Render(mail, testInstance)
		if err != nil {
			t.Errorf("Render(%s) error = %v", kind, err)
			continue
		}
		if strings.TrimSpace(subject) == "" {
			t.Errorf("Render(%s) empty subject", kind)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("Render(%s) empty body", kind)
		}
		if strings.Contains(subject+body, "<no value>") {
			t.Errorf("Render(%s) has unresolved template values:\nsubject: %s\nbody: %s", kind, subject, body)
		}
	}
}

func TestRenderUnknownKind(t *testing.T) {
	if _, _, err := Render(Mail{Kind: "nope", To: "a@b.c"}, testInstance); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestRenderLocaleFallback(t *testing.T) {
	for _, locale := range []string{"", "en", "en-US", "de", "fr_CA"} {
		mail := Mail{Kind: KindPasswordChanged, To: "a@b.c", Locale: locale}
		if _, _, err := Render(mail, testInstance); err != nil {
			t.Errorf("Render(locale=%q) error = %v", locale, err)
		}
	}
}

func TestMailValidate(t *testing.T) {
	if err := (Mail{Kind: KindPasswordChanged, To: "a@b.c"}).Validate(); err != nil {
		t.Errorf("valid mail rejected: %v", err)
	}
	if err := (Mail{Kind: KindPasswordChanged}).Validate(); err == nil {
		t.Error("expected error for missing recipient")
	}
	if err := (Mail{Kind: "bogus", To: "a@b.c"}).Validate(); err == nil {
		t.Error("expected error for unknown kind")
	}
}
