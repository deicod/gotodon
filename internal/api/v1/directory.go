// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/db"
)

// DirectoryHandler handles the profile directory endpoint
type DirectoryHandler struct {
	queries *db.Queries
}

// NewDirectoryHandler creates a new directory handler
func NewDirectoryHandler(queries *db.Queries) *DirectoryHandler {
	return &DirectoryHandler{queries: queries}
}

func (h *DirectoryHandler) useDB() bool {
	return h.queries != nil
}

// Get handles GET /api/v1/directory
func (h *DirectoryHandler) Get(c echo.Context) error {
	limit := parseDirectoryLimit(c.QueryParam("limit"), 40, 80)
	offset := 0
	if o := c.QueryParam("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	local := c.QueryParam("local") == "true"
	order := c.QueryParam("order") // "active" or "new"

	_ = offset
	_ = local
	_ = order

	if h.useDB() {
		accounts, err := h.queries.ListLocalAccounts(c.Request().Context(), db.ListLocalAccountsParams{
			Limit:  int32(limit),
			Offset: 0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Account{})
		}

		result := make([]Account, len(accounts))
		for i, acc := range accounts {
			discoverable := acc.Discoverable.Valid && acc.Discoverable.Bool
			result[i] = Account{
				ID:           strconv.FormatInt(acc.ID, 10),
				Username:     acc.Username,
				Acct:         acc.Username,
				DisplayName:  acc.DisplayName,
				Note:         acc.Note,
				Locked:       acc.Locked,
				Bot:          false,
				Discoverable: &discoverable,
				Emojis:       []any{},
				Fields:       []Field{},
			}
			if acc.CreatedAt.Valid {
				result[i].CreatedAt = acc.CreatedAt.Time
			}
		}
		return c.JSON(http.StatusOK, result)
	}

	// Mock response
	now := time.Now()
	discoverable := true
	return c.JSON(http.StatusOK, []Account{
		{
			ID:           "1",
			Username:     "admin",
			Acct:         "admin",
			DisplayName:  "Administrator",
			Note:         "<p>Server administrator</p>",
			Locked:       false,
			Bot:          false,
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-365 * 24 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		},
	})
}

func parseDirectoryLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// RegisterRoutes registers directory routes
func (h *DirectoryHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/directory", h.Get)
}
