// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// SuggestionsHandler handles v2 suggestion endpoints
type SuggestionsHandler struct {
	queries *db.Queries
}

// NewSuggestionsHandler creates a new suggestions handler
func NewSuggestionsHandler(queries *db.Queries) *SuggestionsHandler {
	return &SuggestionsHandler{queries: queries}
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

	_ = accountID

	limit := 40
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	// Return mock suggestions
	suggestions := h.generateSuggestions(limit)

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

	_ = accountID
	_ = suggestionAccountID

	web.JSON(w, http.StatusOK, map[string]any{})
}

func (h *SuggestionsHandler) generateSuggestions(limit int) []SuggestionV2 {
	suggestions := make([]SuggestionV2, 0)
	now := time.Now()
	discoverable := true

	sourcesList := [][]string{
		{"staff"},
		{"past_interactions", "global"},
		{"global"},
		{"past_interactions"},
		{"staff", "global"},
	}

	for i := 0; i < limit && i < 5; i++ {
		sources := sourcesList[i%len(sourcesList)]
		suggestions = append(suggestions, SuggestionV2{
			Source:  sources[0],
			Sources: sources,
			Account: v1.Account{
				ID:           strconv.Itoa(100 + i),
				Username:     "suggested_user_" + strconv.Itoa(i),
				Acct:         "suggested_user_" + strconv.Itoa(i),
				DisplayName:  "Suggested User " + strconv.Itoa(i),
				Note:         "<p>A user you might want to follow</p>",
				Discoverable: &discoverable,
				CreatedAt:    now.Add(-time.Duration(i*30) * 24 * time.Hour),
				Emojis:       []any{},
				Fields:       []v1.Field{},
			},
		})
	}

	return suggestions
}

// RegisterRoutes registers suggestion routes
func (h *SuggestionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/suggestions", h.Get)
	mux.HandleFunc("DELETE /api/v2/suggestions/{account_id}", h.Delete)
}
