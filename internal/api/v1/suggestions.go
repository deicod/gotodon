// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
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
func (h *SuggestionsHandler) Get(c echo.Context) error {
	limit := 40
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	if h.queries == nil {
		return c.JSON(http.StatusOK, []Account{})
	}

	accounts, err := h.queries.GetFollowSuggestions(c.Request().Context(), db.GetFollowSuggestionsParams{
		AccountID: accountID,
		Limit:     int32(limit),
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
			Emojis:       []any{},
			Fields:       []Field{},
		})
	}

	return c.JSON(http.StatusOK, resp)
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
