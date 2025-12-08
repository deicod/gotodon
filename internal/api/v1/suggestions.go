// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// SuggestionsHandler handles suggestion endpoints
type SuggestionsHandler struct {
	// In production, this would use database/algorithms
}

// NewSuggestionsHandler creates a new suggestions handler
func NewSuggestionsHandler() *SuggestionsHandler {
	return &SuggestionsHandler{}
}

// Suggestion represents a follow suggestion
type Suggestion struct {
	Source  string  `json:"source"` // staff, past_interactions, global
	Account Account `json:"account"`
}

// Get handles GET /api/v1/suggestions (deprecated, use v2)
func (h *SuggestionsHandler) Get(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	_ = accountID

	limit := 40
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	// Return mock suggestions
	suggestions := h.generateSuggestions(limit)

	return c.JSON(http.StatusOK, suggestions)
}

// Delete handles DELETE /api/v1/suggestions/:account_id
func (h *SuggestionsHandler) Delete(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	suggestionAccountID := c.Param("account_id")

	_ = accountID
	_ = suggestionAccountID

	// In production: remove this suggestion for the user
	return c.JSON(http.StatusOK, map[string]any{})
}

func (h *SuggestionsHandler) generateSuggestions(limit int) []Suggestion {
	suggestions := make([]Suggestion, 0)
	now := time.Now()
	discoverable := true

	sources := []string{"staff", "past_interactions", "global"}

	for i := 0; i < limit && i < 5; i++ {
		suggestions = append(suggestions, Suggestion{
			Source: sources[i%len(sources)],
			Account: Account{
				ID:           strconv.Itoa(100 + i),
				Username:     "suggested_user_" + strconv.Itoa(i),
				Acct:         "suggested_user_" + strconv.Itoa(i),
				DisplayName:  "Suggested User " + strconv.Itoa(i),
				Note:         "<p>A user you might want to follow</p>",
				Discoverable: &discoverable,
				CreatedAt:    now.Add(-time.Duration(i*30) * 24 * time.Hour),
				Emojis:       []any{},
				Fields:       []Field{},
			},
		})
	}

	return suggestions
}

// RegisterRoutes registers suggestion routes
func (h *SuggestionsHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/suggestions", h.Get)
	g.DELETE("/suggestions/:account_id", h.Delete)
}
