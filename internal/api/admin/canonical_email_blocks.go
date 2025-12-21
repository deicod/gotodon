// Package admin provides canonical email block management for the Admin API.
package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// CanonicalEmailBlocksHandler handles admin canonical email block endpoints.
type CanonicalEmailBlocksHandler struct {
	queries *db.Queries
}

// NewCanonicalEmailBlocksHandler creates a new canonical email blocks handler.
func NewCanonicalEmailBlocksHandler(queries *db.Queries) *CanonicalEmailBlocksHandler {
	return &CanonicalEmailBlocksHandler{queries: queries}
}

// CanonicalEmailBlock represents a canonical email block entry.
type CanonicalEmailBlock struct {
	ID                 string `json:"id"`
	CanonicalEmailHash string `json:"canonical_email_hash"`
}

func canonicalizeEmail(email string) string {
	normalized := strings.ToLower(email)
	parts := strings.SplitN(normalized, "@", 2)
	local := parts[0]
	domain := ""
	if len(parts) > 1 {
		domain = parts[1]
	}
	local = strings.ReplaceAll(local, ".", "")
	if plus := strings.Index(local, "+"); plus >= 0 {
		local = local[:plus]
	}
	return local + "@" + domain
}

func canonicalEmailHash(email string) string {
	canonical := canonicalizeEmail(email)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func mapCanonicalEmailBlock(id int64, hash string) CanonicalEmailBlock {
	return CanonicalEmailBlock{
		ID:                 strconv.FormatInt(id, 10),
		CanonicalEmailHash: hash,
	}
}

// GetAll handles GET /api/v1/admin/canonical_email_blocks.
func (h *CanonicalEmailBlocksHandler) GetAll(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.queries.AdminListCanonicalEmailBlocks(r.Context(), db.AdminListCanonicalEmailBlocksParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	blocks := make([]CanonicalEmailBlock, 0, len(rows))
	for _, row := range rows {
		blocks = append(blocks, mapCanonicalEmailBlock(row.ID, row.CanonicalEmailHash))
	}

	web.JSON(w, http.StatusOK, blocks)
}

// Get handles GET /api/v1/admin/canonical_email_blocks/{id}.
func (h *CanonicalEmailBlocksHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	row, err := h.queries.AdminGetCanonicalEmailBlock(r.Context(), blockID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapCanonicalEmailBlock(row.ID, row.CanonicalEmailHash))
}

// Test handles POST /api/v1/admin/canonical_email_blocks/test.
func (h *CanonicalEmailBlocksHandler) Test(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	var req struct {
		Email string `json:"email" form:"email"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Email == "" {
		web.JSONError(w, http.StatusBadRequest, "Email is required")
		return
	}

	hash := canonicalEmailHash(req.Email)
	row, err := h.queries.AdminGetCanonicalEmailBlockByHash(r.Context(), hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSON(w, http.StatusOK, []CanonicalEmailBlock{})
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, []CanonicalEmailBlock{mapCanonicalEmailBlock(row.ID, row.CanonicalEmailHash)})
}

// Create handles POST /api/v1/admin/canonical_email_blocks.
func (h *CanonicalEmailBlocksHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	var req struct {
		Email              string `json:"email" form:"email"`
		CanonicalEmailHash string `json:"canonical_email_hash" form:"canonical_email_hash"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	hash := req.CanonicalEmailHash
	if req.Email != "" {
		hash = canonicalEmailHash(req.Email)
	}

	if hash == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Email or canonical_email_hash is required")
		return
	}

	_, err := h.queries.AdminGetCanonicalEmailBlockByHash(r.Context(), hash)
	if err == nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Canonical email hash has already been taken")
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	created, err := h.queries.AdminCreateCanonicalEmailBlock(r.Context(), db.AdminCreateCanonicalEmailBlockParams{
		CanonicalEmailHash: hash,
		ReferenceAccountID: pgtype.Int8{Valid: false},
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Canonical email hash has already been taken")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapCanonicalEmailBlock(created.ID, created.CanonicalEmailHash))
}

// Delete handles DELETE /api/v1/admin/canonical_email_blocks/{id}.
func (h *CanonicalEmailBlocksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if _, err := h.queries.AdminGetCanonicalEmailBlock(r.Context(), blockID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if err := h.queries.AdminDeleteCanonicalEmailBlock(r.Context(), blockID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers canonical email block routes.
func (h *CanonicalEmailBlocksHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/canonical_email_blocks", h.GetAll)
	mux.HandleFunc("POST /api/v1/admin/canonical_email_blocks", h.Create)
	mux.HandleFunc("GET /api/v1/admin/canonical_email_blocks/{id}", h.Get)
	mux.HandleFunc("DELETE /api/v1/admin/canonical_email_blocks/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/admin/canonical_email_blocks/test", h.Test)
}
