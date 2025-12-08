// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/db"
	"github.com/labstack/echo/v4"
)

// TrendsHandler handles trends endpoints
type TrendsHandler struct {
	queries *db.Queries
}

// NewTrendsHandler creates a new trends handler
func NewTrendsHandler(queries *db.Queries) *TrendsHandler {
	return &TrendsHandler{queries: queries}
}

// TrendingTag represents a trending hashtag
type TrendingTag struct {
	Name      string         `json:"name"`
	URL       string         `json:"url"`
	History   []HashtagUsage `json:"history"`
	Following bool           `json:"following,omitempty"`
}

// HashtagUsage represents usage statistics for a hashtag
type HashtagUsage struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

// TrendingStatus represents a trending status
type TrendingStatus struct {
	Status
}

// TrendingLink represents a trending link
type TrendingLink struct {
	URL          string         `json:"url"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Type         string         `json:"type"`
	AuthorName   string         `json:"author_name"`
	AuthorURL    string         `json:"author_url"`
	ProviderName string         `json:"provider_name"`
	ProviderURL  string         `json:"provider_url"`
	HTML         string         `json:"html"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	Image        *string        `json:"image"`
	Blurhash     *string        `json:"blurhash"`
	History      []HashtagUsage `json:"history"`
}

// Tags handles GET /api/v1/trends/tags
func (h *TrendsHandler) Tags(c echo.Context) error {
	limit := parseTrendLimit(c.QueryParam("limit"), 10, 20)

	if h.queries == nil {
		return c.JSON(http.StatusOK, []TrendingTag{})
	}

	tags, err := h.queries.GetTrendingTags(c.Request().Context(), int32(limit))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	resp := []TrendingTag{}
	for _, t := range tags {
		resp = append(resp, TrendingTag{
			Name: t.Name,
			URL:  "/tags/" + t.Name, // Construct proper URL
			History: []HashtagUsage{
				{
					Day:      strconv.FormatInt(time.Now().Unix(), 10),
					Uses:     strconv.FormatInt(t.UsageCount, 10),
					Accounts: strconv.FormatInt(t.AccountCount, 10),
				},
			},
		})
	}

	return c.JSON(http.StatusOK, resp)
}

// Statuses handles GET /api/v1/trends/statuses
func (h *TrendsHandler) Statuses(c echo.Context) error {
	limit := parseTrendLimit(c.QueryParam("limit"), 20, 40)

	if h.queries == nil {
		return c.JSON(http.StatusOK, []Status{})
	}

	statuses, err := h.queries.GetTrendingStatuses(c.Request().Context(), int32(limit))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	resp := []Status{}
	for _, s := range statuses {
		account := Account{
			Username:    s.AccountUsername,
			DisplayName: s.AccountDisplayName,
		}
		if s.AccountAvatar.Valid {
			account.Avatar = s.AccountAvatar.String
		}

		resp = append(resp, Status{
			ID:         strconv.FormatInt(s.ID, 10),
			Content:    s.Text,
			Account:    account,
			Visibility: "public",
			// Minimal fields
		})
	}

	return c.JSON(http.StatusOK, resp)
}

// Links handles GET /api/v1/trends/links
func (h *TrendsHandler) Links(c echo.Context) error {
	// Mock trending links - empty for now
	return c.JSON(http.StatusOK, []TrendingLink{})
}

func parseTrendLimit(s string, defaultVal, maxVal int) int {
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

// RegisterRoutes registers trends routes
func (h *TrendsHandler) RegisterRoutes(g *echo.Group) {
	trends := g.Group("/trends")
	trends.GET("/tags", h.Tags)
	trends.GET("/statuses", h.Statuses)
	trends.GET("/links", h.Links)
}
