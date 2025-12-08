// Package admin provides instance configuration for the Admin API.
package admin

import (
	"net/http"

	"github.com/labstack/echo/v4"
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
func (h *InstanceHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, h.config)
}

// Update handles PATCH /api/v1/admin/instance
func (h *InstanceHandler) Update(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	var req struct {
		Title            *string  `json:"title" form:"title"`
		Description      *string  `json:"description" form:"description"`
		Languages        []string `json:"languages" form:"languages[]"`
		Registrations    *bool    `json:"registrations" form:"registrations"`
		ApprovalRequired *bool    `json:"approval_required" form:"approval_required"`
		InvitesEnabled   *bool    `json:"invites_enabled" form:"invites_enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
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

	return c.JSON(http.StatusOK, h.config)
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
func (h *TrendsHandler) GetTags(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	// Return empty trends for now
	return c.JSON(http.StatusOK, []any{})
}

// GetStatuses handles GET /api/v1/admin/trends/statuses
func (h *TrendsHandler) GetStatuses(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, []any{})
}

// GetLinks handles GET /api/v1/admin/trends/links
func (h *TrendsHandler) GetLinks(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, []any{})
}

// DimensionsHandler handles admin dimensions endpoints
type DimensionsHandler struct{}

// NewDimensionsHandler creates a new dimensions handler
func NewDimensionsHandler() *DimensionsHandler {
	return &DimensionsHandler{}
}

// Get handles POST /api/v1/admin/dimensions
func (h *DimensionsHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	// Dimensions are used for statistics
	return c.JSON(http.StatusOK, []any{})
}

// MeasuresHandler handles admin measures endpoints
type MeasuresHandler struct{}

// NewMeasuresHandler creates a new measures handler
func NewMeasuresHandler() *MeasuresHandler {
	return &MeasuresHandler{}
}

// Get handles POST /api/v1/admin/measures
func (h *MeasuresHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	// Measures are used for statistics
	return c.JSON(http.StatusOK, []any{})
}

// RetentionHandler handles admin retention endpoints
type RetentionHandler struct{}

// NewRetentionHandler creates a new retention handler
func NewRetentionHandler() *RetentionHandler {
	return &RetentionHandler{}
}

// Get handles POST /api/v1/admin/retention
func (h *RetentionHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, []any{})
}

// RegisterInstanceRoutes registers instance route
func (h *InstanceHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/instance", h.Get)
	g.PATCH("/instance", h.Update)
}

// RegisterTrendsRoutes registers trends routes
func (h *TrendsHandler) RegisterRoutes(g *echo.Group) {
	trends := g.Group("/trends")
	trends.GET("/tags", h.GetTags)
	trends.GET("/statuses", h.GetStatuses)
	trends.GET("/links", h.GetLinks)
}

// RegisterDimensionsRoutes registers dimensions route
func (h *DimensionsHandler) RegisterRoutes(g *echo.Group) {
	g.POST("/dimensions", h.Get)
}

// RegisterMeasuresRoutes registers measures route
func (h *MeasuresHandler) RegisterRoutes(g *echo.Group) {
	g.POST("/measures", h.Get)
}

// RegisterRetentionRoutes registers retention route
func (h *RetentionHandler) RegisterRoutes(g *echo.Group) {
	g.POST("/retention", h.Get)
}
