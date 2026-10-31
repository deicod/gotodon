// Package admin provides domain block management for the Admin API.
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// DomainBlocksHandler handles admin domain block endpoints
type DomainBlocksHandler struct {
	queries *db.Queries
	roles   RoleLookup
}

// NewDomainBlocksHandler creates a new domain blocks handler
func NewDomainBlocksHandler(queries *db.Queries) *DomainBlocksHandler {
	return &DomainBlocksHandler{
		queries: queries,
		roles:   NewRoleLookup(queries),
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
func (h *DomainBlocksHandler) GetAll(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.queries.AdminListDomainBlocks(r.Context(), db.AdminListDomainBlocksParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
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

	web.JSON(w, http.StatusOK, blocks)
}

// Get handles GET /api/v1/admin/domain_blocks/:id
func (h *DomainBlocksHandler) Get(w http.ResponseWriter, r *http.Request) {
	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	row, err := h.queries.AdminGetDomainBlock(r.Context(), blockID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
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

	web.JSON(w, http.StatusOK, block)
}

// Create handles POST /api/v1/admin/domain_blocks
func (h *DomainBlocksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain         string `json:"domain" form:"domain"`
		Severity       string `json:"severity" form:"severity"`
		RejectMedia    bool   `json:"reject_media" form:"reject_media"`
		RejectReports  bool   `json:"reject_reports" form:"reject_reports"`
		PrivateComment string `json:"private_comment" form:"private_comment"`
		PublicComment  string `json:"public_comment" form:"public_comment"`
		Obfuscate      bool   `json:"obfuscate" form:"obfuscate"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Domain == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Domain can't be blank")
		return
	}

	severityStr := "silence"
	if req.Severity != "" {
		if !validSeverity[req.Severity] {
			web.JSONError(w, http.StatusUnprocessableEntity, "Invalid severity")
			return
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

	row, err := h.queries.AdminCreateDomainBlock(r.Context(), params)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
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

	web.JSON(w, http.StatusOK, block)
}

// Update handles PUT /api/v1/admin/domain_blocks/:id
func (h *DomainBlocksHandler) Update(w http.ResponseWriter, r *http.Request) {
	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Severity       *string `json:"severity" form:"severity"`
		RejectMedia    *bool   `json:"reject_media" form:"reject_media"`
		RejectReports  *bool   `json:"reject_reports" form:"reject_reports"`
		PrivateComment *string `json:"private_comment" form:"private_comment"`
		PublicComment  *string `json:"public_comment" form:"public_comment"`
		Obfuscate      *bool   `json:"obfuscate" form:"obfuscate"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validations
	if req.Severity != nil && !validSeverity[*req.Severity] {
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid severity")
		return
	}

	params := db.AdminUpdateDomainBlockParams{
		ID: blockID,
	}

	// Fetch first.
	existing, err := h.queries.AdminGetDomainBlock(r.Context(), blockID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
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

	updated, err := h.queries.AdminUpdateDomainBlock(r.Context(), params)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
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

	web.JSON(w, http.StatusOK, block)
}

// Delete handles DELETE /api/v1/admin/domain_blocks/:id
func (h *DomainBlocksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if err := h.queries.AdminDeleteDomainBlock(r.Context(), blockID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers domain block routes. Every action needs
// manage_federation, as in Mastodon's DomainBlockPolicy.
func (h *DomainBlocksHandler) RegisterRoutes(mux RouteRegistrar) {
	manageFederation := WithPermission(h.roles, auth.PermissionManageFederation)

	mux.HandleFunc("GET /api/v1/admin/domain_blocks", manageFederation(h.GetAll))
	mux.HandleFunc("POST /api/v1/admin/domain_blocks", manageFederation(h.Create))
	mux.HandleFunc("GET /api/v1/admin/domain_blocks/{id}", manageFederation(h.Get))
	mux.HandleFunc("PUT /api/v1/admin/domain_blocks/{id}", manageFederation(h.Update))
	mux.HandleFunc("PATCH /api/v1/admin/domain_blocks/{id}", manageFederation(h.Update))
	mux.HandleFunc("DELETE /api/v1/admin/domain_blocks/{id}", manageFederation(h.Delete))
}
