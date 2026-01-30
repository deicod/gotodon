// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// TagsHandler handles tag-related endpoints
type TagsHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewTagsHandler creates a new tags handler
func NewTagsHandler(queries *db.Queries, cfg *config.Config) *TagsHandler {
	return &TagsHandler{queries: queries, cfg: cfg}
}

func (h *TagsHandler) useDB() bool {
	return h.queries != nil
}

// TagResponse represents a tag in API responses
type TagResponse struct {
	Name      string        `json:"name"`
	URL       string        `json:"url"`
	History   []TagDayCount `json:"history"`
	Following *bool         `json:"following,omitempty"`
}

// TagDayCount represents usage stats for a single day
type TagDayCount struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

// dbTagToAPI converts a database Tag to API TagResponse
func (h *TagsHandler) dbTagToAPI(tag db.Tag, following bool, history []TagDayCount) TagResponse {
	url := "https://" + h.cfg.Instance.LocalDomain + "/tags/" + tag.Name
	if history == nil {
		history = []TagDayCount{}
	}
	return TagResponse{
		Name:      tag.Name,
		URL:       url,
		History:   history,
		Following: &following,
	}
}

// Get handles GET /api/v1/tags/:id
func (h *TagsHandler) Get(w http.ResponseWriter, r *http.Request) {
	tagName := web.Param(r, "id")
	if tagName == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Tags unavailable")
		return
	}

	// Get tag by name
	tag, err := h.queries.GetTagByName(ctx, tagName)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// Check if the authenticated user follows this tag
	following := false
	if accountID, ok := middleware.GetAccountID(ctx); ok {
		isFollowing, err := h.queries.IsFollowingTag(ctx, db.IsFollowingTagParams{
			AccountID: accountID,
			TagID:     tag.ID,
		})
		if err == nil && isFollowing {
			following = true
		}
	}

	history := tagHistoryForIDs(ctx, h.queries, []int64{tag.ID})[tag.ID]
	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, following, history))
}

// Follow handles POST /api/v1/tags/:id/follow
func (h *TagsHandler) Follow(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	tagName := web.Param(r, "id")
	if tagName == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Tags unavailable")
		return
	}

	// Get or create tag
	tag, err := h.queries.GetTagByName(ctx, tagName)
	if err != nil {
		// Tag doesn't exist, create it
		tag, err = h.queries.CreateTag(ctx, tagName)
		if err != nil {
			// Try one more get in case of race condition
			tag, err = h.queries.GetTagByName(ctx, tagName)
			if err != nil {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
		}
	}

	// Create tag follow
	_, err = h.queries.CreateTagFollow(ctx, db.CreateTagFollowParams{
		AccountID: accountID,
		TagID:     tag.ID,
	})
	// Ignore duplicate key errors - user is already following
	// But report other errors
	if err != nil {
		// Check if it's a duplicate key violation (user already following)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Duplicate - that's fine, user is already following
		} else {
			web.JSONError(w, http.StatusInternalServerError, "Failed to follow tag")
			return
		}
	}

	history := tagHistoryForIDs(ctx, h.queries, []int64{tag.ID})[tag.ID]
	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, true, history))
}

// Unfollow handles POST /api/v1/tags/:id/unfollow
func (h *TagsHandler) Unfollow(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	tagName := web.Param(r, "id")
	if tagName == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Tags unavailable")
		return
	}

	// Get tag
	tag, err := h.queries.GetTagByName(ctx, tagName)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// Delete tag follow
	err = h.queries.DeleteTagFollow(ctx, db.DeleteTagFollowParams{
		AccountID: accountID,
		TagID:     tag.ID,
	})
	// DeleteTagFollow is :exec so doesn't return error for "not found"
	// But we should still check for actual DB errors
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to unfollow tag")
		return
	}

	history := tagHistoryForIDs(ctx, h.queries, []int64{tag.ID})[tag.ID]
	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, false, history))
}

// ListFollowed handles GET /api/v1/followed_tags
func (h *TagsHandler) ListFollowed(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	// Parse pagination
	limit := int32(20)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 32); err == nil && parsed > 0 && parsed <= 200 {
			limit = int32(parsed)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.ParseInt(o, 10, 32); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Tags unavailable")
		return
	}

	tags, err := h.queries.ListFollowedTags(ctx, db.ListFollowedTagsParams{
		AccountID: accountID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to list followed tags")
		return
	}

	tagIDs := make([]int64, 0, len(tags))
	for _, tag := range tags {
		tagIDs = append(tagIDs, tag.ID)
	}
	histories := tagHistoryForIDs(ctx, h.queries, tagIDs)

	result := make([]TagResponse, len(tags))
	for i, tag := range tags {
		result[i] = h.dbTagToAPI(tag, true, histories[tag.ID])
	}

	web.JSON(w, http.StatusOK, result)
}

// Feature handles POST /api/v1/tags/:id/feature (for personal featured tags)
func (h *TagsHandler) Feature(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	tagName := web.Param(r, "id")
	if tagName == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Tags unavailable")
		return
	}

	// Get or create tag
	tag, err := h.queries.GetTagByName(ctx, tagName)
	if err != nil {
		tag, err = h.queries.CreateTag(ctx, tagName)
		if err != nil {
			tag, err = h.queries.GetTagByName(ctx, tagName)
			if err != nil {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
		}
	}

	// Create featured tag
	_, err = h.queries.CreateFeaturedTag(ctx, db.CreateFeaturedTagParams{
		AccountID:     accountID,
		TagID:         pgtype.Int8{Int64: tag.ID, Valid: true},
		Name:          tag.Name,
		StatusesCount: pgtype.Int8{Int64: 0, Valid: true},
		LastStatusAt:  pgtype.Timestamptz{Valid: false},
	})
	if err != nil {
		// Check if it's a duplicate key (already featured)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Already featured - that's fine
		} else {
			web.JSONError(w, http.StatusInternalServerError, "Failed to feature tag")
			return
		}
	}

	// Return tag with following status
	following := false
	isFollowing, err := h.queries.IsFollowingTag(ctx, db.IsFollowingTagParams{
		AccountID: accountID,
		TagID:     tag.ID,
	})
	if err == nil && isFollowing {
		following = true
	}

	history := tagHistoryForIDs(ctx, h.queries, []int64{tag.ID})[tag.ID]
	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, following, history))
}

// Unfeature handles POST /api/v1/tags/:id/unfeature
func (h *TagsHandler) Unfeature(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	tagName := web.Param(r, "id")
	if tagName == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Tags unavailable")
		return
	}

	// Get tag
	tag, err := h.queries.GetTagByName(ctx, tagName)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	// Delete featured tag
	err = h.queries.DeleteFeaturedTagByTagID(ctx, db.DeleteFeaturedTagByTagIDParams{
		AccountID: accountID,
		TagID:     pgtype.Int8{Int64: tag.ID, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to unfeature tag")
		return
	}

	// Return tag with following status
	following := false
	isFollowing, err := h.queries.IsFollowingTag(ctx, db.IsFollowingTagParams{
		AccountID: accountID,
		TagID:     tag.ID,
	})
	if err == nil && isFollowing {
		following = true
	}

	history := tagHistoryForIDs(ctx, h.queries, []int64{tag.ID})[tag.ID]
	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, following, history))
}

// RegisterRoutes registers the tags routes
func (h *TagsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/tags/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/tags/{id}/follow", h.Follow)
	mux.HandleFunc("POST /api/v1/tags/{id}/unfollow", h.Unfollow)
	mux.HandleFunc("POST /api/v1/tags/{id}/feature", h.Feature)
	mux.HandleFunc("POST /api/v1/tags/{id}/unfeature", h.Unfeature)
	mux.HandleFunc("GET /api/v1/followed_tags", h.ListFollowed)
}

type tagHistoryQuerier interface {
	ListTagHistories(ctx context.Context, tagIds []int64) ([]db.ListTagHistoriesRow, error)
}

func tagHistoryForIDs(ctx context.Context, q tagHistoryQuerier, tagIDs []int64) map[int64][]TagDayCount {
	history := make(map[int64][]TagDayCount, len(tagIDs))
	if q == nil || len(tagIDs) == 0 {
		return history
	}

	rows, err := q.ListTagHistories(ctx, tagIDs)
	if err != nil {
		return history
	}

	for _, row := range rows {
		if !row.Day.Valid {
			continue
		}
		history[row.TagID] = append(history[row.TagID], TagDayCount{
			Day:      strconv.FormatInt(row.Day.Time.Unix(), 10),
			Uses:     strconv.FormatInt(row.Uses, 10),
			Accounts: strconv.FormatInt(row.Accounts, 10),
		})
	}

	return history
}
