// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
)

// SearchHandler handles search endpoints
type SearchHandler struct {
	// In production, this would use database/search engine
}

// NewSearchHandler creates a new search handler
func NewSearchHandler() *SearchHandler {
	return &SearchHandler{}
}

// SearchResult is the response for /api/v2/search
type SearchResult struct {
	Accounts []v1.Account `json:"accounts"`
	Statuses []v1.Status  `json:"statuses"`
	Hashtags []Hashtag    `json:"hashtags"`
}

// Hashtag represents a hashtag in search results
type Hashtag struct {
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

// Search handles GET /api/v2/search
func (h *SearchHandler) Search(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Search query is required",
		})
	}

	// Parse parameters
	searchType := c.QueryParam("type") // accounts, hashtags, statuses
	resolve := c.QueryParam("resolve") == "true"
	following := c.QueryParam("following") == "true"
	limit := 20
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 40 {
			limit = parsed
		}
	}
	offset := 0
	if o := c.QueryParam("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get auth context (optional for search)
	accountID, _ := middleware.GetAccountID(c)

	_ = resolve
	_ = following
	_ = accountID
	_ = offset

	result := SearchResult{
		Accounts: []v1.Account{},
		Statuses: []v1.Status{},
		Hashtags: []Hashtag{},
	}

	// Perform search based on type
	switch searchType {
	case "accounts":
		result.Accounts = h.searchAccounts(q, limit)
	case "hashtags":
		result.Hashtags = h.searchHashtags(q, limit)
	case "statuses":
		result.Statuses = h.searchStatuses(q, limit)
	default:
		// Search all types
		result.Accounts = h.searchAccounts(q, limit)
		result.Hashtags = h.searchHashtags(q, limit)
		result.Statuses = h.searchStatuses(q, limit)
	}

	return c.JSON(http.StatusOK, result)
}

func (h *SearchHandler) searchAccounts(q string, limit int) []v1.Account {
	// Mock implementation - would use database in production
	accounts := []v1.Account{}

	// Simulate finding accounts matching the query
	if len(q) >= 2 {
		now := time.Now()
		discoverable := true

		// Return a mock account that "matches"
		accounts = append(accounts, v1.Account{
			ID:           "1",
			Username:     strings.ToLower(q),
			Acct:         strings.ToLower(q),
			DisplayName:  q,
			Note:         "<p>A user matching your search</p>",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-30 * 24 * time.Hour),
			Emojis:       []any{},
			Fields:       []v1.Field{},
		})
	}

	if len(accounts) > limit {
		accounts = accounts[:limit]
	}

	return accounts
}

func (h *SearchHandler) searchHashtags(q string, limit int) []Hashtag {
	// Mock implementation
	hashtags := []Hashtag{}

	// Normalize hashtag (remove # if present)
	tag := strings.TrimPrefix(strings.ToLower(q), "#")

	if len(tag) >= 1 {
		now := time.Now()
		hashtags = append(hashtags, Hashtag{
			Name: tag,
			URL:  "https://example.com/tags/" + tag,
			History: []HashtagUsage{
				{
					Day:      strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10),
					Uses:     "10",
					Accounts: "5",
				},
				{
					Day:      strconv.FormatInt(now.Add(-48*time.Hour).Unix(), 10),
					Uses:     "8",
					Accounts: "4",
				},
			},
		})
	}

	if len(hashtags) > limit {
		hashtags = hashtags[:limit]
	}

	return hashtags
}

func (h *SearchHandler) searchStatuses(q string, limit int) []v1.Status {
	// Mock implementation
	statuses := []v1.Status{}

	if len(q) >= 3 {
		now := time.Now()
		discoverable := true

		statuses = append(statuses, v1.Status{
			ID:              "1",
			CreatedAt:       now.Add(-time.Hour),
			Content:         "<p>A status containing: " + q + "</p>",
			Visibility:      "public",
			URI:             "https://example.com/statuses/1",
			URL:             ptrStr("https://example.com/@user/1"),
			RepliesCount:    0,
			ReblogsCount:    0,
			FavouritesCount: 0,
			Account: v1.Account{
				ID:           "1",
				Username:     "user",
				Acct:         "user",
				DisplayName:  "Test User",
				Discoverable: &discoverable,
				CreatedAt:    now.Add(-24 * time.Hour),
				Emojis:       []any{},
				Fields:       []v1.Field{},
			},
			MediaAttachments: []v1.MediaAttach{},
			Mentions:         []v1.Mention{},
			Tags:             []v1.Tag{},
			Emojis:           []any{},
		})
	}

	if len(statuses) > limit {
		statuses = statuses[:limit]
	}

	return statuses
}

func ptrStr(s string) *string {
	return &s
}

// RegisterRoutes registers search routes
func (h *SearchHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/search", h.Search)
}
