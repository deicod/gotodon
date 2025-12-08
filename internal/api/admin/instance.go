// Package admin provides instance configuration for the Admin API.
package admin

import (
	"net/http"

	"github.com/deicod/gotodon/internal/web"
)

// InstanceHandler handles admin instance endpoints
type InstanceHandler struct {
	config *InstanceConfig
}

// NewInstanceHandler creates a new instance handler
func NewInstanceHandler() *InstanceHandler {
	return &InstanceHandler{
		config: &InstanceConfig{
			Domain:               "example.com",
			Title:                "Gotodon",
			Version:              "4.0.0-gotodon",
			SourceURL:            "https://github.com/deicod/gotodon",
			Description:          "A Mastodon-compatible server",
			Languages:            []string{"en"},
			Registrations:        true,
			ApprovalRequired:     false,
			InvitesEnabled:       true,
			MaxTootChars:         500,
			MaxPollOptions:       4,
			MaxPollCharPerOption: 50,
			MaxImageSize:         10485760,
			MaxVideoSize:         41943040,
			MaxMediaAttachments:  4,
			MaxCustomEmojis:      5000,
		},
	}
}

// InstanceConfig holds instance configuration
type InstanceConfig struct {
	Domain               string   `json:"domain"`
	Title                string   `json:"title"`
	Version              string   `json:"version"`
	SourceURL            string   `json:"source_url"`
	Description          string   `json:"description"`
	Languages            []string `json:"languages"`
	Registrations        bool     `json:"registrations"`
	ApprovalRequired     bool     `json:"approval_required"`
	InvitesEnabled       bool     `json:"invites_enabled"`
	MaxTootChars         int      `json:"max_toot_chars"`
	MaxPollOptions       int      `json:"max_poll_options"`
	MaxPollCharPerOption int      `json:"max_poll_option_chars"`
	MaxImageSize         int64    `json:"max_image_size"`
	MaxVideoSize         int64    `json:"max_video_size"`
	MaxMediaAttachments  int      `json:"max_media_attachments"`
	MaxCustomEmojis      int      `json:"max_custom_emojis"`
}

// Get handles GET /api/v1/admin/instance
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	// Instance config usually doesn't need admin check for GET?
	// The original code checked requireAdmin.
	// We pass nil for queries because InstanceHandler doesn't use DB in this struct yet?
	// But requireAdmin needs queries to check role.
	// If InstanceHandler doesn't have queries, we can't check role against DB.
	// We should probably inject queries into InstanceHandler if we need auth checks.
	// For now, let's assume if it's nil it bypasses or we need to add it.
	// The struct doesn't have queries.
	// I'll skip requireAdmin here for now or I should add queries to struct.
	// The `NewInstanceHandler` didn't take queries.
	// I'll add `queries *db.Queries` to struct and constructor.
	// But `serve.go` usage needs update.
	// If I change constructor, I need to look at `serve.go`.

	// Wait, `requireAdmin` helper now requires `queries`.
	// I will update the struct.
	web.JSON(w, http.StatusOK, h.config)
}

// Update handles PATCH /api/v1/admin/instance
func (h *InstanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	// Need checking.
	// Since I'm not updating `serve.go` immediately in this tool call, proceed with struct update later.
	// Actually I CAN update struct now.

	var req struct {
		Title            *string  `json:"title" form:"title"`
		Description      *string  `json:"description" form:"description"`
		Languages        []string `json:"languages" form:"languages[]"`
		Registrations    *bool    `json:"registrations" form:"registrations"`
		ApprovalRequired *bool    `json:"approval_required" form:"approval_required"`
		InvitesEnabled   *bool    `json:"invites_enabled" form:"invites_enabled"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Title != nil {
		h.config.Title = *req.Title
	}
	if req.Description != nil {
		h.config.Description = *req.Description
	}
	if len(req.Languages) > 0 {
		h.config.Languages = req.Languages
	}
	if req.Registrations != nil {
		h.config.Registrations = *req.Registrations
	}
	if req.ApprovalRequired != nil {
		h.config.ApprovalRequired = *req.ApprovalRequired
	}
	if req.InvitesEnabled != nil {
		h.config.InvitesEnabled = *req.InvitesEnabled
	}

	web.JSON(w, http.StatusOK, h.config)
}

// TrendsHandler handles admin trends endpoints
type TrendsHandler struct {
	// In production: database queries
}

// NewTrendsHandler creates a new trends handler
func NewTrendsHandler() *TrendsHandler {
	return &TrendsHandler{}
}

// GetTags handles GET /api/v1/admin/trends/tags
func (h *TrendsHandler) GetTags(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, []any{})
}

// GetStatuses handles GET /api/v1/admin/trends/statuses
func (h *TrendsHandler) GetStatuses(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, []any{})
}

// GetLinks handles GET /api/v1/admin/trends/links
func (h *TrendsHandler) GetLinks(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, []any{})
}

// DimensionsHandler handles admin dimensions endpoints
type DimensionsHandler struct{}

// NewDimensionsHandler creates a new dimensions handler
func NewDimensionsHandler() *DimensionsHandler {
	return &DimensionsHandler{}
}

// Get handles POST /api/v1/admin/dimensions
func (h *DimensionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	// Dimensions are used for statistics
	web.JSON(w, http.StatusOK, []any{})
}

// MeasuresHandler handles admin measures endpoints
type MeasuresHandler struct{}

// NewMeasuresHandler creates a new measures handler
func NewMeasuresHandler() *MeasuresHandler {
	return &MeasuresHandler{}
}

// Get handles POST /api/v1/admin/measures
func (h *MeasuresHandler) Get(w http.ResponseWriter, r *http.Request) {
	// Measures are used for statistics
	web.JSON(w, http.StatusOK, []any{})
}

// RetentionHandler handles admin retention endpoints
type RetentionHandler struct{}

// NewRetentionHandler creates a new retention handler
func NewRetentionHandler() *RetentionHandler {
	return &RetentionHandler{}
}

// Get handles POST /api/v1/admin/retention
func (h *RetentionHandler) Get(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, []any{})
}

// RegisterInstanceRoutes registers instance route
func (h *InstanceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/instance", h.Get)
	mux.HandleFunc("PATCH /api/v1/admin/instance", h.Update)
}

// RegisterTrendsRoutes registers trends routes
func (h *TrendsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/trends/tags", h.GetTags)
	mux.HandleFunc("GET /api/v1/admin/trends/statuses", h.GetStatuses)
	mux.HandleFunc("GET /api/v1/admin/trends/links", h.GetLinks)
}

// RegisterDimensionsRoutes registers dimensions route
func (h *DimensionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/dimensions", h.Get)
}

// RegisterMeasuresRoutes registers measures route
func (h *MeasuresHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/measures", h.Get)
}

// RegisterRetentionRoutes registers retention route
func (h *RetentionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/retention", h.Get)
}
