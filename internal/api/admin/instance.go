// Package admin provides instance configuration for the Admin API.
package admin

import (
	"context"
	"encoding/json"
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
	queries *db.Queries
	cfg     *config.Config
}

// NewInstanceHandler creates a new instance handler
func NewInstanceHandler(queries *db.Queries, cfg *config.Config) *InstanceHandler {
	return &InstanceHandler{queries: queries, cfg: cfg}
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

const (
	settingSiteTitle              = "site_title"
	settingSiteDescription        = "site_description"
	settingSiteLanguages          = "site_languages"
	settingRegistrationsEnabled   = "registrations_enabled"
	settingApprovalRequired       = "approval_required"
	settingInvitesEnabled         = "invites_enabled"
	settingMaxTootChars           = "max_toot_chars"
	settingMaxPollOptions         = "max_poll_options"
	settingMaxPollOptionChars     = "max_poll_option_chars"
	settingMaxImageSize           = "max_image_size"
	settingMaxVideoSize           = "max_video_size"
	settingMaxMediaAttachments    = "max_media_attachments"
	settingMaxCustomEmojis        = "max_custom_emojis"
	defaultInstanceTitle          = "Gotodon"
	defaultInstanceVersion        = "4.0.0-gotodon"
	defaultInstanceSourceURL      = "https://github.com/deicod/gotodon"
	defaultInstanceDescription    = "A Mastodon-compatible server"
	defaultInstanceDomainFallback = "example.com"
)

var instanceSettingKeys = []string{
	settingSiteTitle,
	settingSiteDescription,
	settingSiteLanguages,
	settingRegistrationsEnabled,
	settingApprovalRequired,
	settingInvitesEnabled,
	settingMaxTootChars,
	settingMaxPollOptions,
	settingMaxPollOptionChars,
	settingMaxImageSize,
	settingMaxVideoSize,
	settingMaxMediaAttachments,
	settingMaxCustomEmojis,
}

func instanceDomain(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if strings.TrimSpace(cfg.Instance.WebDomain) != "" {
		return cfg.Instance.WebDomain
	}
	return cfg.Instance.LocalDomain
}

func defaultRegistrations(cfg *config.Config) (bool, bool) {
	mode := ""
	singleUser := false
	if cfg != nil {
		mode = strings.ToLower(strings.TrimSpace(cfg.Instance.RegistrationsMode))
		singleUser = cfg.Instance.SingleUserMode
	}
	if mode == "" {
		mode = "open"
	}
	registrations := mode != "none" && !singleUser
	approvalRequired := mode == "approved"
	return registrations, approvalRequired
}

func defaultInstanceConfig(cfg *config.Config) InstanceConfig {
	registrations, approvalRequired := defaultRegistrations(cfg)
	domain := strings.TrimSpace(instanceDomain(cfg))
	if domain == "" {
		domain = defaultInstanceDomainFallback
	}
	return InstanceConfig{
		Domain:               domain,
		Title:                defaultInstanceTitle,
		Version:              defaultInstanceVersion,
		SourceURL:            defaultInstanceSourceURL,
		Description:          defaultInstanceDescription,
		Languages:            []string{"en"},
		Registrations:        registrations,
		ApprovalRequired:     approvalRequired,
		InvitesEnabled:       true,
		MaxTootChars:         500,
		MaxPollOptions:       4,
		MaxPollCharPerOption: 50,
		MaxImageSize:         10485760,
		MaxVideoSize:         41943040,
		MaxMediaAttachments:  4,
		MaxCustomEmojis:      5000,
	}
}

func applyInstanceSetting(cfg *InstanceConfig, key, value string) {
	switch key {
	case settingSiteTitle:
		cfg.Title = value
	case settingSiteDescription:
		cfg.Description = value
	case settingSiteLanguages:
		var languages []string
		if err := json.Unmarshal([]byte(value), &languages); err == nil {
			cfg.Languages = languages
		}
	case settingRegistrationsEnabled:
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Registrations = parsed
		}
	case settingApprovalRequired:
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.ApprovalRequired = parsed
		}
	case settingInvitesEnabled:
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.InvitesEnabled = parsed
		}
	case settingMaxTootChars:
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.MaxTootChars = parsed
		}
	case settingMaxPollOptions:
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.MaxPollOptions = parsed
		}
	case settingMaxPollOptionChars:
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.MaxPollCharPerOption = parsed
		}
	case settingMaxMediaAttachments:
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.MaxMediaAttachments = parsed
		}
	case settingMaxCustomEmojis:
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.MaxCustomEmojis = parsed
		}
	case settingMaxImageSize:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.MaxImageSize = parsed
		}
	case settingMaxVideoSize:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			cfg.MaxVideoSize = parsed
		}
	}
}

func (h *InstanceHandler) loadInstanceConfig(ctx context.Context) InstanceConfig {
	if h == nil {
		return defaultInstanceConfig(nil)
	}
	cfg := defaultInstanceConfig(h.cfg)
	if h.queries == nil {
		return cfg
	}
	settings, err := h.queries.GetSettingsByVars(ctx, instanceSettingKeys)
	if err != nil {
		return cfg
	}
	for _, setting := range settings {
		if !setting.Value.Valid {
			continue
		}
		applyInstanceSetting(&cfg, setting.Var, setting.Value.String)
	}
	return cfg
}

// Get handles GET /api/v1/admin/instance
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	web.JSON(w, http.StatusOK, h.loadInstanceConfig(r.Context()))
}

// Update handles PATCH /api/v1/admin/instance
func (h *InstanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

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

	updates := map[string]string{}
	if req.Title != nil {
		updates[settingSiteTitle] = *req.Title
	}
	if req.Description != nil {
		updates[settingSiteDescription] = *req.Description
	}
	if len(req.Languages) > 0 {
		encoded, err := json.Marshal(req.Languages)
		if err != nil {
			web.JSONError(w, http.StatusBadRequest, "Invalid languages value")
			return
		}
		updates[settingSiteLanguages] = string(encoded)
	}
	if req.Registrations != nil {
		updates[settingRegistrationsEnabled] = strconv.FormatBool(*req.Registrations)
	}
	if req.ApprovalRequired != nil {
		updates[settingApprovalRequired] = strconv.FormatBool(*req.ApprovalRequired)
	}
	if req.InvitesEnabled != nil {
		updates[settingInvitesEnabled] = strconv.FormatBool(*req.InvitesEnabled)
	}

	if h.queries == nil {
		cfg := h.loadInstanceConfig(r.Context())
		for key, value := range updates {
			applyInstanceSetting(&cfg, key, value)
		}
		web.JSON(w, http.StatusOK, cfg)
		return
	}

	for key, value := range updates {
		if err := h.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{
			Var:   key,
			Value: pgtype.Text{String: value, Valid: true},
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}

	web.JSON(w, http.StatusOK, h.loadInstanceConfig(r.Context()))
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

// AdminTrendLinkPublisher represents a link publisher in admin trends responses.
type AdminTrendLinkPublisher struct {
	ID                string  `json:"id"`
	Domain            string  `json:"domain"`
	Trendable         bool    `json:"trendable"`
	ReviewedAt        *string `json:"reviewed_at"`
	RequestedReviewAt *string `json:"requested_review_at"`
	RequiresReview    bool    `json:"requires_review"`
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

func previewCardProviderTrendable(value pgtype.Bool) bool {
	if value.Valid {
		return value.Bool
	}
	return false
}

func previewCardProviderRequiresReview(reviewedAt pgtype.Timestamptz) bool {
	return !reviewedAt.Valid
}

func timestamptzToString(value pgtype.Timestamptz) *string {
	if !value.Valid {
		return nil
	}
	formatted := value.Time.Format(time.RFC3339)
	return &formatted
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

func mapAdminTrendLinkPublisher(provider db.PreviewCardProvider) AdminTrendLinkPublisher {
	return AdminTrendLinkPublisher{
		ID:                strconv.FormatInt(provider.ID, 10),
		Domain:            provider.Domain,
		Trendable:         previewCardProviderTrendable(provider.Trendable),
		ReviewedAt:        timestamptzToString(provider.ReviewedAt),
		RequestedReviewAt: timestamptzToString(provider.RequestedReviewAt),
		RequiresReview:    previewCardProviderRequiresReview(provider.ReviewedAt),
	}
}

// GetTags handles GET /api/v1/admin/trends/tags
func (h *TrendsHandler) GetTags(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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

// GetLinkPublishers handles GET /api/v1/admin/trends/links/publishers
func (h *TrendsHandler) GetLinkPublishers(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
		return
	}

	limit := parseTrendLimit(web.QueryParam(r, "limit"), 100, 100)
	maxID, _ := strconv.ParseInt(web.QueryParam(r, "max_id"), 10, 64)
	sinceID, _ := strconv.ParseInt(web.QueryParam(r, "since_id"), 10, 64)
	minID, _ := strconv.ParseInt(web.QueryParam(r, "min_id"), 10, 64)

	var providers []db.PreviewCardProvider
	var err error
	if minID > 0 {
		providers, err = h.queries.AdminListPreviewCardProvidersMinID(r.Context(), db.AdminListPreviewCardProvidersMinIDParams{
			Limit: int32(limit),
			MinID: minID,
			MaxID: maxID,
		})
	} else {
		providers, err = h.queries.AdminListPreviewCardProviders(r.Context(), db.AdminListPreviewCardProvidersParams{
			Limit:   int32(limit),
			MaxID:   maxID,
			SinceID: sinceID,
		})
	}
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	if minID > 0 && len(providers) > 1 {
		for i, j := 0, len(providers)-1; i < j; i, j = i+1, j-1 {
			providers[i], providers[j] = providers[j], providers[i]
		}
	}

	resp := make([]AdminTrendLinkPublisher, 0, len(providers))
	for _, provider := range providers {
		resp = append(resp, mapAdminTrendLinkPublisher(provider))
	}

	web.JSON(w, http.StatusOK, resp)
}

// ApproveTag handles POST /api/v1/admin/trends/tags/{id}/approve.
func (h *TrendsHandler) ApproveTag(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
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

// ApproveLinkPublisher handles POST /api/v1/admin/trends/links/publishers/{id}/approve.
func (h *TrendsHandler) ApproveLinkPublisher(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
		return
	}

	providerID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	provider, err := h.queries.UpdatePreviewCardProviderTrendable(r.Context(), db.UpdatePreviewCardProviderTrendableParams{
		ID:        providerID,
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

	web.JSON(w, http.StatusOK, mapAdminTrendLinkPublisher(provider))
}

// RejectLinkPublisher handles POST /api/v1/admin/trends/links/publishers/{id}/reject.
func (h *TrendsHandler) RejectLinkPublisher(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSONError(w, http.StatusInternalServerError, "Trends data unavailable")
		return
	}

	providerID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	provider, err := h.queries.UpdatePreviewCardProviderTrendable(r.Context(), db.UpdatePreviewCardProviderTrendableParams{
		ID:        providerID,
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

	web.JSON(w, http.StatusOK, mapAdminTrendLinkPublisher(provider))
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
	mux.HandleFunc("GET /api/v1/admin/trends/links/publishers", h.GetLinkPublishers)
	mux.HandleFunc("POST /api/v1/admin/trends/links/publishers/{id}/approve", h.ApproveLinkPublisher)
	mux.HandleFunc("POST /api/v1/admin/trends/links/publishers/{id}/reject", h.RejectLinkPublisher)
}
