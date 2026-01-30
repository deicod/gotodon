// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

type profileQuerier interface {
	accountCredentialQueries
	ClearAccountAvatar(ctx context.Context, id int64) (db.Account, error)
	ClearAccountHeader(ctx context.Context, id int64) (db.Account, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
}

// ProfileHandler handles profile management endpoints
type ProfileHandler struct {
	queries profileQuerier
	cfg     *config.Config
}

// NewProfileHandler creates a new profile handler
func NewProfileHandler(queries profileQuerier, cfg *config.Config) *ProfileHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &ProfileHandler{queries: queries, cfg: cfg}
}

// DeleteAvatar handles DELETE /api/v1/profile/avatar
func (h *ProfileHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Profile data unavailable")
		return
	}

	ctx := r.Context()

	_, err := h.queries.ClearAccountAvatar(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to delete avatar")
		return
	}

	dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
		return
	}

	web.JSON(w, http.StatusOK, h.dbAccountToCredential(ctx, dbAcc))
}

// DeleteHeader handles DELETE /api/v1/profile/header
func (h *ProfileHandler) DeleteHeader(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Profile data unavailable")
		return
	}

	ctx := r.Context()

	_, err := h.queries.ClearAccountHeader(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to delete header")
		return
	}

	dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
		return
	}

	web.JSON(w, http.StatusOK, h.dbAccountToCredential(ctx, dbAcc))
}

func (h *ProfileHandler) dbAccountToCredential(ctx context.Context, dbAcc db.Account) CredentialAccount {
	return buildCredentialAccount(ctx, h.queries, h.cfg, dbAcc)
}

// RegisterRoutes registers the profile routes
func (h *ProfileHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/v1/profile/avatar", h.DeleteAvatar)
	mux.HandleFunc("DELETE /api/v1/profile/header", h.DeleteHeader)
}
