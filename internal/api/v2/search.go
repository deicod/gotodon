// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// SearchHandler handles search endpoints
type SearchHandler struct {
	queries *db.Queries
}

// NewSearchHandler creates a new search handler
func NewSearchHandler(queries *db.Queries) *SearchHandler {
	return &SearchHandler{queries: queries}
}

func (h *SearchHandler) useDB() bool {
	return h.queries != nil
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
	_ = accountID // Used for "following" logic in future

	result := SearchResult{
		Accounts: []v1.Account{},
		Statuses: []v1.Status{},
		Hashtags: []Hashtag{},
	}

	ctx := c.Request().Context()

	if !h.useDB() {
		return c.JSON(http.StatusOK, result)
	}

	// Perform search based on type

	if searchType == "accounts" || searchType == "" {
		accounts, err := h.queries.SearchAccountsV2(ctx, db.SearchAccountsV2Params{
			Query:  q,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err == nil {
			for _, a := range accounts {
				discoverable := a.Discoverable.Valid && a.Discoverable.Bool
				result.Accounts = append(result.Accounts, v1.Account{
					ID:           strconv.FormatInt(a.ID, 10),
					Username:     a.Username,
					Acct:         a.Username, // Should be domain aware
					DisplayName:  a.DisplayName,
					Note:         a.Note,
					Locked:       a.Locked,
					Discoverable: &discoverable,
					Emojis:       []any{},
					Fields:       []v1.Field{},
				})
			}
		}
	}

	if searchType == "hashtags" || searchType == "" {
		hashtags, err := h.queries.SearchHashtags(ctx, db.SearchHashtagsParams{
			Query:  pgtype.Text{String: q, Valid: true},
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err == nil {
			for _, t := range hashtags {
				result.Hashtags = append(result.Hashtags, Hashtag{
					Name:    t.Name,
					URL:     "/tags/" + t.Name, // Construct proper URL
					History: []HashtagUsage{},
				})
			}
		}
	}

	if searchType == "statuses" || searchType == "" {
		statuses, err := h.queries.SearchStatuses(ctx, db.SearchStatusesParams{
			Query:  q,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err == nil {
			for _, s := range statuses {
				account := v1.Account{
					Username:    s.AccountUsername,
					DisplayName: s.AccountDisplayName,
					// Basic account info, could be enriched
				}
				if s.AccountAvatar.Valid {
					account.Avatar = s.AccountAvatar.String
				}

				result.Statuses = append(result.Statuses, v1.Status{
					ID:         strconv.FormatInt(s.ID, 10),
					Content:    s.Text,
					Account:    account,
					Visibility: "public", // Filtered by query
					// Fill strictly necessary fields
				})
			}
		}
	}

	return c.JSON(http.StatusOK, result)
}

func (h *SearchHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/search", h.Search)
}
