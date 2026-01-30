// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// TimelinesHandler handles timeline endpoints
type TimelinesHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewTimelinesHandler creates a new timelines handler
func NewTimelinesHandler(queries db.Querier, cfg *config.Config) *TimelinesHandler {
	return &TimelinesHandler{
		queries: queries,
		cfg:     cfg,
	}
}

func (h *TimelinesHandler) useDB() bool {
	return h.queries != nil
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
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Timelines unavailable")
		return
	}

	dbStatuses, err := h.queries.GetHomeTimeline(ctx, db.GetHomeTimelineParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    0,
	})
	if err != nil {
		web.JSON(w, http.StatusOK, []Status{})
		return
	}
	web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
}

// Public handles GET /api/v1/timelines/public
func (h *TimelinesHandler) Public(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(web.QueryParam(r, "limit"), 20, 40)
	local := web.QueryParam(r, "local") == "true"

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Timelines unavailable")
		return
	}

	dbStatuses, err := h.queries.GetPublicTimeline(ctx, db.GetPublicTimelineParams{
		Limit:  int32(limit),
		Offset: 0,
		Local:  local,
	})
	if err != nil {
		web.JSON(w, http.StatusOK, []Status{})
		return
	}
	web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Timelines unavailable")
		return
	}

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
	web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Timelines unavailable")
		return
	}

	// Verify list ownership
	_, err = h.queries.GetList(ctx, db.GetListParams{
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
	web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
}

// Link handles GET /api/v1/timelines/link
func (h *TimelinesHandler) Link(w http.ResponseWriter, r *http.Request) {
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read:statuses") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	linkURL := web.QueryParam(r, "url")
	if linkURL == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseLimit(web.QueryParam(r, "limit"), 20, 40)

	maxID, maxOK := parseOptionalTimelineID(web.QueryParam(r, "max_id"))
	sinceID, sinceOK := parseOptionalTimelineID(web.QueryParam(r, "since_id"))
	minID, minOK := parseOptionalTimelineID(web.QueryParam(r, "min_id"))
	if !maxOK || !sinceOK || !minOK {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Timelines unavailable")
		return
	}

	ctx := r.Context()

	card, err := h.queries.GetPreviewCardByURL(ctx, linkURL)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	trend, err := h.queries.GetPreviewCardTrend(ctx, card.ID)
	if err != nil || !trend.Allowed {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var dbStatuses []db.Status
	if minID > 0 {
		maxParam := pgtype.Int8{}
		if maxID > 0 {
			maxParam = pgtype.Int8{Int64: maxID, Valid: true}
		}
		dbStatuses, err = h.queries.GetLinkTimelineStatusesMinID(ctx, db.GetLinkTimelineStatusesMinIDParams{
			PreviewCardID: card.ID,
			ID:            minID,
			Limit:         int32(limit),
			MaxID:         maxParam,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}
		for i, j := 0, len(dbStatuses)-1; i < j; i, j = i+1, j-1 {
			dbStatuses[i], dbStatuses[j] = dbStatuses[j], dbStatuses[i]
		}
	} else {
		maxParam := pgtype.Int8{}
		if maxID > 0 {
			maxParam = pgtype.Int8{Int64: maxID, Valid: true}
		}
		sinceParam := pgtype.Int8{}
		if sinceID > 0 {
			sinceParam = pgtype.Int8{Int64: sinceID, Valid: true}
		}
		dbStatuses, err = h.queries.GetLinkTimelineStatuses(ctx, db.GetLinkTimelineStatusesParams{
			PreviewCardID: card.ID,
			Limit:         int32(limit),
			MaxID:         maxParam,
			SinceID:       sinceParam,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}
	}

	web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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

func parseOptionalTimelineID(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

// RegisterRoutes registers the timelines routes
func (h *TimelinesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/timelines/home", h.Home)
	mux.HandleFunc("GET /api/v1/timelines/public", h.Public)
	mux.HandleFunc("GET /api/v1/timelines/link", h.Link)
	mux.HandleFunc("GET /api/v1/timelines/tag/{hashtag}", h.Hashtag)
	mux.HandleFunc("GET /api/v1/timelines/list/{list_id}", h.List)
}
