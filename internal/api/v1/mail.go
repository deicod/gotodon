package v1

import (
	"net/url"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/mailer"
)

// mailPublicDomain resolves the user-facing domain for links in emails.
func mailPublicDomain(cfg *config.Config) string {
	if cfg == nil {
		return "localhost:3000"
	}
	return cfg.Instance.PublicWebDomain()
}

// confirmationMail builds the confirmation-instructions mail for a new or
// re-sent registration confirmation.
func confirmationMail(cfg *config.Config, email, locale, token string, approved bool) mailer.Mail {
	pending := "false"
	if !approved {
		pending = "true"
	}
	confirmationURL := "https://" + mailPublicDomain(cfg) + "/auth/confirmation?confirmation_token=" + url.QueryEscape(token)
	return mailer.Mail{
		Kind:   mailer.KindConfirmationInstructions,
		To:     email,
		Locale: locale,
		Params: map[string]string{
			"URL":     confirmationURL,
			"Pending": pending,
		},
	}
}
