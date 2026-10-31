// Package admin provides IP block management for the Admin API.
package admin

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// IpBlocksHandler handles admin IP block endpoints.
type IpBlocksHandler struct {
	queries *db.Queries
	roles   RoleLookup
}

// NewIpBlocksHandler creates a new IP blocks handler.
func NewIpBlocksHandler(queries *db.Queries) *IpBlocksHandler {
	return &IpBlocksHandler{queries: queries, roles: NewRoleLookup(queries)}
}

// IpBlock represents an IP block entry.
type IpBlock struct {
	ID        string     `json:"id"`
	IP        string     `json:"ip"`
	Severity  string     `json:"severity"`
	Comment   string     `json:"comment"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

var ipBlockSeverities = map[string]int32{
	"sign_up_requires_approval": 5000,
	"sign_up_block":             5500,
	"no_access":                 9999,
}

func ipSeverityToInt(severity string) (int32, bool) {
	value, ok := ipBlockSeverities[severity]
	return value, ok
}

func intToIpSeverity(value int32) string {
	for key, v := range ipBlockSeverities {
		if v == value {
			return key
		}
	}
	return "no_access"
}

func parseIPPrefix(input string) (netip.Prefix, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return netip.Prefix{}, errors.New("empty ip")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix, nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func ipForDB(prefix netip.Prefix) string {
	if prefix.Bits() == prefix.Addr().BitLen() {
		return prefix.Addr().String()
	}
	return prefix.String()
}

func formatIPPrefix(input string) string {
	normalized, err := parseIPPrefix(input)
	if err != nil {
		return input
	}
	return normalized.String()
}

func (h *IpBlocksHandler) mapIpBlock(row db.AdminListIpBlocksRow) IpBlock {
	createdAt := time.Now()
	if row.CreatedAt.Valid {
		createdAt = row.CreatedAt.Time
	}
	var expiresAt *time.Time
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		expiresAt = &t
	}
	return IpBlock{
		ID:        strconv.FormatInt(row.ID, 10),
		IP:        formatIPPrefix(row.Ip),
		Severity:  intToIpSeverity(row.Severity),
		Comment:   row.Comment,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}
}

// GetAll handles GET /api/v1/admin/ip_blocks.
func (h *IpBlocksHandler) GetAll(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.queries.AdminListIpBlocks(r.Context(), db.AdminListIpBlocksParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	blocks := make([]IpBlock, 0, len(rows))
	for _, row := range rows {
		blocks = append(blocks, h.mapIpBlock(row))
	}

	web.JSON(w, http.StatusOK, blocks)
}

// Get handles GET /api/v1/admin/ip_blocks/{id}.
func (h *IpBlocksHandler) Get(w http.ResponseWriter, r *http.Request) {
	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	row, err := h.queries.AdminGetIpBlock(r.Context(), blockID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	web.JSON(w, http.StatusOK, h.mapIpBlock(db.AdminListIpBlocksRow(row)))
}

// Create handles POST /api/v1/admin/ip_blocks.
func (h *IpBlocksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP        string `json:"ip" form:"ip"`
		Severity  string `json:"severity" form:"severity"`
		Comment   string `json:"comment" form:"comment"`
		ExpiresIn *int64 `json:"expires_in" form:"expires_in"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	prefix, err := parseIPPrefix(req.IP)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: IP is invalid")
		return
	}
	normalizedIP := ipForDB(prefix)

	severity := "no_access"
	if req.Severity != "" {
		if _, ok := ipSeverityToInt(req.Severity); !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Severity is invalid")
			return
		}
		severity = req.Severity
	}

	existing, err := h.queries.AdminGetIpBlockByIP(r.Context(), normalizedIP)
	if err == nil {
		web.JSON(w, http.StatusOK, h.mapIpBlock(db.AdminListIpBlocksRow(existing)))
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	var expiresAt pgtype.Timestamptz
	if req.ExpiresIn != nil {
		if *req.ExpiresIn > 0 {
			expiresAt = pgtype.Timestamptz{Time: time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second), Valid: true}
		} else {
			expiresAt = pgtype.Timestamptz{Valid: false}
		}
	}

	created, err := h.queries.AdminCreateIpBlock(r.Context(), db.AdminCreateIpBlockParams{
		Column1:   normalizedIP,
		Severity:  ipBlockSeverities[severity],
		ExpiresAt: expiresAt,
		Comment:   req.Comment,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, h.mapIpBlock(db.AdminListIpBlocksRow(created)))
}

// Update handles PUT /api/v1/admin/ip_blocks/{id}.
func (h *IpBlocksHandler) Update(w http.ResponseWriter, r *http.Request) {
	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		IP        *string `json:"ip" form:"ip"`
		Severity  *string `json:"severity" form:"severity"`
		Comment   *string `json:"comment" form:"comment"`
		ExpiresIn *int64  `json:"expires_in" form:"expires_in"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	existing, err := h.queries.AdminGetIpBlock(r.Context(), blockID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ipValue := existing.Ip
	if req.IP != nil {
		normalizedPrefix, err := parseIPPrefix(*req.IP)
		if err != nil {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: IP is invalid")
			return
		}
		normalized := ipForDB(normalizedPrefix)
		if other, err := h.queries.AdminGetIpBlockByIP(r.Context(), normalized); err == nil && other.ID != blockID {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: IP has already been taken")
			return
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		ipValue = normalized
	}

	severityValue := existing.Severity
	if req.Severity != nil {
		value, ok := ipSeverityToInt(*req.Severity)
		if !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Severity is invalid")
			return
		}
		severityValue = value
	}

	commentValue := existing.Comment
	if req.Comment != nil {
		commentValue = *req.Comment
	}

	expiresAt := existing.ExpiresAt
	if req.ExpiresIn != nil {
		if *req.ExpiresIn > 0 {
			expiresAt = pgtype.Timestamptz{Time: time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second), Valid: true}
		} else {
			expiresAt = pgtype.Timestamptz{Valid: false}
		}
	}

	updated, err := h.queries.AdminUpdateIpBlock(r.Context(), db.AdminUpdateIpBlockParams{
		ID:        blockID,
		Column2:   ipValue,
		Severity:  severityValue,
		ExpiresAt: expiresAt,
		Comment:   commentValue,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, h.mapIpBlock(db.AdminListIpBlocksRow(updated)))
}

// Delete handles DELETE /api/v1/admin/ip_blocks/{id}.
func (h *IpBlocksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if err := h.queries.AdminDeleteIpBlock(r.Context(), blockID); err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers IP block routes. Every action needs manage_blocks,
// as in Mastodon's IpBlockPolicy.
func (h *IpBlocksHandler) RegisterRoutes(mux RouteRegistrar) {
	manageBlocks := WithPermission(h.roles, auth.PermissionManageBlocks)

	mux.HandleFunc("GET /api/v1/admin/ip_blocks", manageBlocks(h.GetAll))
	mux.HandleFunc("POST /api/v1/admin/ip_blocks", manageBlocks(h.Create))
	mux.HandleFunc("GET /api/v1/admin/ip_blocks/{id}", manageBlocks(h.Get))
	mux.HandleFunc("PUT /api/v1/admin/ip_blocks/{id}", manageBlocks(h.Update))
	mux.HandleFunc("PATCH /api/v1/admin/ip_blocks/{id}", manageBlocks(h.Update))
	mux.HandleFunc("DELETE /api/v1/admin/ip_blocks/{id}", manageBlocks(h.Delete))
}
