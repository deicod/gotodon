// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

type emailQuerier interface {
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	UpdateUserConfirmationToken(ctx context.Context, arg db.UpdateUserConfirmationTokenParams) error
}

// EmailsHandler handles email-related endpoints
type EmailsHandler struct {
	queries emailQuerier
}

// NewEmailsHandler creates a new emails handler
func NewEmailsHandler(queries emailQuerier) *EmailsHandler {
	return &EmailsHandler{queries: queries}
}

// Confirmations handles POST /api/v1/emails/confirmations
// This resends the confirmation email
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

	ctx := r.Context()

	user, err := h.queries.GetUserByAccountID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if user.ConfirmedAt.Valid {
		web.JSONError(w, http.StatusForbidden, "Already confirmed")
		return
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
