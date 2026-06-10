package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/config"
)

// fakeSMTPServer speaks just enough ESMTP for the sender tests.
type fakeSMTPServer struct {
	listener net.Listener

	mu       sync.Mutex
	commands []string
	authLine string
	mailFrom string
	rcptTo   string
	data     string
}

func newFakeSMTPServer(t *testing.T, authMechanisms string) *fakeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTPServer{listener: listener}
	go server.serve(authMechanisms)
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *fakeSMTPServer) port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *fakeSMTPServer) serve(authMechanisms string) {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }

	write("220 fake ESMTP ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.commands = append(s.commands, line)
		s.mu.Unlock()

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if authMechanisms != "" {
				write("250-fake")
				write("250-AUTH " + authMechanisms)
				write("250 8BITMIME")
			} else {
				write("250-fake")
				write("250 8BITMIME")
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			s.mu.Lock()
			s.authLine = line
			s.mu.Unlock()
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			user, _ := reader.ReadString('\n')
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			pass, _ := reader.ReadString('\n')
			s.mu.Lock()
			s.authLine = "LOGIN " + strings.TrimSpace(user) + " " + strings.TrimSpace(pass)
			s.mu.Unlock()
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mu.Lock()
			s.mailFrom = line
			s.mu.Unlock()
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.rcptTo = line
			s.mu.Unlock()
			write("250 OK")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				data.WriteString(dataLine)
			}
			s.mu.Lock()
			s.data = data.String()
			s.mu.Unlock()
			write("250 OK queued")
		case upper == "QUIT":
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func (s *fakeSMTPServer) snapshot() (authLine, mailFrom, rcptTo, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authLine, s.mailFrom, s.rcptTo, s.data
}

func testMailConfig(port int) config.MailConfig {
	return config.MailConfig{
		SMTPServer:      "127.0.0.1",
		SMTPPort:        port,
		SMTPLogin:       "smtpuser",
		SMTPPassword:    "smtppass",
		SMTPAuthMethod:  "plain",
		SMTPFromAddress: "notifications@example.com",
		SMTPStartTLS:    "never",
	}
}

func TestSMTPSenderSendPlainAuth(t *testing.T) {
	server := newFakeSMTPServer(t, "PLAIN LOGIN")
	sender, err := NewSMTPSender(testMailConfig(server.port()))
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}

	message := []byte("Subject: hi\r\n\r\nbody line\r\n")
	if err := sender.Send(context.Background(), "user@example.org", message); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	authLine, mailFrom, rcptTo, data := server.snapshot()
	wantAuth := base64.StdEncoding.EncodeToString([]byte("\x00smtpuser\x00smtppass"))
	if authLine != "AUTH PLAIN "+wantAuth {
		t.Errorf("auth = %q", authLine)
	}
	if !strings.HasPrefix(mailFrom, "MAIL FROM:<notifications@example.com>") {
		t.Errorf("mail from = %q", mailFrom)
	}
	if rcptTo != "RCPT TO:<user@example.org>" {
		t.Errorf("rcpt to = %q", rcptTo)
	}
	if !strings.Contains(data, "body line") {
		t.Errorf("data = %q", data)
	}
}

func TestSMTPSenderSendLoginAuth(t *testing.T) {
	server := newFakeSMTPServer(t, "LOGIN")
	cfg := testMailConfig(server.port())
	cfg.SMTPAuthMethod = "login"
	sender, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}

	if err := sender.Send(context.Background(), "user@example.org", []byte("Subject: hi\r\n\r\nx\r\n")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	authLine, _, _, _ := server.snapshot()
	wantUser := base64.StdEncoding.EncodeToString([]byte("smtpuser"))
	wantPass := base64.StdEncoding.EncodeToString([]byte("smtppass"))
	if authLine != "LOGIN "+wantUser+" "+wantPass {
		t.Errorf("auth = %q", authLine)
	}
}

func TestSMTPSenderSendNoAuth(t *testing.T) {
	server := newFakeSMTPServer(t, "")
	cfg := testMailConfig(server.port())
	cfg.SMTPAuthMethod = "none"
	sender, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}
	if err := sender.Send(context.Background(), "user@example.org", []byte("Subject: hi\r\n\r\nx\r\n")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	authLine, _, _, _ := server.snapshot()
	if authLine != "" {
		t.Errorf("unexpected auth: %q", authLine)
	}
}

func TestSMTPSenderHelloDomain(t *testing.T) {
	server := newFakeSMTPServer(t, "")
	cfg := testMailConfig(server.port())
	cfg.SMTPAuthMethod = "none"
	cfg.SMTPDomain = "mail.example.com"
	sender, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}
	if err := sender.Send(context.Background(), "user@example.org", []byte("Subject: hi\r\n\r\nx\r\n")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	found := false
	for _, cmd := range server.commands {
		if strings.EqualFold(cmd, "EHLO mail.example.com") {
			found = true
		}
	}
	if !found {
		t.Errorf("EHLO domain not used; commands: %v", server.commands)
	}
}

func TestSMTPSenderStartTLSAlwaysFailsWithoutSupport(t *testing.T) {
	server := newFakeSMTPServer(t, "")
	cfg := testMailConfig(server.port())
	cfg.SMTPAuthMethod = "none"
	cfg.SMTPStartTLS = "always"
	sender, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}
	err = sender.Send(context.Background(), "user@example.org", []byte("Subject: hi\r\n\r\nx\r\n"))
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("expected STARTTLS error, got %v", err)
	}
}

func TestNewSMTPSenderValidation(t *testing.T) {
	if _, err := NewSMTPSender(config.MailConfig{}); err == nil {
		t.Error("expected error for missing server")
	}
	if _, err := NewSMTPSender(config.MailConfig{SMTPServer: "x", SMTPAuthMethod: "bogus"}); err == nil {
		t.Error("expected error for bad auth method")
	}
	if _, err := NewSMTPSender(config.MailConfig{SMTPServer: "x", SMTPStartTLS: "bogus"}); err == nil {
		t.Error("expected error for bad starttls mode")
	}
}

func TestEnvelopeAddress(t *testing.T) {
	cases := map[string]string{
		"user@example.org":                   "user@example.org",
		"Name <user@example.org>":            "user@example.org",
		" spaced <user@example.org> ":        "user@example.org",
		"\"Quoted Name\" <user@example.org>": "user@example.org",
	}
	for input, want := range cases {
		if got := envelopeAddress(input); got != want {
			t.Errorf("envelopeAddress(%q) = %q, want %q", input, got, want)
		}
	}
}
