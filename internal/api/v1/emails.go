// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// EmailsHandler handles email-related endpoints
type EmailsHandler struct {
	queries *db.Queries
}

// NewEmailsHandler creates a new emails handler
func NewEmailsHandler(queries *db.Queries) *EmailsHandler {
	return &EmailsHandler{queries: queries}
}

func (h *EmailsHandler) useDB() bool {
	return h.queries != nil
}

// Confirmations handles POST /api/v1/emails/confirmations
// This resends the confirmation email
func (h *EmailsHandler) Confirmations(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		// Get user by account ID
		user, err := h.queries.GetUserByAccountID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Check if already confirmed
		if user.ConfirmedAt.Valid {
			web.JSONError(w, http.StatusForbidden, "Already confirmed")
			return
		}

		// In a real implementation, we would:
		// 1. Generate a new confirmation token
		// 2. Save it to the user record
		// 3. Send the confirmation email
		// For now, return success as we don't have email sending configured
	}

	// Return empty success response
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

	ctx := r.Context()
	confirmed := true // Default to true for mock mode

	if h.useDB() {
		user, err := h.queries.GetUserByAccountID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		confirmed = user.ConfirmedAt.Valid
	}

	web.JSON(w, http.StatusOK, map[string]bool{"confirmed": confirmed})
}

// RegisterRoutes registers the emails routes
func (h *EmailsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/emails/confirmations", h.Confirmations)
	mux.HandleFunc("GET /api/v1/emails/check_confirmation", h.CheckConfirmation)
}
