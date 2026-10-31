// Package admin provides domain allow management for the Admin API.
package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// DomainAllowsHandler handles admin domain allow endpoints.
type DomainAllowsHandler struct {
	queries *db.Queries
	roles   RoleLookup
}

// NewDomainAllowsHandler creates a new domain allows handler.
func NewDomainAllowsHandler(queries *db.Queries) *DomainAllowsHandler {
	return &DomainAllowsHandler{queries: queries, roles: NewRoleLookup(queries)}
}

// DomainAllow represents a domain allow entry.
type DomainAllow struct {
	ID        string    `json:"id"`
	Domain    string    `json:"domain"`
	CreatedAt time.Time `json:"created_at"`
}

// GetAll handles GET /api/v1/admin/domain_allows.
func (h *DomainAllowsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.queries.ListDomainAllows(r.Context(), db.ListDomainAllowsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	allows := make([]DomainAllow, 0, len(rows))
	for _, row := range rows {
		createdAt := time.Now()
		if row.CreatedAt.Valid {
			createdAt = row.CreatedAt.Time
		}
		allows = append(allows, DomainAllow{
			ID:        strconv.FormatInt(row.ID, 10),
			Domain:    row.Domain,
			CreatedAt: createdAt,
		})
	}

	web.JSON(w, http.StatusOK, allows)
}

// Get handles GET /api/v1/admin/domain_allows/{id}.
func (h *DomainAllowsHandler) Get(w http.ResponseWriter, r *http.Request) {
	allowID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	row, err := h.queries.GetDomainAllow(r.Context(), allowID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	createdAt := time.Now()
	if row.CreatedAt.Valid {
		createdAt = row.CreatedAt.Time
	}

	web.JSON(w, http.StatusOK, DomainAllow{
		ID:        strconv.FormatInt(row.ID, 10),
		Domain:    row.Domain,
		CreatedAt: createdAt,
	})
}

// Create handles POST /api/v1/admin/domain_allows.
func (h *DomainAllowsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain" form:"domain"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Domain can't be blank")
		return
	}

	existing, err := h.queries.GetDomainAllowByDomain(r.Context(), domain)
	if err == nil {
		createdAt := time.Now()
		if existing.CreatedAt.Valid {
			createdAt = existing.CreatedAt.Time
		}
		web.JSON(w, http.StatusOK, DomainAllow{
			ID:        strconv.FormatInt(existing.ID, 10),
			Domain:    existing.Domain,
			CreatedAt: createdAt,
		})
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	created, err := h.queries.CreateDomainAllow(r.Context(), domain)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	createdAt := time.Now()
	if created.CreatedAt.Valid {
		createdAt = created.CreatedAt.Time
	}

	web.JSON(w, http.StatusOK, DomainAllow{
		ID:        strconv.FormatInt(created.ID, 10),
		Domain:    created.Domain,
		CreatedAt: createdAt,
	})
}

// Delete handles DELETE /api/v1/admin/domain_allows/{id}.
func (h *DomainAllowsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	allowID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if err := h.queries.DeleteDomainAllow(r.Context(), allowID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers domain allow routes. Every action needs
// manage_federation, as in Mastodon's DomainAllowPolicy.
func (h *DomainAllowsHandler) RegisterRoutes(mux RouteRegistrar) {
	manageFederation := WithPermission(h.roles, auth.PermissionManageFederation)

	mux.HandleFunc("GET /api/v1/admin/domain_allows", manageFederation(h.GetAll))
	mux.HandleFunc("POST /api/v1/admin/domain_allows", manageFederation(h.Create))
	mux.HandleFunc("GET /api/v1/admin/domain_allows/{id}", manageFederation(h.Get))
	mux.HandleFunc("DELETE /api/v1/admin/domain_allows/{id}", manageFederation(h.Delete))
}
