// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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
		web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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
		web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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
		web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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
		web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses))
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
