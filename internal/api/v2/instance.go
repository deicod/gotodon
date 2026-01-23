package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

const (
	defaultInstanceTitle                  = "Gotodon"
	defaultInstanceDescription            = "A Mastodon-compatible server"
	defaultInstanceVersion                = "4.5.3"
	defaultInstanceSourceURL              = "https://github.com/deicod/gotodon"
	defaultInstanceDomainFallback         = "example.com"
	defaultInstanceMaxImageSize           = int64(16777216)
	defaultInstanceMaxVideoSize           = int64(103809024)
	defaultInstanceMaxMediaPerPost        = 4
	defaultInstanceMaxTootChars           = 500
	defaultInstanceMaxPollOptions         = 4
	defaultInstanceMaxPollChars           = 50
	defaultInstanceURLPlaceholder         = 23
	defaultInstanceMinPollExpiration      = 300
	defaultInstanceMaxPollExpiration      = 2629746
	defaultInstanceImageMatrixLimit       = 33177600
	defaultInstanceVideoMatrixLimit       = 8294400
	defaultInstanceVideoFrameRate         = 120
	defaultInstanceMaxFeaturedTags        = 10
	defaultInstanceMaxPinnedStatuses      = 5
	defaultInstanceMaxProfileFields       = 4
	defaultInstanceProfileFieldNameLimit  = 255
	defaultInstanceProfileFieldValueLimit = 255
)

const (
	settingSiteTitle            = "site_title"
	settingSiteDescription      = "site_description"
	settingSiteLanguages        = "site_languages"
	settingRegistrationsEnabled = "registrations_enabled"
	settingApprovalRequired     = "approval_required"
	settingMaxTootChars         = "max_toot_chars"
	settingMaxPollOptions       = "max_poll_options"
	settingMaxPollOptionChars   = "max_poll_option_chars"
	settingMaxImageSize         = "max_image_size"
	settingMaxVideoSize         = "max_video_size"
	settingMaxMediaAttachments  = "max_media_attachments"
	settingSiteContactEmail     = "site_contact_email"
	settingSiteContactUsername  = "site_contact_username"
)

var instanceSettingKeys = []string{
	settingSiteTitle,
	settingSiteDescription,
	settingSiteLanguages,
	settingRegistrationsEnabled,
	settingApprovalRequired,
	settingMaxTootChars,
	settingMaxPollOptions,
	settingMaxPollOptionChars,
	settingMaxImageSize,
	settingMaxVideoSize,
	settingMaxMediaAttachments,
	settingSiteContactEmail,
	settingSiteContactUsername,
}

var instanceSupportedMimeTypes = []string{
	"image/jpeg",
	"image/png",
	"image/gif",
	"image/heic",
	"image/heif",
	"image/webp",
	"image/avif",
	"video/webm",
	"video/mp4",
	"video/quicktime",
	"video/ogg",
	"audio/wave",
	"audio/wav",
	"audio/x-wav",
	"audio/x-pn-wave",
	"audio/vnd.wave",
	"audio/ogg",
	"audio/vorbis",
	"audio/mpeg",
	"audio/mp3",
	"audio/webm",
	"audio/flac",
	"audio/aac",
	"audio/m4a",
	"audio/x-m4a",
	"audio/mp4",
	"audio/3gpp",
	"video/x-ms-asf",
}

type instanceQuerier interface {
	GetSettingsByVars(ctx context.Context, vars []string) ([]db.GetSettingsByVarsRow, error)
	ListRules(ctx context.Context) ([]db.Rule, error)
	GetCurrentTermsOfService(ctx context.Context) (db.TermsOfService, error)
	CountActiveUsers(ctx context.Context) (int64, error)
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error)
}

type instanceSettings struct {
	Title            string
	Description      string
	Languages        []string
	Registrations    bool
	ApprovalRequired bool
	MaxTootChars     int
	MaxMedia         int
	MaxPollOptions   int
	MaxPollChars     int
	MaxImageSize     int64
	MaxVideoSize     int64
	ContactEmail     string
	ContactUsername  string
}

type InstanceHandler struct {
	queries instanceQuerier
	cfg     *config.Config
}

func NewInstanceHandler(queries instanceQuerier, cfg *config.Config) *InstanceHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &InstanceHandler{queries: queries, cfg: cfg}
}

// Get handles GET /api/v2/instance
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings := h.loadInstanceSettings(ctx)

	domain := instancePublicDomain(h.cfg)
	if domain == "" {
		domain = defaultInstanceDomainFallback
	}
	baseURL := ""
	if domain != "" {
		baseURL = "https://" + domain
	}

	streamingURL := ""
	if domain != "" {
		streamingURL = "wss://" + domain + "/api/v1/streaming"
	}

	termsURL := any(nil)
	if domain != "" && hasCurrentTermsOfService(ctx, h.queries) {
		termsURL = baseURL + "/terms-of-service"
	}

	thumbnailURL := ""
	if baseURL != "" {
		thumbnailURL = baseURL + "/images/preview.png"
	}

	icons := make([]map[string]any, 0, len(androidIconSizes))
	for _, size := range androidIconSizes {
		src := "/icons/android-chrome-" + strconv.Itoa(size) + "x" + strconv.Itoa(size) + ".png"
		if baseURL != "" {
			src = baseURL + src
		}
		icons = append(icons, map[string]any{
			"src":  src,
			"size": strconv.Itoa(size) + "x" + strconv.Itoa(size),
		})
	}

	languages := settings.Languages
	if len(languages) == 0 {
		languages = []string{"en"}
	}

	contactEmail := strings.TrimSpace(settings.ContactEmail)
	if contactEmail == "" && domain != "" {
		contactEmail = "admin@" + domain
	}
	contactAccount := h.contactAccount(ctx, settings.ContactUsername)

	activeUsers := int64(0)
	if h.queries != nil {
		if count, err := h.queries.CountActiveUsers(ctx); err == nil {
			activeUsers = count
		}
	}

	rules := instanceRules(ctx, h.queries)

	resp := map[string]any{
		"domain":      domain,
		"title":       settings.Title,
		"version":     defaultInstanceVersion,
		"source_url":  defaultInstanceSourceURL,
		"description": settings.Description,
		"usage": map[string]any{
			"users": map[string]any{
				"active_month": activeUsers,
			},
		},
		"thumbnail": map[string]any{
			"url": thumbnailURL,
		},
		"icon":      icons,
		"languages": languages,
		"configuration": map[string]any{
			"urls": map[string]any{
				"streaming":        streamingURL,
				"status":           nil,
				"about":            baseURL + "/about",
				"privacy_policy":   baseURL + "/privacy-policy",
				"terms_of_service": termsURL,
			},
			"vapid": map[string]any{
				"public_key": h.cfg.Secrets.VAPIDPublic,
			},
			"accounts": map[string]any{
				"max_featured_tags":         defaultInstanceMaxFeaturedTags,
				"max_pinned_statuses":       defaultInstanceMaxPinnedStatuses,
				"max_profile_fields":        defaultInstanceMaxProfileFields,
				"profile_field_name_limit":  defaultInstanceProfileFieldNameLimit,
				"profile_field_value_limit": defaultInstanceProfileFieldValueLimit,
			},
			"statuses": map[string]any{
				"max_characters":              settings.MaxTootChars,
				"max_media_attachments":       settings.MaxMedia,
				"characters_reserved_per_url": defaultInstanceURLPlaceholder,
			},
			"media_attachments": map[string]any{
				"description_limit":      1500,
				"image_matrix_limit":     defaultInstanceImageMatrixLimit,
				"image_size_limit":       settings.MaxImageSize,
				"supported_mime_types":   instanceSupportedMimeTypes,
				"video_frame_rate_limit": defaultInstanceVideoFrameRate,
				"video_matrix_limit":     defaultInstanceVideoMatrixLimit,
				"video_size_limit":       settings.MaxVideoSize,
			},
			"polls": map[string]any{
				"max_options":               settings.MaxPollOptions,
				"max_characters_per_option": settings.MaxPollChars,
				"min_expiration":            defaultInstanceMinPollExpiration,
				"max_expiration":            defaultInstanceMaxPollExpiration,
			},
			"translation": map[string]any{
				"enabled": h.cfg.Translation.Enabled(),
			},
			"timelines_access": map[string]any{
				"live_feeds": map[string]any{
					"local":  "public",
					"remote": "public",
				},
				"hashtag_feeds": map[string]any{
					"local":  "public",
					"remote": "public",
				},
				"trending_link_feeds": map[string]any{
					"local":  "public",
					"remote": "public",
				},
			},
			"limited_federation": false,
		},
		"registrations": map[string]any{
			"enabled":           settings.Registrations,
			"approval_required": settings.ApprovalRequired,
			"reason_required":   settings.ApprovalRequired,
			"message":           nil,
			"min_age":           nil,
			"url":               nil,
		},
		"contact": map[string]any{
			"email":   contactEmail,
			"account": contactAccount,
		},
		"api_versions": map[string]any{
			"mastodon": 7,
		},
		"rules": rules,
	}

	web.JSON(w, http.StatusOK, resp)
}

func (h *InstanceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/instance", h.Get)
}

func (h *InstanceHandler) loadInstanceSettings(ctx context.Context) instanceSettings {
	registrations, approvalRequired := defaultRegistrations(h.cfg)
	settings := instanceSettings{
		Title:            defaultInstanceTitle,
		Description:      defaultInstanceDescription,
		Languages:        []string{"en"},
		Registrations:    registrations,
		ApprovalRequired: approvalRequired,
		MaxTootChars:     defaultInstanceMaxTootChars,
		MaxMedia:         defaultInstanceMaxMediaPerPost,
		MaxPollOptions:   defaultInstanceMaxPollOptions,
		MaxPollChars:     defaultInstanceMaxPollChars,
		MaxImageSize:     defaultInstanceMaxImageSize,
		MaxVideoSize:     defaultInstanceMaxVideoSize,
	}

	if h == nil || h.queries == nil {
		return settings
	}

	rows, err := h.queries.GetSettingsByVars(ctx, instanceSettingKeys)
	if err != nil {
		return settings
	}

	for _, row := range rows {
		if !row.Value.Valid {
			continue
		}
		switch row.Var {
		case settingSiteTitle:
			settings.Title = row.Value.String
		case settingSiteDescription:
			settings.Description = row.Value.String
		case settingSiteLanguages:
			var langs []string
			if err := json.Unmarshal([]byte(row.Value.String), &langs); err == nil && len(langs) > 0 {
				settings.Languages = langs
			}
		case settingRegistrationsEnabled:
			if parsed, err := strconv.ParseBool(row.Value.String); err == nil {
				settings.Registrations = parsed
			}
		case settingApprovalRequired:
			if parsed, err := strconv.ParseBool(row.Value.String); err == nil {
				settings.ApprovalRequired = parsed
			}
		case settingMaxTootChars:
			if parsed, err := strconv.Atoi(row.Value.String); err == nil {
				settings.MaxTootChars = parsed
			}
		case settingMaxMediaAttachments:
			if parsed, err := strconv.Atoi(row.Value.String); err == nil {
				settings.MaxMedia = parsed
			}
		case settingMaxPollOptions:
			if parsed, err := strconv.Atoi(row.Value.String); err == nil {
				settings.MaxPollOptions = parsed
			}
		case settingMaxPollOptionChars:
			if parsed, err := strconv.Atoi(row.Value.String); err == nil {
				settings.MaxPollChars = parsed
			}
		case settingMaxImageSize:
			if parsed, err := strconv.ParseInt(row.Value.String, 10, 64); err == nil {
				settings.MaxImageSize = parsed
			}
		case settingMaxVideoSize:
			if parsed, err := strconv.ParseInt(row.Value.String, 10, 64); err == nil {
				settings.MaxVideoSize = parsed
			}
		case settingSiteContactEmail:
			settings.ContactEmail = row.Value.String
		case settingSiteContactUsername:
			settings.ContactUsername = row.Value.String
		}
	}

	return settings
}

func (h *InstanceHandler) contactAccount(ctx context.Context, username string) *v1.Account {
	if h == nil || h.queries == nil {
		return nil
	}

	handle := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if handle == "" {
		return nil
	}

	parts := strings.SplitN(handle, "@", 2)
	user := strings.TrimSpace(parts[0])
	if user == "" {
		return nil
	}

	var acc db.Account
	var err error
	if len(parts) == 1 {
		acc, err = h.queries.GetAccountByUsername(ctx, user)
	} else {
		domain := strings.TrimSpace(parts[1])
		if h.cfg != nil {
			if strings.EqualFold(domain, h.cfg.Instance.LocalDomain) || strings.EqualFold(domain, h.cfg.Instance.WebDomain) {
				domain = ""
			}
		}
		if domain == "" {
			acc, err = h.queries.GetAccountByUsername(ctx, user)
		} else {
			acc, err = h.queries.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
				Username: user,
				Domain:   domain,
			})
		}
	}

	if err != nil {
		return nil
	}

	if dataQueries, ok := h.queries.(v1.AccountDataQuerier); ok {
		serialized := v1.SerializeAccounts(ctx, dataQueries, h.cfg, []db.Account{acc}, true)
		if api, ok := serialized[acc.ID]; ok {
			return &api
		}
	}
	api := v1.SerializeAccount(h.cfg, acc)
	return &api
}

var androidIconSizes = []int{36, 48, 72, 96, 144, 192, 256, 384, 512}

func instancePublicDomain(cfg *config.Config) string {
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

func instanceRules(ctx context.Context, queries instanceQuerier) []v1.RuleResponse {
	if queries == nil {
		return []v1.RuleResponse{
			{ID: "1", Text: "Be respectful to others."},
			{ID: "2", Text: "No spam or advertising."},
			{ID: "3", Text: "No illegal content."},
		}
	}

	rules, err := queries.ListRules(ctx)
	if err != nil {
		return []v1.RuleResponse{}
	}

	resp := make([]v1.RuleResponse, len(rules))
	for i, rule := range rules {
		resp[i] = v1.RuleResponse{
			ID:   strconv.FormatInt(rule.ID, 10),
			Text: rule.Text,
			Hint: rule.Hint,
		}
	}
	return resp
}

func hasCurrentTermsOfService(ctx context.Context, queries instanceQuerier) bool {
	if queries == nil {
		return false
	}
	_, err := queries.GetCurrentTermsOfService(ctx)
	return err == nil
}
