package mailer

import (
	"fmt"
	"sort"
	"strings"
	texttemplate "text/template"
)

// kindTemplate holds the localized subject and body templates for a mail kind.
type kindTemplate struct {
	subject map[string]*texttemplate.Template
	body    map[string]*texttemplate.Template
}

const defaultLocale = "en"

func mustBody(locale, kind, text string) *texttemplate.Template {
	return texttemplate.Must(texttemplate.New(kind + "." + locale).Parse(text))
}

func mustSubject(locale, kind, text string) *texttemplate.Template {
	return texttemplate.Must(texttemplate.New(kind + ".subject." + locale).Parse(text))
}

// Template params common to all kinds: Instance (title), Domain, BaseURL.
// Kind-specific params are documented per template below.
var templates = map[string]*kindTemplate{
	// Params: URL (confirmation link), Pending ("true" when approval required)
	KindConfirmationInstructions: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindConfirmationInstructions, "{{.Instance}}: Confirmation instructions for {{.Domain}}"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindConfirmationInstructions, `Verify email address

===

{{if eq .Pending "true"}}You applied for an invite to {{.Domain}} with this email address. Once you confirm your e-mail address, we will review your application. You cannot access most of the functions until your account is approved. If your application is rejected, your data will be removed, so no further action will be required from you. If this wasn't you, please ignore this email.{{else}}You have created an account on {{.Domain}} with this email address. You are one click away from activating it. If this wasn't you, please ignore this email.{{end}}

=> {{.URL}}

Please also check out the rules of the server and our terms of service.

=> {{.BaseURL}}/about
=> {{.BaseURL}}/privacy-policy
`),
		},
	},
	// Params: URL (reset link)
	KindResetPasswordInstructions: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindResetPasswordInstructions, "{{.Instance}}: Reset password instructions"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindResetPasswordInstructions, `Password reset

===

You requested a new password for your account.

=> {{.URL}}

If you didn't request this, please ignore this email. Your password won't change until you access the link above and create a new one.
`),
		},
	},
	// Params: none beyond common.
	KindPasswordChanged: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindPasswordChanged, "{{.Instance}}: Password changed"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindPasswordChanged, `Password changed

===

The password for your account has been changed.

If you did not change your password, it is likely that someone has gained access to your account. Please change your password immediately or contact the server admin if you're locked out of your account.
`),
		},
	},
	// Params: NewEmail
	KindEmailChanged: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindEmailChanged, "{{.Instance}}: Email changed"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindEmailChanged, `New email address

===

The email address for your account is being changed to:

{{.NewEmail}}

If you did not change your email, it is likely that someone has gained access to your account. Please change your password immediately or contact the server admin if you're locked out of your account.
`),
		},
	},
	// Params: IP, UserAgent, Timestamp
	KindSuspiciousSignIn: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindSuspiciousSignIn, "{{.Instance}}: New sign-in to your account"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindSuspiciousSignIn, `New sign-in to your account

===

We detected a sign-in to your account from a new IP address.

Here are the details of the sign-in:

IP: {{.IP}}
Browser: {{.UserAgent}}
{{.Timestamp}}

If this was you, you can safely ignore this email. If this wasn't you, we recommend that you change your password immediately and enable two-factor authentication.

=> {{.BaseURL}}/auth/password/new
`),
		},
	},
	// Params: ReportID, Reporter, Target
	KindAdminNewReport: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindAdminNewReport, "New report for {{.Domain}} (#{{.ReportID}})"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindAdminNewReport, `New report

===

{{.Reporter}} has reported {{.Target}}

View all reports:

=> {{.BaseURL}}/admin/reports
`),
		},
	},
	// Params: Username, Email
	KindAdminNewPendingAccount: {
		subject: map[string]*texttemplate.Template{
			"en": mustSubject("en", KindAdminNewPendingAccount, "A new account is up for review on {{.Domain}} ({{.Username}})"),
		},
		body: map[string]*texttemplate.Template{
			"en": mustBody("en", KindAdminNewPendingAccount, `New account up for review

===

A new account ({{.Username}}, {{.Email}}) is waiting for your review. You can approve or reject this application.

=> {{.BaseURL}}/admin/accounts
`),
		},
	},
}

// Kinds returns the supported mail kinds, sorted.
func Kinds() []string {
	kinds := make([]string, 0, len(templates))
	for kind := range templates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// InstanceInfo provides the common values available to every template.
type InstanceInfo struct {
	Title  string
	Domain string
}

// BaseURL returns the canonical https base URL of the instance.
func (i InstanceInfo) BaseURL() string {
	return "https://" + i.Domain
}

// Render produces the subject and body for a mail using the given locale,
// falling back to English when the locale has no translation.
func Render(mail Mail, instance InstanceInfo) (subject, body string, err error) {
	tmpl, ok := templates[mail.Kind]
	if !ok {
		return "", "", ErrUnknownKind
	}

	data := map[string]string{
		"Instance": instance.Title,
		"Domain":   instance.Domain,
		"BaseURL":  instance.BaseURL(),
	}
	for key, value := range mail.Params {
		data[key] = value
	}

	locale := normalizeLocale(mail.Locale)
	subjectTmpl, ok := tmpl.subject[locale]
	if !ok {
		subjectTmpl = tmpl.subject[defaultLocale]
	}
	bodyTmpl, ok := tmpl.body[locale]
	if !ok {
		bodyTmpl = tmpl.body[defaultLocale]
	}

	var subjectSB strings.Builder
	if err := subjectTmpl.Execute(&subjectSB, data); err != nil {
		return "", "", fmt.Errorf("rendering subject for %s: %w", mail.Kind, err)
	}

	var sb strings.Builder
	if err := bodyTmpl.Execute(&sb, data); err != nil {
		return "", "", fmt.Errorf("rendering body for %s: %w", mail.Kind, err)
	}
	return subjectSB.String(), sb.String(), nil
}

func normalizeLocale(locale string) string {
	locale = strings.ToLower(strings.TrimSpace(locale))
	if locale == "" {
		return defaultLocale
	}
	// "en-US" and "en_US" fall back to their base language.
	if idx := strings.IndexAny(locale, "-_"); idx > 0 {
		locale = locale[:idx]
	}
	return locale
}
