// Package admin provides tag management for the Admin API.
package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// TagsHandler handles admin tag endpoints.
type TagsHandler struct {
	queries *db.Queries
}

// NewTagsHandler creates a new admin tags handler.
func NewTagsHandler(queries *db.Queries) *TagsHandler {
	return &TagsHandler{queries: queries}
}

// TagHistory represents usage stats for a single day.
type TagHistory struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

// AdminTag represents a tag in admin responses.
type AdminTag struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	URL            string       `json:"url"`
	History        []TagHistory `json:"history"`
	Trendable      bool         `json:"trendable"`
	Usable         bool         `json:"usable"`
	RequiresReview bool         `json:"requires_review"`
	Listable       bool         `json:"listable"`
}

func tagBoolOrDefault(value pgtype.Bool, defaultValue bool) bool {
	if value.Valid {
		return value.Bool
	}
	return defaultValue
}

func tagDisplayName(tag db.Tag) string {
	if tag.DisplayName.Valid && strings.TrimSpace(tag.DisplayName.String) != "" {
		return tag.DisplayName.String
	}
	return tag.Name
}

func tagRequiresReview(reviewedAt pgtype.Timestamptz) bool {
	return !reviewedAt.Valid
}

func mapAdminTag(tag db.Tag) AdminTag {
	return AdminTag{
		ID:             strconv.FormatInt(tag.ID, 10),
		Name:           tagDisplayName(tag),
		URL:            "/tags/" + tag.Name,
		History:        []TagHistory{},
		Trendable:      tagBoolOrDefault(tag.Trendable, false),
		Usable:         tagBoolOrDefault(tag.Usable, true),
		Listable:       tagBoolOrDefault(tag.Listable, true),
		RequiresReview: tagRequiresReview(tag.ReviewedAt),
	}
}

// GetAll handles GET /api/v1/admin/tags.
func (h *TagsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	limit := 100
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	rows, err := h.queries.AdminListTags(r.Context(), db.AdminListTagsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	resp := make([]AdminTag, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, mapAdminTag(row))
	}

	web.JSON(w, http.StatusOK, resp)
}

// Get handles GET /api/v1/admin/tags/{id}.
func (h *TagsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	tagID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	tag, err := h.queries.AdminGetTag(r.Context(), tagID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapAdminTag(tag))
}

// Update handles PUT /api/v1/admin/tags/{id}.
func (h *TagsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	tagID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		DisplayName *string `json:"display_name"`
		Listable    *bool   `json:"listable"`
		Trendable   *bool   `json:"trendable"`
		Usable      *bool   `json:"usable"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	existing, err := h.queries.AdminGetTag(r.Context(), tagID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	displayName := existing.DisplayName
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if trimmed == "" {
			displayName = pgtype.Text{Valid: false}
		} else {
			displayName = pgtype.Text{String: trimmed, Valid: true}
		}
	}

	usable := existing.Usable
	if req.Usable != nil {
		usable = pgtype.Bool{Bool: *req.Usable, Valid: true}
	}

	trendable := existing.Trendable
	if req.Trendable != nil {
		trendable = pgtype.Bool{Bool: *req.Trendable, Valid: true}
	}

	listable := existing.Listable
	if req.Listable != nil {
		listable = pgtype.Bool{Bool: *req.Listable, Valid: true}
	}

	reviewedAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}

	updated, err := h.queries.AdminUpdateTag(r.Context(), db.AdminUpdateTagParams{
		ID:          tagID,
		DisplayName: displayName,
		Usable:      usable,
		Trendable:   trendable,
		Listable:    listable,
		ReviewedAt:  reviewedAt,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapAdminTag(updated))
}

// RegisterRoutes registers admin tag routes.
func (h *TagsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/tags", h.GetAll)
	mux.HandleFunc("GET /api/v1/admin/tags/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/admin/tags/{id}", h.Update)
}
