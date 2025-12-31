// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// SuggestionsHandler handles suggestions endpoints
type SuggestionsHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewSuggestionsHandler creates a new suggestions handler
func NewSuggestionsHandler(queries *db.Queries, cfg *config.Config) *SuggestionsHandler {
	return &SuggestionsHandler{queries: queries, cfg: cfg}
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
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
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
		Offset:    int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	serialized := serializeAccounts(r.Context(), h.queries, h.cfg, accounts, true)
	resp := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if apiAcc, ok := serialized[account.ID]; ok {
			resp = append(resp, apiAcc)
			continue
		}
		resp = append(resp, SerializeAccount(h.cfg, account))
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
	targetID, err := strconv.ParseInt(suggestionAccountID, 10, 64)
	if err != nil || targetID <= 0 {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.queries != nil {
		err = h.queries.CreateFollowRecommendationMute(r.Context(), db.CreateFollowRecommendationMuteParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}

	// In production: remove this suggestion for the user
	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers suggestion routes
func (h *SuggestionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/suggestions", h.Get)
	mux.HandleFunc("DELETE /api/v1/suggestions/{account_id}", h.Delete)
}
