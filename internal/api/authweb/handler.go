// Package authweb serves the minimal browser-facing account flows that email
// links point at: email confirmation and password reset.
package authweb

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
)

const (
	// confirmWithin mirrors Devise's confirm_within in Mastodon.
	confirmWithin = 48 * time.Hour
	// resetPasswordWithin mirrors Devise's reset_password_within in Mastodon.
	resetPasswordWithin = 6 * time.Hour
)

// Querier is the database surface used by the auth web flows.
type Querier interface {
	GetUserByConfirmationToken(ctx context.Context, confirmationToken pgtype.Text) (db.User, error)
	ConfirmUser(ctx context.Context, id int64) error
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	UpdateUserConfirmationToken(ctx context.Context, arg db.UpdateUserConfirmationTokenParams) error
	UpdateUserResetPasswordToken(ctx context.Context, arg db.UpdateUserResetPasswordTokenParams) error
	GetUserByResetPasswordToken(ctx context.Context, resetPasswordToken pgtype.Text) (db.User, error)
	UpdateUserPassword(ctx context.Context, arg db.UpdateUserPasswordParams) error
	RevokeOAuthAccessTokensByUser(ctx context.Context, resourceOwnerID pgtype.Int8) error
	ListUsersWithPermission(ctx context.Context, permission int64) ([]db.User, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
}

// Handler serves the confirmation and password reset endpoints.
type Handler struct {
	queries Querier
	cfg     *config.Config
	mail    mailer.Deliverer
	now     func() time.Time
}

// NewHandler creates an auth web handler.
func NewHandler(queries Querier, cfg *config.Config, mail mailer.Deliverer) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{queries: queries, cfg: cfg, mail: mail, now: time.Now}
}

// RegisterRoutes registers the auth web routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/confirmation", h.Confirm)
	mux.HandleFunc("GET /auth/confirmation/new", h.NewConfirmation)
	mux.HandleFunc("POST /auth/confirmation", h.ResendConfirmation)
	mux.HandleFunc("GET /auth/password/new", h.NewPassword)
	mux.HandleFunc("GET /auth/password/edit", h.EditPassword)
	mux.HandleFunc("POST /auth/password", h.PasswordPost)
	mux.HandleFunc("PUT /auth/password", h.UpdatePassword)
}

// Confirm handles GET /auth/confirmation?confirmation_token=...
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("confirmation_token"))
	if token == "" || h.queries == nil {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Confirmation failed",
			"The confirmation link is invalid. Please request a new confirmation email.")
		return
	}

	ctx := r.Context()
	user, err := h.queries.GetUserByConfirmationToken(ctx, pgtype.Text{String: token, Valid: true})
	if err != nil {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Confirmation failed",
			"The confirmation link is invalid. Please request a new confirmation email.")
		return
	}

	if user.ConfirmedAt.Valid {
		h.renderMessage(w, http.StatusOK, "Email already confirmed",
			"Your email address has already been confirmed. You can log in with your client application.")
		return
	}

	if user.ConfirmationSentAt.Valid && h.now().Sub(user.ConfirmationSentAt.Time) > confirmWithin {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Confirmation link expired",
			"This confirmation link has expired. Please request a new confirmation email.")
		return
	}

	if err := h.queries.ConfirmUser(ctx, user.ID); err != nil {
		h.renderMessage(w, http.StatusInternalServerError, "Confirmation failed",
			"Something went wrong while confirming your email address. Please try again.")
		return
	}

	if !user.Approved {
		h.notifyStaffAboutPendingAccount(ctx, user)
		h.renderMessage(w, http.StatusOK, "Email confirmed",
			"Your email address has been confirmed. Your account is now waiting for review by the server staff; you will be able to log in once it is approved.")
		return
	}

	h.renderMessage(w, http.StatusOK, "Email confirmed",
		"Your email address has been confirmed. You can now log in with your client application.")
}

// NewConfirmation handles GET /auth/confirmation/new (resend form).
func (h *Handler) NewConfirmation(w http.ResponseWriter, r *http.Request) {
	h.renderForm(w, formData{
		Title:  "Resend confirmation instructions",
		Action: "/auth/confirmation",
		Fields: []formField{{Name: "email", Label: "Email address", Type: "email"}},
		Submit: "Resend confirmation instructions",
	})
}

// ResendConfirmation handles POST /auth/confirmation. It never discloses
// whether the email exists.
func (h *Handler) ResendConfirmation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderMessage(w, http.StatusBadRequest, "Invalid request", "The submitted form was invalid.")
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	neutral := func() {
		h.renderMessage(w, http.StatusOK, "Check your inbox",
			"If your email address exists in our database and is awaiting confirmation, you will receive an email with confirmation instructions in a few minutes.")
	}
	if email == "" || h.queries == nil {
		neutral()
		return
	}

	ctx := r.Context()
	user, err := h.queries.GetUserByEmail(ctx, email)
	if err != nil || user.ConfirmedAt.Valid {
		neutral()
		return
	}

	token, err := auth.GenerateConfirmationToken()
	if err != nil {
		neutral()
		return
	}
	if err := h.queries.UpdateUserConfirmationToken(ctx, db.UpdateUserConfirmationTokenParams{
		ID:                user.ID,
		ConfirmationToken: pgtype.Text{String: token, Valid: true},
	}); err != nil {
		neutral()
		return
	}
	h.deliverMail(ctx, mailer.Mail{
		Kind:   mailer.KindConfirmationInstructions,
		To:     user.Email,
		Locale: textOrEmpty(user.Locale),
		Params: map[string]string{
			"URL":     h.baseURL() + "/auth/confirmation?confirmation_token=" + url.QueryEscape(token),
			"Pending": boolString(!user.Approved),
		},
	})
	neutral()
}

// NewPassword handles GET /auth/password/new (request reset form).
func (h *Handler) NewPassword(w http.ResponseWriter, r *http.Request) {
	h.renderForm(w, formData{
		Title:  "Reset your password",
		Action: "/auth/password",
		Fields: []formField{{Name: "email", Label: "Email address", Type: "email"}},
		Submit: "Send me reset password instructions",
	})
}

// PasswordPost handles POST /auth/password. Browsers cannot send PUT from
// plain forms, so a Rails-style _method override routes updates here too.
func (h *Handler) PasswordPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderMessage(w, http.StatusBadRequest, "Invalid request", "The submitted form was invalid.")
		return
	}
	if strings.EqualFold(r.PostFormValue("_method"), "put") {
		h.UpdatePassword(w, r)
		return
	}
	h.requestPasswordReset(w, r)
}

// requestPasswordReset sends reset instructions without disclosing whether
// the email exists.
func (h *Handler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	neutral := func() {
		h.renderMessage(w, http.StatusOK, "Check your inbox",
			"If your email address exists in our database, you will receive a password recovery link at your email address in a few minutes.")
	}
	if email == "" || h.queries == nil {
		neutral()
		return
	}

	ctx := r.Context()
	user, err := h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		neutral()
		return
	}

	token, err := auth.GenerateResetToken()
	if err != nil {
		neutral()
		return
	}
	if err := h.queries.UpdateUserResetPasswordToken(ctx, db.UpdateUserResetPasswordTokenParams{
		ID:                 user.ID,
		ResetPasswordToken: pgtype.Text{String: token, Valid: true},
	}); err != nil {
		neutral()
		return
	}
	h.deliverMail(ctx, mailer.Mail{
		Kind:   mailer.KindResetPasswordInstructions,
		To:     user.Email,
		Locale: textOrEmpty(user.Locale),
		Params: map[string]string{
			"URL": h.baseURL() + "/auth/password/edit?reset_password_token=" + url.QueryEscape(token),
		},
	})
	neutral()
}

// EditPassword handles GET /auth/password/edit?reset_password_token=...
func (h *Handler) EditPassword(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("reset_password_token"))
	if token == "" {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Invalid link",
			"The password reset link is invalid. Please request a new one.")
		return
	}
	h.renderForm(w, formData{
		Title:  "Set new password",
		Action: "/auth/password",
		Hidden: map[string]string{
			"_method":              "put",
			"reset_password_token": token,
		},
		Fields: []formField{
			{Name: "password", Label: "New password", Type: "password"},
			{Name: "password_confirmation", Label: "Confirm new password", Type: "password"},
		},
		Submit: "Change my password",
	})
}

// UpdatePassword handles PUT /auth/password (directly or via _method override).
func (h *Handler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderMessage(w, http.StatusBadRequest, "Invalid request", "The submitted form was invalid.")
		return
	}
	token := strings.TrimSpace(r.PostFormValue("reset_password_token"))
	password := r.PostFormValue("password")
	confirmation := r.PostFormValue("password_confirmation")

	if token == "" || h.queries == nil {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Password reset failed",
			"The password reset link is invalid. Please request a new one.")
		return
	}
	if password != confirmation {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Password reset failed",
			"The password confirmation does not match. Please go back and try again.")
		return
	}
	if err := auth.ValidatePassword(password); err != nil {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Password reset failed",
			"Validation failed: Password "+err.Error()+". Please go back and try again.")
		return
	}

	ctx := r.Context()
	user, err := h.queries.GetUserByResetPasswordToken(ctx, pgtype.Text{String: token, Valid: true})
	if err != nil {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Password reset failed",
			"The password reset link is invalid. Please request a new one.")
		return
	}
	if !user.ResetPasswordSentAt.Valid || h.now().Sub(user.ResetPasswordSentAt.Time) > resetPasswordWithin {
		h.renderMessage(w, http.StatusUnprocessableEntity, "Password reset failed",
			"This password reset link has expired. Please request a new one.")
		return
	}

	hashed, err := auth.HashPassword(password)
	if err != nil {
		h.renderMessage(w, http.StatusInternalServerError, "Password reset failed",
			"Something went wrong while updating your password. Please try again.")
		return
	}
	if err := h.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:                user.ID,
		EncryptedPassword: hashed,
	}); err != nil {
		h.renderMessage(w, http.StatusInternalServerError, "Password reset failed",
			"Something went wrong while updating your password. Please try again.")
		return
	}

	// Whoever held the old password must not stay signed in.
	if err := h.queries.RevokeOAuthAccessTokensByUser(ctx, pgtype.Int8{Int64: user.ID, Valid: true}); err != nil {
		slog.Error("Failed to revoke access tokens after password reset", "user_id", user.ID, "error", err)
	}

	h.deliverMail(ctx, mailer.Mail{
		Kind:   mailer.KindPasswordChanged,
		To:     user.Email,
		Locale: textOrEmpty(user.Locale),
	})

	h.renderMessage(w, http.StatusOK, "Password changed",
		"Your password has been changed successfully. You can now log in with your new password.")
}

func (h *Handler) notifyStaffAboutPendingAccount(ctx context.Context, user db.User) {
	if h.mail == nil || h.queries == nil {
		return
	}
	username := ""
	if account, err := h.queries.GetAccountByID(ctx, user.AccountID); err == nil {
		username = account.Username
	}
	staff, err := h.queries.ListUsersWithPermission(ctx, auth.PermissionManageUsers)
	if err != nil {
		slog.Error("Failed to load staff for pending account mail", "error", err)
		return
	}
	for _, member := range staff {
		h.deliverMail(ctx, mailer.Mail{
			Kind:   mailer.KindAdminNewPendingAccount,
			To:     member.Email,
			Locale: textOrEmpty(member.Locale),
			Params: map[string]string{
				"Username": username,
				"Email":    user.Email,
			},
		})
	}
}

func (h *Handler) deliverMail(ctx context.Context, mail mailer.Mail) {
	if h.mail == nil {
		return
	}
	if err := h.mail.Deliver(ctx, mail); err != nil {
		slog.Error("Failed to enqueue mail", "kind", mail.Kind, "error", err)
	}
}

func (h *Handler) baseURL() string {
	return "https://" + h.cfg.Instance.PublicWebDomain()
}

func textOrEmpty(value pgtype.Text) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

type formField struct {
	Name  string
	Label string
	Type  string
}

type formData struct {
	Title  string
	Action string
	Hidden map[string]string
	Fields []formField
	Submit string
}

type messageData struct {
	Title   string
	Message string
}

var pageStyle = `body{font-family:system-ui,sans-serif;background:#191b22;color:#fff;display:flex;justify-content:center;padding-top:10vh}
main{background:#282c37;border-radius:8px;padding:2rem;max-width:28rem;width:100%}
h1{font-size:1.25rem;margin-top:0}
p{color:#d9e1e8;line-height:1.5}
label{display:block;margin:1rem 0 .25rem;color:#d9e1e8}
input{width:100%;box-sizing:border-box;padding:.5rem;border-radius:4px;border:1px solid #393f4f;background:#191b22;color:#fff}
button{margin-top:1.5rem;width:100%;padding:.6rem;border:0;border-radius:4px;background:#595aff;color:#fff;font-size:1rem;cursor:pointer}`

var messageTemplate = template.Must(template.New("message").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title>
<style>` + pageStyle + `</style></head>
<body><main><h1>{{.Title}}</h1><p>{{.Message}}</p></main></body>
</html>
`))

var formTemplate = template.Must(template.New("form").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title>
<style>` + pageStyle + `</style></head>
<body><main><h1>{{.Title}}</h1>
<form method="post" action="{{.Action}}">
{{range $name, $value := .Hidden}}<input type="hidden" name="{{$name}}" value="{{$value}}">
{{end}}{{range .Fields}}<label for="{{.Name}}">{{.Label}}</label>
<input type="{{.Type}}" id="{{.Name}}" name="{{.Name}}" required>
{{end}}<button type="submit">{{.Submit}}</button>
</form></main></body>
</html>
`))

func (h *Handler) renderMessage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = messageTemplate.Execute(w, messageData{Title: title, Message: message})
}

func (h *Handler) renderForm(w http.ResponseWriter, data formData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = formTemplate.Execute(w, data)
}
