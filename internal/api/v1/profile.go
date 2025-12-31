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

// ProfileHandler handles profile management endpoints
type ProfileHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewProfileHandler creates a new profile handler
func NewProfileHandler(queries *db.Queries, cfg *config.Config) *ProfileHandler {
	return &ProfileHandler{queries: queries, cfg: cfg}
}

func (h *ProfileHandler) useDB() bool {
	return h.queries != nil
}

// DeleteAvatar handles DELETE /api/v1/profile/avatar
func (h *ProfileHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		// Clear avatar fields on account
		_, err := h.queries.ClearAccountAvatar(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to delete avatar")
			return
		}

		// Get updated account
		dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
			return
		}

		web.JSON(w, http.StatusOK, h.dbAccountToCredential(ctx, dbAcc))
		return
	}

	// Mock fallback
	web.JSON(w, http.StatusOK, h.mockCredentialAccount())
}

// DeleteHeader handles DELETE /api/v1/profile/header
func (h *ProfileHandler) DeleteHeader(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		// Clear header fields on account
		_, err := h.queries.ClearAccountHeader(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to delete header")
			return
		}

		// Get updated account
		dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
			return
		}

		web.JSON(w, http.StatusOK, h.dbAccountToCredential(ctx, dbAcc))
		return
	}

	// Mock fallback
	web.JSON(w, http.StatusOK, h.mockCredentialAccount())
}

func (h *ProfileHandler) dbAccountToCredential(ctx context.Context, dbAcc db.Account) CredentialAccount {
	if h.queries == nil {
		return h.mockCredentialAccount()
	}
	return buildCredentialAccount(ctx, h.queries, h.cfg, dbAcc)
}

func (h *ProfileHandler) mockCredentialAccount() CredentialAccount {
	discoverable := true
	return CredentialAccount{
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			Avatar:       "https://example.com/avatars/original/missing.png",
			AvatarStatic: "https://example.com/avatars/original/missing.png",
			Header:       "https://example.com/headers/original/missing.png",
			HeaderStatic: "https://example.com/headers/original/missing.png",
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		},
		Source: AccountSource{
			Privacy:            "public",
			Sensitive:          false,
			Language:           "en",
			Fields:             []Field{},
			HideCollections:    false,
			Discoverable:       true,
			Indexable:          true,
			AttributionDomains: []string{},
			QuotePolicy:        "public",
		},
	}
}

// RegisterRoutes registers the profile routes
func (h *ProfileHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/v1/profile/avatar", h.DeleteAvatar)
	mux.HandleFunc("DELETE /api/v1/profile/header", h.DeleteHeader)
}
