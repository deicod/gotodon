// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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
				Emojis:       []CustomEmoji{},
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
func (h *TimelinesHandler) Home(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	limit := parseLimit(web.QueryParam(r, "limit"), 20, 40)
	ctx := r.Context()

	if h.useDB() {
		dbStatuses, err := h.queries.GetHomeTimeline(ctx, db.GetHomeTimelineParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}
		web.JSON(w, http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
		return
	}

	web.JSON(w, http.StatusOK, []Status{})
}

// Public handles GET /api/v1/timelines/public
func (h *TimelinesHandler) Public(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(web.QueryParam(r, "limit"), 20, 40)
	local := web.QueryParam(r, "local") == "true"

	ctx := r.Context()

	if h.useDB() {
		dbStatuses, err := h.queries.GetPublicTimeline(ctx, db.GetPublicTimelineParams{
			Limit:  int32(limit),
			Offset: 0,
			Local:  local,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}
		web.JSON(w, http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
		return
	}

	web.JSON(w, http.StatusOK, []Status{})
}

// Hashtag handles GET /api/v1/timelines/tag/:hashtag
func (h *TimelinesHandler) Hashtag(w http.ResponseWriter, r *http.Request) {
	hashtag := web.Param(r, "hashtag")
	if hashtag == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseLimit(web.QueryParam(r, "limit"), 20, 40)
	local := web.QueryParam(r, "local") == "true"

	ctx := r.Context()

	if h.useDB() {
		dbStatuses, err := h.queries.GetHashtagTimeline(ctx, db.GetHashtagTimelineParams{
			Tag:    hashtag,
			Limit:  int32(limit),
			Offset: 0,
			Local:  local,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}
		web.JSON(w, http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
		return
	}

	web.JSON(w, http.StatusOK, []Status{})
}

// List handles GET /api/v1/timelines/list/:list_id
func (h *TimelinesHandler) List(w http.ResponseWriter, r *http.Request) {
	listIDStr := web.Param(r, "list_id")
	if listIDStr == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	listID, err := strconv.ParseInt(listIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	limit := parseLimit(web.QueryParam(r, "limit"), 20, 40)

	if h.useDB() {
		// Verify list ownership
		_, err := h.queries.GetList(ctx, db.GetListParams{
			ID:        listID,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		dbStatuses, err := h.queries.GetListTimeline(ctx, db.GetListTimelineParams{
			ListID: listID,
			Limit:  int32(limit),
			Offset: 0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}
		web.JSON(w, http.StatusOK, h.dbStatusesToAPI(ctx, dbStatuses))
		return
	}

	web.JSON(w, http.StatusOK, []Status{})
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
func (h *TimelinesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/timelines/home", h.Home)
	mux.HandleFunc("GET /api/v1/timelines/public", h.Public)
	mux.HandleFunc("GET /api/v1/timelines/tag/{hashtag}", h.Hashtag)
	mux.HandleFunc("GET /api/v1/timelines/list/{list_id}", h.List)
}
