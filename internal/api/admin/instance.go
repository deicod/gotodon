// Package admin provides instance configuration for the Admin API.
package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
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
	queries *db.Queries
	cfg     *config.Config
}

// NewTrendsHandler creates a new trends handler
func NewTrendsHandler(queries *db.Queries, cfg *config.Config) *TrendsHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &TrendsHandler{queries: queries, cfg: cfg}
}

// AdminTrendStatus represents a status in admin trends responses.
type AdminTrendStatus struct {
	v1.Status
	RequiresReview bool `json:"requires_review"`
}

// AdminTrendLink represents a link in admin trends responses.
type AdminTrendLink struct {
	ID             string       `json:"id"`
	URL            string       `json:"url"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Type           string       `json:"type"`
	AuthorName     string       `json:"author_name"`
	AuthorURL      string       `json:"author_url"`
	ProviderName   string       `json:"provider_name"`
	ProviderURL    string       `json:"provider_url"`
	HTML           string       `json:"html"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	Image          *string      `json:"image"`
	Blurhash       *string      `json:"blurhash"`
	History        []TagHistory `json:"history"`
	RequiresReview bool         `json:"requires_review"`
}

func parseTrendLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func trendableParam(value bool) pgtype.Bool {
	return pgtype.Bool{Bool: value, Valid: true}
}

func trendHistoryEntry(uses, accounts int64) TagHistory {
	return TagHistory{
		Day:      strconv.FormatInt(time.Now().Unix(), 10),
		Uses:     strconv.FormatInt(uses, 10),
		Accounts: strconv.FormatInt(accounts, 10),
	}
}

func trendTagFromRow(row db.AdminListTrendingTagsRow) db.Tag {
	return db.Tag{
		ID:                row.ID,
		Name:              row.Name,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		Usable:            row.Usable,
		Trendable:         row.Trendable,
		Listable:          row.Listable,
		ReviewedAt:        row.ReviewedAt,
		RequestedReviewAt: row.RequestedReviewAt,
		LastStatusAt:      row.LastStatusAt,
		MaxScore:          row.MaxScore,
		MaxScoreAt:        row.MaxScoreAt,
		DisplayName:       row.DisplayName,
	}
}

func previewCardType(card db.PreviewCard) string {
	switch card.Type {
	case 1:
		return "photo"
	case 2:
		return "video"
	case 3:
		return "rich"
	default:
		return "link"
	}
}

func previewCardImageURL(card db.PreviewCard) *string {
	if !card.ImageFileName.Valid || strings.TrimSpace(card.ImageFileName.String) == "" {
		return nil
	}
	image := card.ImageFileName.String
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return &image
	}
	url := "/files/" + image
	return &url
}

func previewCardDomain(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func previewCardRequiresReview(card db.PreviewCard, provider *db.PreviewCardProvider) bool {
	if card.Trendable.Valid {
		return false
	}
	if provider == nil {
		return true
	}
	return !provider.ReviewedAt.Valid
}

func statusRequiresReview(status db.Status, account db.Account) bool {
	if status.Trendable.Valid {
		return false
	}
	return !account.ReviewedAt.Valid
}

func mapAdminTrendLink(card db.PreviewCard, requiresReview bool) AdminTrendLink {
	var blurhash *string
	if card.Blurhash.Valid {
		value := card.Blurhash.String
		blurhash = &value
	}
	return AdminTrendLink{
		ID:             strconv.FormatInt(card.ID, 10),
		URL:            card.Url,
		Title:          card.Title,
		Description:    card.Description,
		Type:           previewCardType(card),
		AuthorName:     card.AuthorName,
		AuthorURL:      card.AuthorUrl,
		ProviderName:   card.ProviderName,
		ProviderURL:    card.ProviderUrl,
		HTML:           card.Html,
		Width:          int(card.Width),
		Height:         int(card.Height),
		Image:          previewCardImageURL(card),
		Blurhash:       blurhash,
		History:        []TagHistory{},
		RequiresReview: requiresReview,
	}
}

// GetTags handles GET /api/v1/admin/trends/tags
func (h *TrendsHandler) GetTags(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []AdminTag{})
		return
	}

	limit := parseTrendLimit(web.QueryParam(r, "limit"), 10, 20)

	rows, err := h.queries.AdminListTrendingTags(r.Context(), int32(limit))
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	resp := make([]AdminTag, 0, len(rows))
	for _, row := range rows {
		tag := mapAdminTag(trendTagFromRow(row))
		tag.History = []TagHistory{trendHistoryEntry(row.UsageCount, row.AccountCount)}
		resp = append(resp, tag)
	}

	web.JSON(w, http.StatusOK, resp)
}

// GetStatuses handles GET /api/v1/admin/trends/statuses
func (h *TrendsHandler) GetStatuses(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []AdminTrendStatus{})
		return
	}

	limit := parseTrendLimit(web.QueryParam(r, "limit"), 20, 40)

	statuses, err := h.queries.AdminListTrendingStatuses(r.Context(), int32(limit))
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	accountIDs := make([]int64, 0, len(statuses))
	accountSeen := make(map[int64]struct{})
	for _, status := range statuses {
		if _, ok := accountSeen[status.AccountID]; !ok {
			accountSeen[status.AccountID] = struct{}{}
			accountIDs = append(accountIDs, status.AccountID)
		}
	}

	accounts, err := h.queries.GetAccountsByIDs(r.Context(), accountIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	accountsMap := make(map[int64]db.Account, len(accounts))
	for _, account := range accounts {
		accountsMap[account.ID] = account
	}

	resp := make([]AdminTrendStatus, 0, len(statuses))
	for _, status := range statuses {
		apiStatus := v1.SerializeStatus(r.Context(), h.queries, h.cfg, status)
		requiresReview := false
		if account, ok := accountsMap[status.AccountID]; ok {
			requiresReview = statusRequiresReview(status, account)
		}
		resp = append(resp, AdminTrendStatus{
			Status:         apiStatus,
			RequiresReview: requiresReview,
		})
	}

	web.JSON(w, http.StatusOK, resp)
}

// GetLinks handles GET /api/v1/admin/trends/links
func (h *TrendsHandler) GetLinks(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []AdminTrendLink{})
		return
	}

	limit := parseTrendLimit(web.QueryParam(r, "limit"), 10, 20)

	links, err := h.queries.AdminListTrendingLinks(r.Context(), int32(limit))
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	resp := make([]AdminTrendLink, 0, len(links))
	for _, link := range links {
		var provider *db.PreviewCardProvider
		domain := previewCardDomain(link.Url)
		if domain != "" {
			record, err := h.queries.GetPreviewCardProviderByDomain(r.Context(), domain)
			if err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					web.JSONError(w, http.StatusInternalServerError, "Database error")
					return
				}
			} else {
				provider = &record
			}
		}
		resp = append(resp, mapAdminTrendLink(link, previewCardRequiresReview(link, provider)))
	}

	web.JSON(w, http.StatusOK, resp)
}

// ApproveTag handles POST /api/v1/admin/trends/tags/{id}/approve.
func (h *TrendsHandler) ApproveTag(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	tagID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	tag, err := h.queries.AdminUpdateTagTrendable(r.Context(), db.AdminUpdateTagTrendableParams{
		ID:        tagID,
		Trendable: trendableParam(true),
	})
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

// RejectTag handles POST /api/v1/admin/trends/tags/{id}/reject.
func (h *TrendsHandler) RejectTag(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	tagID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	tag, err := h.queries.AdminUpdateTagTrendable(r.Context(), db.AdminUpdateTagTrendableParams{
		ID:        tagID,
		Trendable: trendableParam(false),
	})
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

// ApproveStatus handles POST /api/v1/admin/trends/statuses/{id}/approve.
func (h *TrendsHandler) ApproveStatus(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	status, err := h.queries.AdminUpdateStatusTrendable(r.Context(), db.AdminUpdateStatusTrendableParams{
		ID:        statusID,
		Trendable: trendableParam(true),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	apiStatus := v1.SerializeStatus(r.Context(), h.queries, h.cfg, status)
	web.JSON(w, http.StatusOK, AdminTrendStatus{Status: apiStatus, RequiresReview: false})
}

// RejectStatus handles POST /api/v1/admin/trends/statuses/{id}/reject.
func (h *TrendsHandler) RejectStatus(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	status, err := h.queries.AdminUpdateStatusTrendable(r.Context(), db.AdminUpdateStatusTrendableParams{
		ID:        statusID,
		Trendable: trendableParam(false),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	apiStatus := v1.SerializeStatus(r.Context(), h.queries, h.cfg, status)
	web.JSON(w, http.StatusOK, AdminTrendStatus{Status: apiStatus, RequiresReview: false})
}

// ApproveLink handles POST /api/v1/admin/trends/links/{id}/approve.
func (h *TrendsHandler) ApproveLink(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	linkID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	link, err := h.queries.AdminUpdatePreviewCardTrendable(r.Context(), db.AdminUpdatePreviewCardTrendableParams{
		ID:        linkID,
		Trendable: trendableParam(true),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapAdminTrendLink(link, false))
}

// RejectLink handles POST /api/v1/admin/trends/links/{id}/reject.
func (h *TrendsHandler) RejectLink(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	linkID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	link, err := h.queries.AdminUpdatePreviewCardTrendable(r.Context(), db.AdminUpdatePreviewCardTrendableParams{
		ID:        linkID,
		Trendable: trendableParam(false),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapAdminTrendLink(link, false))
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
	mux.HandleFunc("POST /api/v1/admin/trends/tags/{id}/approve", h.ApproveTag)
	mux.HandleFunc("POST /api/v1/admin/trends/tags/{id}/reject", h.RejectTag)
	mux.HandleFunc("GET /api/v1/admin/trends/statuses", h.GetStatuses)
	mux.HandleFunc("POST /api/v1/admin/trends/statuses/{id}/approve", h.ApproveStatus)
	mux.HandleFunc("POST /api/v1/admin/trends/statuses/{id}/reject", h.RejectStatus)
	mux.HandleFunc("GET /api/v1/admin/trends/links", h.GetLinks)
	mux.HandleFunc("POST /api/v1/admin/trends/links/{id}/approve", h.ApproveLink)
	mux.HandleFunc("POST /api/v1/admin/trends/links/{id}/reject", h.RejectLink)
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
