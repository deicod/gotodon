// Package admin provides email domain block management for the Admin API.
package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

const emailDomainHistoryDays = 7

// EmailDomainBlocksHandler handles admin email domain block endpoints.
type EmailDomainBlocksHandler struct {
	queries *db.Queries
	redis   *redis.Client
}

type emailDomainBlockEntry struct {
	ID                int64
	Domain            string
	CreatedAt         time.Time
	AllowWithApproval bool
}

// EmailDomainBlock represents an email domain block entry.
type EmailDomainBlock struct {
	ID                string       `json:"id"`
	Domain            string       `json:"domain"`
	CreatedAt         time.Time    `json:"created_at"`
	History           []TagHistory `json:"history"`
	AllowWithApproval bool         `json:"allow_with_approval"`
}

// NewEmailDomainBlocksHandler creates a new email domain blocks handler.
func NewEmailDomainBlocksHandler(queries *db.Queries, redisClient *redis.Client) *EmailDomainBlocksHandler {
	return &EmailDomainBlocksHandler{
		queries: queries,
		redis:   redisClient,
	}
}

func (h *EmailDomainBlocksHandler) useDB() bool {
	return h.queries != nil
}

func normalizeEmailDomain(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func emailDomainBlockHistory(ctx context.Context, redisClient *redis.Client, id int64) []TagHistory {
	days := make([]time.Time, 0, emailDomainHistoryDays)
	now := time.Now().UTC()
	for i := 0; i < emailDomainHistoryDays; i++ {
		day := now.AddDate(0, 0, -i)
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		days = append(days, day)
	}

	history := make([]TagHistory, len(days))
	for i, day := range days {
		history[i] = TagHistory{
			Day:      strconv.FormatInt(day.Unix(), 10),
			Uses:     "0",
			Accounts: "0",
		}
	}

	if redisClient == nil || id <= 0 {
		return history
	}

	pipe := redisClient.Pipeline()
	usesCmds := make([]*redis.StringCmd, len(days))
	accountsCmds := make([]*redis.IntCmd, len(days))
	for i, day := range days {
		key := fmt.Sprintf("activity:email_domain_blocks:%d:%d", id, day.Unix())
		usesCmds[i] = pipe.Get(ctx, key)
		accountsCmds[i] = pipe.PFCount(ctx, key+":accounts")
	}

	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return history
	}

	for i := range days {
		if err := usesCmds[i].Err(); err == nil {
			if value := strings.TrimSpace(usesCmds[i].Val()); value != "" {
				history[i].Uses = value
			}
		}

		if err := accountsCmds[i].Err(); err == nil {
			history[i].Accounts = strconv.FormatInt(accountsCmds[i].Val(), 10)
		}
	}

	return history
}

func mapEmailDomainBlock(entry emailDomainBlockEntry, history []TagHistory) EmailDomainBlock {
	return EmailDomainBlock{
		ID:                strconv.FormatInt(entry.ID, 10),
		Domain:            entry.Domain,
		CreatedAt:         entry.CreatedAt,
		History:           history,
		AllowWithApproval: entry.AllowWithApproval,
	}
}

// GetAll handles GET /api/v1/admin/email_domain_blocks.
func (h *EmailDomainBlocksHandler) GetAll(w http.ResponseWriter, r *http.Request) {
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

	if h.useDB() {
		rows, err := h.queries.AdminListEmailDomainBlocks(r.Context(), db.AdminListEmailDomainBlocksParams{
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		blocks := make([]EmailDomainBlock, 0, len(rows))
		for _, row := range rows {
			entry := emailDomainBlockEntry{
				ID:                row.ID,
				Domain:            row.Domain,
				CreatedAt:         row.CreatedAt.Time,
				AllowWithApproval: row.AllowWithApproval,
			}
			blocks = append(blocks, mapEmailDomainBlock(entry, emailDomainBlockHistory(r.Context(), h.redis, row.ID)))
		}

		web.JSON(w, http.StatusOK, blocks)
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Email domain blocks unavailable")
}

// Get handles GET /api/v1/admin/email_domain_blocks/{id}.
func (h *EmailDomainBlocksHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		row, err := h.queries.AdminGetEmailDomainBlock(r.Context(), blockID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		entry := emailDomainBlockEntry{
			ID:                row.ID,
			Domain:            row.Domain,
			CreatedAt:         row.CreatedAt.Time,
			AllowWithApproval: row.AllowWithApproval,
		}
		web.JSON(w, http.StatusOK, mapEmailDomainBlock(entry, emailDomainBlockHistory(r.Context(), h.redis, row.ID)))
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Email domain blocks unavailable")
}

// Create handles POST /api/v1/admin/email_domain_blocks.
func (h *EmailDomainBlocksHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	var req struct {
		Domain            string `json:"domain" form:"domain"`
		AllowWithApproval bool   `json:"allow_with_approval" form:"allow_with_approval"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	domain := normalizeEmailDomain(req.Domain)
	if domain == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Domain can't be blank")
		return
	}

	if h.useDB() {
		_, err := h.queries.AdminGetEmailDomainBlockByDomain(r.Context(), domain)
		if err == nil {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Domain has already been taken")
			return
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		created, err := h.queries.AdminCreateEmailDomainBlock(r.Context(), db.AdminCreateEmailDomainBlockParams{
			Domain:            domain,
			AllowWithApproval: req.AllowWithApproval,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Domain has already been taken")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		entry := emailDomainBlockEntry{
			ID:                created.ID,
			Domain:            created.Domain,
			CreatedAt:         created.CreatedAt.Time,
			AllowWithApproval: created.AllowWithApproval,
		}
		web.JSON(w, http.StatusOK, mapEmailDomainBlock(entry, emailDomainBlockHistory(r.Context(), h.redis, created.ID)))
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Email domain blocks unavailable")
}

// Delete handles DELETE /api/v1/admin/email_domain_blocks/{id}.
func (h *EmailDomainBlocksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	blockID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		if err := h.queries.AdminDeleteEmailDomainBlock(r.Context(), blockID); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		web.JSON(w, http.StatusOK, map[string]any{})
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Email domain blocks unavailable")
}

// RegisterRoutes registers email domain block routes.
func (h *EmailDomainBlocksHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/email_domain_blocks", h.GetAll)
	mux.HandleFunc("POST /api/v1/admin/email_domain_blocks", h.Create)
	mux.HandleFunc("GET /api/v1/admin/email_domain_blocks/{id}", h.Get)
	mux.HandleFunc("DELETE /api/v1/admin/email_domain_blocks/{id}", h.Delete)
}
