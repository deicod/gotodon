// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// TimelinesHandler handles timeline endpoints
type TimelinesHandler struct {
	// TODO: Add database queries dependency
}

// NewTimelinesHandler creates a new timelines handler
func NewTimelinesHandler() *TimelinesHandler {
	return &TimelinesHandler{}
}

// Home handles GET /api/v1/timelines/home
func (h *TimelinesHandler) Home(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Parse pagination params
	limit := parseLimit(c.QueryParam("limit"), 20, 40)
	maxID := c.QueryParam("max_id")
	sinceID := c.QueryParam("since_id")
	minID := c.QueryParam("min_id")

	// TODO: Fetch home timeline from database
	_ = accountID
	_ = maxID
	_ = sinceID
	_ = minID

	// Mock empty timeline
	statuses := make([]Status, 0, limit)

	return c.JSON(http.StatusOK, statuses)
}

// Public handles GET /api/v1/timelines/public
func (h *TimelinesHandler) Public(c echo.Context) error {
	// Parse params
	limit := parseLimit(c.QueryParam("limit"), 20, 40)
	local := c.QueryParam("local") == "true"
	remote := c.QueryParam("remote") == "true"
	onlyMedia := c.QueryParam("only_media") == "true"
	maxID := c.QueryParam("max_id")
	sinceID := c.QueryParam("since_id")
	minID := c.QueryParam("min_id")

	// TODO: Fetch public timeline from database
	_ = local
	_ = remote
	_ = onlyMedia
	_ = maxID
	_ = sinceID
	_ = minID

	statuses := make([]Status, 0, limit)

	return c.JSON(http.StatusOK, statuses)
}

// Hashtag handles GET /api/v1/timelines/tag/:hashtag
func (h *TimelinesHandler) Hashtag(c echo.Context) error {
	hashtag := c.Param("hashtag")
	if hashtag == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	limit := parseLimit(c.QueryParam("limit"), 20, 40)
	local := c.QueryParam("local") == "true"
	onlyMedia := c.QueryParam("only_media") == "true"
	maxID := c.QueryParam("max_id")
	sinceID := c.QueryParam("since_id")
	minID := c.QueryParam("min_id")

	// TODO: Fetch hashtag timeline from database
	_ = hashtag
	_ = local
	_ = onlyMedia
	_ = maxID
	_ = sinceID
	_ = minID

	statuses := make([]Status, 0, limit)

	return c.JSON(http.StatusOK, statuses)
}

// List handles GET /api/v1/timelines/list/:list_id
func (h *TimelinesHandler) List(c echo.Context) error {
	listID := c.Param("list_id")
	if listID == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Verify list ownership and fetch from database
	_ = accountID
	_ = listID

	limit := parseLimit(c.QueryParam("limit"), 20, 40)
	statuses := make([]Status, 0, limit)

	return c.JSON(http.StatusOK, statuses)
}

// parseLimit parses a limit query param with defaults and max
func parseLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

// RegisterRoutes registers the timelines routes
func (h *TimelinesHandler) RegisterRoutes(g *echo.Group) {
	timelines := g.Group("/timelines")
	timelines.GET("/public", h.Public)
	timelines.GET("/tag/:hashtag", h.Hashtag)
	// Home and list require auth, registered separately
}
