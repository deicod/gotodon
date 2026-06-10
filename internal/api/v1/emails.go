// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/web"
)

type emailQuerier interface {
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	UpdateUserConfirmationToken(ctx context.Context, arg db.UpdateUserConfirmationTokenParams) error
	UpdateUserEmail(ctx context.Context, arg db.UpdateUserEmailParams) error
}

// EmailsHandler handles email-related endpoints
type EmailsHandler struct {
	queries emailQuerier
	cfg     *config.Config
	mail    mailer.Deliverer
}

// NewEmailsHandler creates a new emails handler
func NewEmailsHandler(queries emailQuerier) *EmailsHandler {
	return &EmailsHandler{queries: queries}
}

// SetMailDeliverer enables sending of confirmation instructions.
func (h *EmailsHandler) SetMailDeliverer(cfg *config.Config, deliverer mailer.Deliverer) {
	if h == nil {
		return
	}
	h.cfg = cfg
	h.mail = deliverer
}

// Confirmations handles POST /api/v1/emails/confirmations
// This resends the confirmation email, optionally updating the address first.
func (h *EmailsHandler) Confirmations(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Email confirmation unavailable")
		return
	}

	var req struct {
		Email string `json:"email" form:"email"`
	}
	if r.ContentLength != 0 {
		if err := web.Bind(r, &req); err != nil {
			web.JSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
	}

	ctx := r.Context()

	user, err := h.queries.GetUserByAccountID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if user.ConfirmedAt.Valid {
		web.JSONError(w, http.StatusForbidden, "This method is only available while the e-mail is awaiting confirmation")
		return
	}

	email := user.Email
	if req.Email != "" {
		if !auth.ValidateEmail(req.Email) {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Email is invalid")
			return
		}
		if err := h.queries.UpdateUserEmail(ctx, db.UpdateUserEmailParams{
			ID:    user.ID,
			Email: req.Email,
		}); err != nil {
			// Unique constraint violation (code 23505)
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Email has already been taken")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Failed to update email")
			return
		}
		email = req.Email
	}

	token, err := auth.GenerateConfirmationToken()
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to generate confirmation token")
		return
	}

	if err := h.queries.UpdateUserConfirmationToken(ctx, db.UpdateUserConfirmationTokenParams{
		ID:                user.ID,
		ConfirmationToken: pgtype.Text{String: token, Valid: true},
	}); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to update confirmation")
		return
	}

	if h.mail != nil {
		locale := ""
		if user.Locale.Valid {
			locale = user.Locale.String
		}
		if err := h.mail.Deliver(ctx, confirmationMail(h.cfg, email, locale, token, user.Approved)); err != nil {
			slog.Error("Failed to enqueue confirmation mail", "error", err)
		}
	}

	web.JSON(w, http.StatusOK, map[string]interface{}{})
}

// CheckConfirmation handles GET /api/v1/emails/check_confirmation
// This checks if the user's email is confirmed
func (h *EmailsHandler) CheckConfirmation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Email confirmation unavailable")
		return
	}

	ctx := r.Context()
	user, err := h.queries.GetUserByAccountID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	web.JSON(w, http.StatusOK, map[string]bool{"confirmed": user.ConfirmedAt.Valid})
}

// RegisterRoutes registers the emails routes
func (h *EmailsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/emails/confirmations", h.Confirmations)
	mux.HandleFunc("GET /api/v1/emails/check_confirmation", h.CheckConfirmation)
}
