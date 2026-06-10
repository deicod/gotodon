package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/deicod/gotodon/internal/config"
)

const (
	StartTLSAuto   = "auto"
	StartTLSAlways = "always"
	StartTLSNever  = "never"

	AuthPlain   = "plain"
	AuthLogin   = "login"
	AuthCRAMMD5 = "cram_md5"
	AuthNone    = "none"

	defaultSendTimeout = 30 * time.Second
)

var (
	ErrMailNotConfigured = errors.New("smtp server is not configured")
	ErrStartTLSRequired  = errors.New("server does not support STARTTLS")
	ErrAuthUnsupported   = errors.New("unsupported smtp auth method")
)

// SMTPSender delivers messages to a single configured SMTP relay.
type SMTPSender struct {
	cfg     config.MailConfig
	timeout time.Duration
}

// NewSMTPSender creates a sender from mail configuration.
func NewSMTPSender(cfg config.MailConfig) (*SMTPSender, error) {
	if !cfg.Enabled() {
		return nil, ErrMailNotConfigured
	}
	switch strings.ToLower(cfg.SMTPAuthMethod) {
	case AuthPlain, AuthLogin, AuthCRAMMD5, AuthNone, "":
	default:
		return nil, fmt.Errorf("%w: %s", ErrAuthUnsupported, cfg.SMTPAuthMethod)
	}
	switch strings.ToLower(cfg.SMTPStartTLS) {
	case StartTLSAuto, StartTLSAlways, StartTLSNever, "":
	default:
		return nil, fmt.Errorf("invalid smtp_enable_starttls value: %s", cfg.SMTPStartTLS)
	}
	return &SMTPSender{cfg: cfg, timeout: defaultSendTimeout}, nil
}

// FromAddress returns the configured sender address.
func (s *SMTPSender) FromAddress() string {
	return s.cfg.SMTPFromAddress
}

// Send transmits a built message to a single recipient.
func (s *SMTPSender) Send(ctx context.Context, to string, message []byte) error {
	if s == nil {
		return ErrMailNotConfigured
	}

	timeout := s.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}

	addr := net.JoinHostPort(s.cfg.SMTPServer, fmt.Sprintf("%d", s.cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: timeout}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connecting to smtp server: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if s.cfg.SMTPSSL {
		conn = tls.Client(conn, s.tlsConfig())
	}

	client, err := smtp.NewClient(conn, s.cfg.SMTPServer)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if domain := strings.TrimSpace(s.cfg.SMTPDomain); domain != "" {
		if err := client.Hello(domain); err != nil {
			return fmt.Errorf("smtp hello: %w", err)
		}
	}

	if !s.cfg.SMTPSSL {
		if err := s.maybeStartTLS(client); err != nil {
			return err
		}
	}

	if err := s.authenticate(client); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err := client.Mail(envelopeAddress(s.cfg.SMTPFromAddress)); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(envelopeAddress(to)); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp data write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}

	return client.Quit()
}

func (s *SMTPSender) maybeStartTLS(client *smtp.Client) error {
	mode := strings.ToLower(s.cfg.SMTPStartTLS)
	if mode == "" {
		mode = StartTLSAuto
	}
	supported, _ := client.Extension("STARTTLS")
	switch mode {
	case StartTLSNever:
		return nil
	case StartTLSAuto:
		if !supported {
			return nil
		}
	case StartTLSAlways:
		if !supported {
			return ErrStartTLSRequired
		}
	}
	if err := client.StartTLS(s.tlsConfig()); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}
	return nil
}

func (s *SMTPSender) authenticate(client *smtp.Client) error {
	method := strings.ToLower(s.cfg.SMTPAuthMethod)
	if method == AuthNone || s.cfg.SMTPLogin == "" {
		return nil
	}
	var auth smtp.Auth
	switch method {
	case AuthLogin:
		auth = &loginAuth{username: s.cfg.SMTPLogin, password: s.cfg.SMTPPassword}
	case AuthCRAMMD5:
		auth = smtp.CRAMMD5Auth(s.cfg.SMTPLogin, s.cfg.SMTPPassword)
	case AuthPlain, "":
		auth = smtp.PlainAuth("", s.cfg.SMTPLogin, s.cfg.SMTPPassword, s.cfg.SMTPServer)
	default:
		return fmt.Errorf("%w: %s", ErrAuthUnsupported, method)
	}
	return client.Auth(auth)
}

func (s *SMTPSender) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         s.cfg.SMTPServer,
		InsecureSkipVerify: s.cfg.SMTPNoVerify, //nolint:gosec // operator opt-in via SMTP_OPENSSL_VERIFY_MODE=none
		MinVersion:         tls.VersionTLS12,
	}
}

// envelopeAddress strips an optional display name from an address.
func envelopeAddress(address string) string {
	address = strings.TrimSpace(address)
	if start := strings.LastIndex(address, "<"); start >= 0 {
		if end := strings.LastIndex(address, ">"); end > start {
			return address[start+1 : end]
		}
	}
	return address
}

// loginAuth implements the legacy AUTH LOGIN mechanism still required by
// several hosted SMTP relays.
type loginAuth struct {
	username string
	password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhost(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected server challenge: %s", fromServer)
	}
}

func isLocalhost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}
