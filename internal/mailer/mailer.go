// Package mailer renders and delivers transactional email over SMTP.
package mailer

import (
	"context"
	"errors"
	"strings"
)

// Mail kinds rendered by this package.
const (
	KindConfirmationInstructions  = "confirmation_instructions"
	KindResetPasswordInstructions = "reset_password_instructions"
	KindPasswordChanged           = "password_changed"
	KindEmailChanged              = "email_changed"
	KindSuspiciousSignIn          = "suspicious_sign_in"
	KindAdminNewReport            = "admin_new_report"
	KindAdminNewPendingAccount    = "admin_new_pending_account"
)

var (
	ErrUnknownKind   = errors.New("unknown mail kind")
	ErrRecipientMiss = errors.New("mail recipient is required")
)

// Mail describes a single email to deliver. Params feed the kind's template.
type Mail struct {
	Kind   string            `json:"kind"`
	To     string            `json:"to"`
	Locale string            `json:"locale,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

// Validate reports whether the mail can be rendered and delivered.
func (m Mail) Validate() error {
	if strings.TrimSpace(m.To) == "" {
		return ErrRecipientMiss
	}
	if _, ok := templates[m.Kind]; !ok {
		return ErrUnknownKind
	}
	return nil
}

// Deliverer hands a mail off for delivery (queued or direct).
type Deliverer interface {
	Deliver(ctx context.Context, mail Mail) error
}

// Sender transmits a fully built message to a recipient.
type Sender interface {
	Send(ctx context.Context, to string, message []byte) error
}
