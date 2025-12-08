// Package admin provides domain block management for the Admin API.
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/db"
)

// DomainBlocksHandler handles admin domain block endpoints
type DomainBlocksHandler struct {
	queries *db.Queries
}

// NewDomainBlocksHandler creates a new domain blocks handler
func NewDomainBlocksHandler(queries *db.Queries) *DomainBlocksHandler {
	return &DomainBlocksHandler{
		queries: queries,
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
	"noop":    true, // Often corresponds to severity 0 or 'noop' in Mastodon
}

// severityToInt converts severity string to int for DB
func severityToInt(s string) int32 {
	switch s {
	case "silence":
		return 1
	case "suspend":
		return 2
	case "noop":
		return 0
	default:
		return 0 // default to noop
	}
}

// intToSeverity converts DB int to severity string
func intToSeverity(i int32) string {
	switch i {
	case 1:
		return "silence"
	case 2:
		return "suspend"
	case 0:
		return "noop"
	default:
		return "noop"
	}
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
	offset := 0
	if o := c.QueryParam("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	rows, err := h.queries.AdminListDomainBlocks(c.Request().Context(), db.AdminListDomainBlocksParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
	}

	blocks := make([]DomainBlock, 0, len(rows))
	for _, row := range rows {
		blocks = append(blocks, DomainBlock{
			ID:             strconv.FormatInt(row.ID, 10),
			Domain:         row.Domain,
			CreatedAt:      row.CreatedAt.Time,
			Severity:       intToSeverity(row.Severity),
			RejectMedia:    row.RejectMedia,
			RejectReports:  row.RejectReports,
			PrivateComment: row.PrivateComment.String,
			PublicComment:  row.PublicComment.String,
			Obfuscate:      row.Obfuscate,
		})
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

	row, err := h.queries.AdminGetDomainBlock(c.Request().Context(), blockID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	block := DomainBlock{
		ID:             strconv.FormatInt(row.ID, 10),
		Domain:         row.Domain,
		CreatedAt:      row.CreatedAt.Time,
		Severity:       intToSeverity(row.Severity),
		RejectMedia:    row.RejectMedia,
		RejectReports:  row.RejectReports,
		PrivateComment: row.PrivateComment.String,
		PublicComment:  row.PublicComment.String,
		Obfuscate:      row.Obfuscate,
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

	severityStr := "silence"
	if req.Severity != "" {
		if !validSeverity[req.Severity] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Invalid severity",
			})
		}
		severityStr = req.Severity
	}

	params := db.AdminCreateDomainBlockParams{
		Domain:        req.Domain,
		Severity:      severityToInt(severityStr),
		RejectMedia:   req.RejectMedia,
		RejectReports: req.RejectReports,
		Obfuscate:     req.Obfuscate,
	}
	if req.PrivateComment != "" {
		params.PrivateComment = pgtype.Text{String: req.PrivateComment, Valid: true}
	}
	if req.PublicComment != "" {
		params.PublicComment = pgtype.Text{String: req.PublicComment, Valid: true}
	}

	row, err := h.queries.AdminCreateDomainBlock(c.Request().Context(), params)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
	}

	block := DomainBlock{
		ID:             strconv.FormatInt(row.ID, 10),
		Domain:         row.Domain,
		CreatedAt:      row.CreatedAt.Time,
		Severity:       intToSeverity(row.Severity),
		RejectMedia:    row.RejectMedia,
		RejectReports:  row.RejectReports,
		PrivateComment: row.PrivateComment.String,
		PublicComment:  row.PublicComment.String,
		Obfuscate:      row.Obfuscate,
	}

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

	// Validations
	if req.Severity != nil && !validSeverity[*req.Severity] {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Invalid severity",
		})
	}

	// Because sqlc Update is specific about what is updated or we use COALESCE in SQL,
	// We matched SQL with COALESCE. We need to pass valid params.
	// However, we don't have the current values easily without a Get.
	// But COALESCE(NULL, old) works if we pass NULL for unaffected fields.
	// pgtype handles NULLs.

	params := db.AdminUpdateDomainBlockParams{
		ID: blockID,
	}

	// We need to fetch current to handle defaulting?
	// Actually our SQL uses COALESCE($X, column). So if we pass NULL, it keeps existing.
	// But pgtype doesn't have a simple "Null" that means "don't change" unless the query expects it.
	// The query expects typed parameters.
	// $2 is severity. If we pass NULL (impossible for int32 unless wrapped), we can't update to NULL.
	// But severity is NOT NULL in DB.
	// Wait, `AdminUpdateDomainBlock` generated code signature:
	// Severity int32 `db:"severity" json:"severity"`
	// This means it takes an int32, NOT a pointer. So we CANNOT pass NULL for int32.
	// This implies the SQL `COALESCE($2, severity)` assumes $2 can be null, but sqlc generated a non-null primitive because the column is non-null?
	// ACTUALLY: `sqlc` generates primitives for non-null columns usually.
	// To support optional updates, I should have used `sqlc.narg` or nullable params in SQL, but sqlc is strict.
	// Best practice: Fetch the object, apply changes in Go, then Update.
	// Or change query to take nullable params.

	// Let's fetch first.
	existing, err := h.queries.AdminGetDomainBlock(c.Request().Context(), blockID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	params.Severity = existing.Severity
	params.RejectMedia = existing.RejectMedia
	params.RejectReports = existing.RejectReports
	params.Obfuscate = existing.Obfuscate
	params.PrivateComment = existing.PrivateComment
	params.PublicComment = existing.PublicComment

	if req.Severity != nil {
		params.Severity = severityToInt(*req.Severity)
	}
	if req.RejectMedia != nil {
		params.RejectMedia = *req.RejectMedia
	}
	if req.RejectReports != nil {
		params.RejectReports = *req.RejectReports
	}
	if req.Obfuscate != nil {
		params.Obfuscate = *req.Obfuscate
	}
	if req.PrivateComment != nil {
		params.PrivateComment = pgtype.Text{String: *req.PrivateComment, Valid: true}
	}
	if req.PublicComment != nil {
		params.PublicComment = pgtype.Text{String: *req.PublicComment, Valid: true}
	}

	updated, err := h.queries.AdminUpdateDomainBlock(c.Request().Context(), params)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
	}

	block := DomainBlock{
		ID:             strconv.FormatInt(updated.ID, 10),
		Domain:         updated.Domain,
		CreatedAt:      updated.CreatedAt.Time,
		Severity:       intToSeverity(updated.Severity),
		RejectMedia:    updated.RejectMedia,
		RejectReports:  updated.RejectReports,
		PrivateComment: updated.PrivateComment.String,
		PublicComment:  updated.PublicComment.String,
		Obfuscate:      updated.Obfuscate,
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

	if err := h.queries.AdminDeleteDomainBlock(c.Request().Context(), blockID); err != nil {
		// Could check for not found, but delete is idempotent effectively if we don't care about 404
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// requireAdmin checked via shared helper in package

// RegisterRoutes registers domain block routes
func (h *DomainBlocksHandler) RegisterRoutes(g *echo.Group) {
	blocks := g.Group("/domain_blocks")
	blocks.GET("", h.GetAll)
	blocks.POST("", h.Create)
	blocks.GET("/:id", h.Get)
	blocks.PUT("/:id", h.Update)
	blocks.DELETE("/:id", h.Delete)
}
