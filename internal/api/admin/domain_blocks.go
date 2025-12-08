// Package admin provides domain block management for the Admin API.
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/pkg/snowflake"
)

// DomainBlocksHandler handles admin domain block endpoints
type DomainBlocksHandler struct {
	idGen  *snowflake.Generator
	blocks map[int64]*DomainBlock
}

// NewDomainBlocksHandler creates a new domain blocks handler
func NewDomainBlocksHandler() *DomainBlocksHandler {
	return &DomainBlocksHandler{
		idGen:  snowflake.NewGenerator(),
		blocks: make(map[int64]*DomainBlock),
	}
}

// DomainBlock represents a domain block
type DomainBlock struct {
	ID             string    `json:"id"`
	Domain         string    `json:"domain"`
	CreatedAt      time.Time `json:"created_at"`
	Severity       string    `json:"severity"`
	RejectMedia    bool      `json:"reject_media"`
	RejectReports  bool      `json:"reject_reports"`
	PrivateComment string    `json:"private_comment,omitempty"`
	PublicComment  string    `json:"public_comment,omitempty"`
	Obfuscate      bool      `json:"obfuscate"`
}

// Valid severity levels
var validSeverity = map[string]bool{
	"silence": true,
	"suspend": true,
	"noop":    true,
}

// GetAll handles GET /api/v1/admin/domain_blocks
func (h *DomainBlocksHandler) GetAll(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	blocks := make([]DomainBlock, 0, limit)
	for _, block := range h.blocks {
		blocks = append(blocks, *block)
		if len(blocks) >= limit {
			break
		}
	}

	return c.JSON(http.StatusOK, blocks)
}

// Get handles GET /api/v1/admin/domain_blocks/:id
func (h *DomainBlocksHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	blockID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	block, exists := h.blocks[blockID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	return c.JSON(http.StatusOK, block)
}

// Create handles POST /api/v1/admin/domain_blocks
func (h *DomainBlocksHandler) Create(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	var req struct {
		Domain         string `json:"domain" form:"domain"`
		Severity       string `json:"severity" form:"severity"`
		RejectMedia    bool   `json:"reject_media" form:"reject_media"`
		RejectReports  bool   `json:"reject_reports" form:"reject_reports"`
		PrivateComment string `json:"private_comment" form:"private_comment"`
		PublicComment  string `json:"public_comment" form:"public_comment"`
		Obfuscate      bool   `json:"obfuscate" form:"obfuscate"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if req.Domain == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Domain can't be blank",
		})
	}

	severity := "silence"
	if req.Severity != "" {
		if !validSeverity[req.Severity] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Invalid severity",
			})
		}
		severity = req.Severity
	}

	blockID := h.idGen.Generate()
	block := &DomainBlock{
		ID:             strconv.FormatInt(blockID, 10),
		Domain:         req.Domain,
		CreatedAt:      time.Now(),
		Severity:       severity,
		RejectMedia:    req.RejectMedia,
		RejectReports:  req.RejectReports,
		PrivateComment: req.PrivateComment,
		PublicComment:  req.PublicComment,
		Obfuscate:      req.Obfuscate,
	}
	h.blocks[blockID] = block

	return c.JSON(http.StatusOK, block)
}

// Update handles PUT /api/v1/admin/domain_blocks/:id
func (h *DomainBlocksHandler) Update(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	blockID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	block, exists := h.blocks[blockID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req struct {
		Severity       *string `json:"severity" form:"severity"`
		RejectMedia    *bool   `json:"reject_media" form:"reject_media"`
		RejectReports  *bool   `json:"reject_reports" form:"reject_reports"`
		PrivateComment *string `json:"private_comment" form:"private_comment"`
		PublicComment  *string `json:"public_comment" form:"public_comment"`
		Obfuscate      *bool   `json:"obfuscate" form:"obfuscate"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if req.Severity != nil {
		if !validSeverity[*req.Severity] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Invalid severity",
			})
		}
		block.Severity = *req.Severity
	}
	if req.RejectMedia != nil {
		block.RejectMedia = *req.RejectMedia
	}
	if req.RejectReports != nil {
		block.RejectReports = *req.RejectReports
	}
	if req.PrivateComment != nil {
		block.PrivateComment = *req.PrivateComment
	}
	if req.PublicComment != nil {
		block.PublicComment = *req.PublicComment
	}
	if req.Obfuscate != nil {
		block.Obfuscate = *req.Obfuscate
	}

	return c.JSON(http.StatusOK, block)
}

// Delete handles DELETE /api/v1/admin/domain_blocks/:id
func (h *DomainBlocksHandler) Delete(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	blockID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if _, exists := h.blocks[blockID]; !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	delete(h.blocks, blockID)

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers domain block routes
func (h *DomainBlocksHandler) RegisterRoutes(g *echo.Group) {
	blocks := g.Group("/domain_blocks")
	blocks.GET("", h.GetAll)
	blocks.POST("", h.Create)
	blocks.GET("/:id", h.Get)
	blocks.PUT("/:id", h.Update)
	blocks.DELETE("/:id", h.Delete)
}
