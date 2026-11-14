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
		"Action":    "silence",
		"Acct":      "@carol@social.example.com",
		"Text":      "Spam",
		"Statuses":  "=> https://social.example.com/@carol/1",
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

func TestRenderWarning(t *testing.T) {
	tests := []struct {
		action      string
		subject     string
		title       string
		explanation string
	}{
		{"none", "Warning for @carol@social.example.com", "Warning", ""},
		{"disable", "Your account @carol@social.example.com has been frozen", "Account frozen", "You can no longer use your account, but your profile"},
		{"mark_statuses_as_sensitive", "Your posts on @carol@social.example.com have been marked as sensitive", "Posts marked as sensitive", "marked as sensitive by the moderators of social.example.com."},
		{"delete_statuses", "Your posts on @carol@social.example.com have been removed", "Posts removed", "removed by the moderators of social.example.com."},
		{"sensitive", "Your posts on @carol@social.example.com will be marked as sensitive from now on", "Account marked as sensitive", "From now on, all your uploaded media files"},
		{"silence", "Your account @carol@social.example.com has been limited", "Account limited", "only people who are already following you"},
		{"suspend", "Your account @carol@social.example.com has been suspended", "Account suspended", "You can no longer use your account, and your profile"},
		// An action the template doesn't know reads as none.
		{"unknown", "Warning for @carol@social.example.com", "Warning", ""},
	}
	explanations := []string{}
	for _, tt := range tests {
		if tt.explanation != "" {
			explanations = append(explanations, tt.explanation)
		}
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			mail := Mail{Kind: KindWarning, To: "carol@example.org", Params: map[string]string{
				"Action":   tt.action,
				"Acct":     "@carol@social.example.com",
				"Text":     "Please stop posting spam.",
				"Statuses": "=> https://social.example.com/@carol/1\n=> https://social.example.com/@carol/2",
			}}
			subject, body, err := Render(mail, testInstance)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if subject != tt.subject {
				t.Errorf("subject = %q, want %q", subject, tt.subject)
			}
			if !strings.HasPrefix(body, tt.title+"\n\n===\n") {
				t.Errorf("body doesn't start with the title %q:\n%s", tt.title, body)
			}
			for _, explanation := range explanations {
				if got, want := strings.Contains(body, explanation), explanation == tt.explanation; got != want {
					t.Errorf("body contains %q = %v, want %v:\n%s", explanation, got, want, body)
				}
			}
			for _, part := range []string{
				"Reason:\n\nPlease stop posting spam.\n",
				"Posts cited:\n\n=> https://social.example.com/@carol/1\n=> https://social.example.com/@carol/2\n",
			} {
				if !strings.Contains(body, part) {
					t.Errorf("body missing %q:\n%s", part, body)
				}
			}
			if strings.Contains(subject+body, "<no value>") {
				t.Errorf("unresolved template values:\nsubject: %s\nbody: %s", subject, body)
			}
		})
	}
}

// TestRenderWarningWithoutTextOrStatuses checks that a warning without text
// or cited posts leaves out their sections, and that a mail without an
// action reads as a plain warning.
func TestRenderWarningWithoutTextOrStatuses(t *testing.T) {
	for _, params := range []map[string]string{
		{"Action": "none", "Acct": "@carol@social.example.com", "Text": "", "Statuses": ""},
		{"Acct": "@carol@social.example.com"},
	} {
		subject, body, err := Render(Mail{Kind: KindWarning, To: "carol@example.org", Params: params}, testInstance)
		if err != nil {
			t.Fatalf("Render(%v) error = %v", params, err)
		}
		if subject != "Warning for @carol@social.example.com" {
			t.Errorf("Render(%v) subject = %q", params, subject)
		}
		if body != "Warning\n\n===\n" {
			t.Errorf("Render(%v) body = %q, want only the title", params, body)
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
