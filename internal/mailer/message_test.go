package mailer

import (
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	msg, err := BuildMessage("notifications@example.com", "user@example.org", "Hello", "Line one\nLine two\n", now)
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}

	parsed, err := mail.ReadMessage(strings.NewReader(string(msg)))
	if err != nil {
		t.Fatalf("parsing message: %v", err)
	}

	if got := parsed.Header.Get("From"); got != "<notifications@example.com>" {
		t.Errorf("From = %q", got)
	}
	if got := parsed.Header.Get("To"); got != "<user@example.org>" {
		t.Errorf("To = %q", got)
	}
	if got := parsed.Header.Get("Subject"); got != "Hello" {
		t.Errorf("Subject = %q", got)
	}
	if got := parsed.Header.Get("Date"); got != "Tue, 09 Jun 2026 12:00:00 +0000" {
		t.Errorf("Date = %q", got)
	}
	if got := parsed.Header.Get("Content-Type"); got != `text/plain; charset="UTF-8"` {
		t.Errorf("Content-Type = %q", got)
	}
	if got := parsed.Header.Get("Content-Transfer-Encoding"); got != "quoted-printable" {
		t.Errorf("Content-Transfer-Encoding = %q", got)
	}
	id := parsed.Header.Get("Message-Id")
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID = %q", id)
	}

	decoded, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	body := strings.ReplaceAll(string(decoded), "\r\n", "\n")
	if body != "Line one\nLine two\n" {
		t.Errorf("body = %q", body)
	}
}

func TestBuildMessageEncodesNonASCIISubject(t *testing.T) {
	msg, err := BuildMessage("a@example.com", "b@example.com", "Grüße aus Köln", "hi", time.Now())
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(msg)))
	if err != nil {
		t.Fatalf("parsing message: %v", err)
	}
	raw := parsed.Header.Get("Subject")
	if !strings.HasPrefix(raw, "=?utf-8?q?") {
		t.Errorf("subject not RFC 2047 encoded: %q", raw)
	}
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(raw)
	if err != nil {
		t.Fatalf("decoding subject: %v", err)
	}
	if subject != "Grüße aus Köln" {
		t.Errorf("decoded subject = %q", subject)
	}
}

func TestBuildMessageEncodesLongBodyLines(t *testing.T) {
	long := strings.Repeat("a", 1000)
	msg, err := BuildMessage("a@example.com", "b@example.com", "s", long, time.Now())
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	_, body, found := strings.Cut(string(msg), "\r\n\r\n")
	if !found {
		t.Fatal("no header/body separator")
	}
	for _, line := range strings.Split(body, "\r\n") {
		if len(line) > 78 {
			t.Fatalf("body line exceeds 78 chars: %d", len(line))
		}
	}
}

func TestBuildMessageRejectsInvalidAddresses(t *testing.T) {
	if _, err := BuildMessage("not-an-address", "b@example.com", "s", "b", time.Now()); err == nil {
		t.Error("expected error for invalid from address")
	}
	if _, err := BuildMessage("a@example.com", "not-an-address", "s", "b", time.Now()); err == nil {
		t.Error("expected error for invalid to address")
	}
}
