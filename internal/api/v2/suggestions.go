// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// SuggestionsHandler handles v2 suggestion endpoints
type SuggestionsHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewSuggestionsHandler creates a new suggestions handler
func NewSuggestionsHandler(queries *db.Queries, cfg *config.Config) *SuggestionsHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &SuggestionsHandler{queries: queries, cfg: cfg}
}

// SuggestionV2 represents a v2 follow suggestion
type SuggestionV2 struct {
	Source  string     `json:"source"` // staff, past_interactions, global
	Sources []string   `json:"sources"`
	Account v1.Account `json:"account"`
}

// Get handles GET /api/v2/suggestions
func (h *SuggestionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

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

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Suggestions unavailable")
		return
	}

	suppressed, err := h.queries.IsFollowRecommendationSuppressed(r.Context(), accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if suppressed {
		web.JSON(w, http.StatusOK, []SuggestionV2{})
		return
	}

	accounts, err := h.queries.GetFollowSuggestions(r.Context(), db.GetFollowSuggestionsParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	serialized := v1.SerializeAccounts(r.Context(), h.queries, h.cfg, accounts, true)
	suggestions := make([]SuggestionV2, 0, len(accounts))
	for _, account := range accounts {
		apiAcc, ok := serialized[account.ID]
		if !ok {
			apiAcc = v1.SerializeAccount(h.cfg, account)
		}
		suggestions = append(suggestions, SuggestionV2{
			Source:  suggestionLegacySource,
			Sources: []string{suggestionSourceMostFollowed},
			Account: apiAcc,
		})
	}

	web.JSON(w, http.StatusOK, suggestions)
}

// Delete handles DELETE /api/v2/suggestions/:account_id
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

	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Suggestions unavailable")
		return
	}

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

	web.JSON(w, http.StatusOK, map[string]any{})
}

const (
	suggestionSourceMostFollowed = "most_followed"
	suggestionLegacySource       = "global"
)

// RegisterRoutes registers suggestion routes
func (h *SuggestionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/suggestions", h.Get)
	mux.HandleFunc("DELETE /api/v2/suggestions/{account_id}", h.Delete)
}
