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

// TagsHandler handles tag-related endpoints
type TagsHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewTagsHandler creates a new tags handler
func NewTagsHandler(queries *db.Queries, cfg *config.Config) *TagsHandler {
	return &TagsHandler{queries: queries, cfg: cfg}
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
func (h *TagsHandler) dbTagToAPI(tag db.Tag, following bool) TagResponse {
	url := "https://" + h.cfg.Instance.LocalDomain + "/tags/" + tag.Name
	return TagResponse{
		Name:      tag.Name,
		URL:       url,
		History:   []TagDayCount{}, // Empty for now - would require usage tracking
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

	if h.queries == nil {
		// Mock fallback
		web.JSON(w, http.StatusOK, TagResponse{
			Name:    tagName,
			URL:     "https://example.com/tags/" + tagName,
			History: []TagDayCount{},
		})
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

	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, following))
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

	if h.queries == nil {
		// Mock fallback
		web.JSON(w, http.StatusOK, TagResponse{
			Name:    tagName,
			URL:     "https://example.com/tags/" + tagName,
			History: []TagDayCount{},
		})
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

	// Create tag follow (ignore duplicate errors)
	_, err = h.queries.CreateTagFollow(ctx, db.CreateTagFollowParams{
		AccountID: accountID,
		TagID:     tag.ID,
	})
	// Ignore duplicate key errors - user is already following

	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, true))
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

	if h.queries == nil {
		// Mock fallback
		f := false
		web.JSON(w, http.StatusOK, TagResponse{
			Name:      tagName,
			URL:       "https://example.com/tags/" + tagName,
			History:   []TagDayCount{},
			Following: &f,
		})
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
	// Ignore not found errors - user wasn't following anyway
	_ = err

	web.JSON(w, http.StatusOK, h.dbTagToAPI(tag, false))
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

	if h.queries == nil {
		// Mock fallback
		web.JSON(w, http.StatusOK, []TagResponse{})
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

	result := make([]TagResponse, len(tags))
	for i, tag := range tags {
		result[i] = h.dbTagToAPI(tag, true)
	}

	web.JSON(w, http.StatusOK, result)
}

// Feature handles POST /api/v1/tags/:id/feature (for personal featured tags)
func (h *TagsHandler) Feature(w http.ResponseWriter, r *http.Request) {
	// Feature functionality is handled by featured_tags.go
	// This just returns the tag with updated info
	h.Get(w, r)
}

// Unfeature handles POST /api/v1/tags/:id/unfeature
func (h *TagsHandler) Unfeature(w http.ResponseWriter, r *http.Request) {
	// Unfeature functionality is handled by featured_tags.go
	// This just returns the tag with updated info
	h.Get(w, r)
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
