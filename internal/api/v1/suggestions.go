// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// SuggestionsHandler handles suggestions endpoints
type SuggestionsHandler struct {
	queries *db.Queries
}

// NewSuggestionsHandler creates a new suggestions handler
func NewSuggestionsHandler(queries *db.Queries) *SuggestionsHandler {
	return &SuggestionsHandler{queries: queries}
}

// Suggestion represents a follow suggestion
type Suggestion struct {
	Source  string  `json:"source"` // staff, past_interactions, global
	Account Account `json:"account"`
}

// Get handles GET /api/v1/suggestions (deprecated, use v2)
func (h *SuggestionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	limit := 40
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	accounts, err := h.queries.GetFollowSuggestions(r.Context(), db.GetFollowSuggestionsParams{
		AccountID: accountID,
		Limit:     int32(limit),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := []Account{}
	for _, a := range accounts {
		discoverable := a.Discoverable.Valid && a.Discoverable.Bool
		resp = append(resp, Account{
			ID:           strconv.FormatInt(a.ID, 10),
			Username:     a.Username,
			Acct:         a.Username,
			DisplayName:  a.DisplayName,
			Note:         a.Note,
			Locked:       a.Locked,
			Discoverable: &discoverable,
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		})
	}

	web.JSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /api/v1/suggestions/:account_id
func (h *SuggestionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	suggestionAccountID := web.Param(r, "account_id")

	_ = accountID
	_ = suggestionAccountID

	// In production: remove this suggestion for the user
	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers suggestion routes
func (h *SuggestionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/suggestions", h.Get)
	mux.HandleFunc("DELETE /api/v1/suggestions/{account_id}", h.Delete)
}
