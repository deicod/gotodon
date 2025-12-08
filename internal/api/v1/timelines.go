// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// TimelinesHandler handles timeline endpoints
type TimelinesHandler struct {
	queries *db.Queries
}

// NewTimelinesHandler creates a new timelines handler
func NewTimelinesHandler(queries *db.Queries) *TimelinesHandler {
	return &TimelinesHandler{queries: queries}
}

func (h *TimelinesHandler) useDB() bool {
	return h.queries != nil
}

func (h *TimelinesHandler) dbStatusesToAPI(ctx context.Context, dbStatuses []db.Status) []Status {
	statuses := make([]Status, len(dbStatuses))
	for i, s := range dbStatuses {
		statuses[i] = h.dbStatusToAPI(ctx, s)
	}
	return statuses
}

func (h *TimelinesHandler) dbStatusToAPI(ctx context.Context, dbStatus db.Status) Status {
	visibility := visibilityToString[dbStatus.Visibility]
	if visibility == "" {
		visibility = "public"
	}

	var url *string
	if dbStatus.Url.Valid {
		url = &dbStatus.Url.String
	}

	var inReplyToID, inReplyToAccountID *string
	if dbStatus.InReplyToID.Valid {
		s := strconv.FormatInt(dbStatus.InReplyToID.Int64, 10)
		inReplyToID = &s
	}
	if dbStatus.InReplyToAccountID.Valid {
		s := strconv.FormatInt(dbStatus.InReplyToAccountID.Int64, 10)
		inReplyToAccountID = &s
	}

	var language *string
	if dbStatus.Language.Valid {
		language = &dbStatus.Language.String
	}

	// Load stats
	var repliesCount, reblogsCount, favouritesCount int64
	if h.useDB() {
		stats, err := h.queries.GetStatusStats(ctx, dbStatus.ID)
		if err == nil {
			repliesCount = stats.RepliesCount
			reblogsCount = stats.ReblogsCount
			favouritesCount = stats.FavouritesCount
		}
	}

	// Load account
	var account Account
	if h.useDB() {
		dbAcc, err := h.queries.GetAccountByID(ctx, dbStatus.AccountID)
		if err == nil {
			discoverable := dbAcc.Discoverable.Valid && dbAcc.Discoverable.Bool
			account = Account{
				ID:           strconv.FormatInt(dbAcc.ID, 10),
				Username:     dbAcc.Username,
				Acct:         dbAcc.Username,
				DisplayName:  dbAcc.DisplayName,
				Discoverable: &discoverable,
				Emojis:       []any{},
				Fields:       []Field{},
			}
			if dbAcc.CreatedAt.Valid {
				account.CreatedAt = dbAcc.CreatedAt.Time
			}
		}
	}

	uri := ""
	if dbStatus.Uri.Valid {
		uri = dbStatus.Uri.String
	}

	status := Status{
		ID:                 strconv.FormatInt(dbStatus.ID, 10),
		InReplyToID:        inReplyToID,
		InReplyToAccountID: inReplyToAccountID,
		Sensitive:          dbStatus.Sensitive,
		SpoilerText:        dbStatus.SpoilerText,
		Visibility:         visibility,
		Language:           language,
		URI:                uri,
		URL:                url,
		RepliesCount:       repliesCount,
		ReblogsCount:       reblogsCount,
		FavouritesCount:    favouritesCount,
		Content:            "<p>" + dbStatus.Text + "</p>",
		Account:            account,
		MediaAttachments:   []MediaAttach{},
		Mentions:           []Mention{},
		Tags:               []Tag{},
		Emojis:             []any{},
	}

	if dbStatus.CreatedAt.Valid {
		status.CreatedAt = dbStatus.CreatedAt.Time
	}
	if dbStatus.EditedAt.Valid {
		status.EditedAt = &dbStatus.EditedAt.Time
	}

	return status
}

// Home handles GET /api/v1/timelines/home
func (h *TimelinesHandler) Home(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	limit := parseLimit(c.QueryParam("limit"), 20, 40)
	ctx := context.Background()

	if h.useDB() {
		dbStatuses, err := h.queries.GetHomeTimeline(ctx, db.GetHomeTimelineParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Status{})
		}
		return c.JSON(http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
	}

	return c.JSON(http.StatusOK, []Status{})
}

// Public handles GET /api/v1/timelines/public
func (h *TimelinesHandler) Public(c echo.Context) error {
	limit := parseLimit(c.QueryParam("limit"), 20, 40)
	local := c.QueryParam("local") == "true"

	ctx := context.Background()

	if h.useDB() {
		dbStatuses, err := h.queries.GetPublicTimeline(ctx, db.GetPublicTimelineParams{
			Limit:  int32(limit),
			Offset: 0,
			Local:  local,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Status{})
		}
		return c.JSON(http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
	}

	return c.JSON(http.StatusOK, []Status{})
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

	ctx := context.Background()

	if h.useDB() {
		dbStatuses, err := h.queries.GetHashtagTimeline(ctx, db.GetHashtagTimelineParams{
			Tag:    hashtag,
			Limit:  int32(limit),
			Offset: 0,
			Local:  local,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Status{})
		}
		return c.JSON(http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
	}

	return c.JSON(http.StatusOK, []Status{})
}

// List handles GET /api/v1/timelines/list/:list_id
func (h *TimelinesHandler) List(c echo.Context) error {
	listIDStr := c.Param("list_id")
	if listIDStr == "" {
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

	listID, err := strconv.ParseInt(listIDStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	ctx := context.Background()

	limit := parseLimit(c.QueryParam("limit"), 20, 40)

	if h.useDB() {
		// Verify list ownership
		_, err := h.queries.GetList(ctx, db.GetListParams{
			ID:        listID,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}

		dbStatuses, err := h.queries.GetListTimeline(ctx, db.GetListTimelineParams{
			ListID: listID,
			Limit:  int32(limit),
			Offset: 0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Status{})
		}
		return c.JSON(http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
	}

	return c.JSON(http.StatusOK, []Status{})
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
}
