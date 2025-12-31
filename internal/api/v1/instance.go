package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

const (
	defaultInstanceTitle             = "Gotodon"
	defaultInstanceDescription       = "A Mastodon-compatible server"
	defaultInstanceVersion           = "4.5.3"
	defaultInstanceDomainFallback    = "example.com"
	defaultInstanceMaxImageSize      = int64(16777216)
	defaultInstanceMaxVideoSize      = int64(103809024)
	defaultInstanceMaxMediaPerPost   = 4
	defaultInstanceMaxFeaturedTags   = 10
	defaultInstanceMaxTootChars      = 500
	defaultInstanceMaxPollOptions    = 4
	defaultInstanceMaxPollChars      = 50
	defaultInstanceURLPlaceholder    = 23
	defaultInstanceMinPollExpiration = 300
	defaultInstanceMaxPollExpiration = 2629746
	defaultInstanceImageMatrixLimit  = 33177600
	defaultInstanceVideoMatrixLimit  = 8294400
	defaultInstanceVideoFrameRate    = 120
)

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

const (
	settingSiteTitle            = "site_title"
	settingSiteDescription      = "site_description"
	settingSiteShortDescription = "site_short_description"
	settingSiteLanguages        = "site_languages"
	settingRegistrationsEnabled = "registrations_enabled"
	settingApprovalRequired     = "approval_required"
	settingInvitesEnabled       = "invites_enabled"
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
	settingSiteShortDescription,
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
	settingSiteContactEmail,
	settingSiteContactUsername,
}

type instanceQuerier interface {
	GetSettingsByVars(ctx context.Context, vars []string) ([]db.GetSettingsByVarsRow, error)
	ListRules(ctx context.Context) ([]db.Rule, error)
	CountInstanceUsers(ctx context.Context) (int64, error)
	CountInstanceStatuses(ctx context.Context) (int64, error)
	CountInstanceDomains(ctx context.Context) (int64, error)
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error)
}

// InstanceHandler handles GET /api/v1/instance
// Mirrors the Mastodon v1 instance serializer.
type InstanceHandler struct {
	queries instanceQuerier
	cfg     *config.Config
}

// NewInstanceHandler creates a new instance handler.
func NewInstanceHandler(queries instanceQuerier, cfg *config.Config) *InstanceHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &InstanceHandler{queries: queries, cfg: cfg}
}

type instanceSettings struct {
	Title            string
	ShortDescription string
	Description      string
	Languages        []string
	Registrations    bool
	ApprovalRequired bool
	InvitesEnabled   bool
	MaxTootChars     int
	MaxMedia         int
	MaxPollOptions   int
	MaxPollChars     int
	MaxImageSize     int64
	MaxVideoSize     int64
	ContactEmail     string
	ContactUsername  string
}

type InstanceResponse struct {
	URI              string                `json:"uri"`
	Title            string                `json:"title"`
	ShortDesc        string                `json:"short_description"`
	Description      string                `json:"description"`
	Email            string                `json:"email"`
	Version          string                `json:"version"`
	URLs             InstanceURLs          `json:"urls"`
	Stats            InstanceStats         `json:"stats"`
	Thumbnail        string                `json:"thumbnail"`
	Languages        []string              `json:"languages"`
	Registrations    bool                  `json:"registrations"`
	ApprovalRequired bool                  `json:"approval_required"`
	InvitesEnabled   bool                  `json:"invites_enabled"`
	Configuration    InstanceConfiguration `json:"configuration"`
	ContactAccount   *Account              `json:"contact_account"`
	Rules            []RuleResponse        `json:"rules"`
}

type InstanceURLs struct {
	StreamingAPI string `json:"streaming_api"`
}

type InstanceStats struct {
	UserCount   int64 `json:"user_count"`
	StatusCount int64 `json:"status_count"`
	DomainCount int64 `json:"domain_count"`
}

type InstanceConfiguration struct {
	Accounts         InstanceConfigAccounts         `json:"accounts"`
	Statuses         InstanceConfigStatuses         `json:"statuses"`
	MediaAttachments InstanceConfigMediaAttachments `json:"media_attachments"`
	Polls            InstanceConfigPolls            `json:"polls"`
}

type InstanceConfigAccounts struct {
	MaxFeaturedTags int `json:"max_featured_tags"`
}

type InstanceConfigStatuses struct {
	MaxCharacters            int `json:"max_characters"`
	MaxMediaAttachments      int `json:"max_media_attachments"`
	CharactersReservedPerURL int `json:"characters_reserved_per_url"`
}

type InstanceConfigMediaAttachments struct {
	SupportedMimeTypes  []string `json:"supported_mime_types"`
	ImageSizeLimit      int64    `json:"image_size_limit"`
	ImageMatrixLimit    int      `json:"image_matrix_limit"`
	VideoSizeLimit      int64    `json:"video_size_limit"`
	VideoFrameRateLimit int      `json:"video_frame_rate_limit"`
	VideoMatrixLimit    int      `json:"video_matrix_limit"`
}

type InstanceConfigPolls struct {
	MaxOptions             int `json:"max_options"`
	MaxCharactersPerOption int `json:"max_characters_per_option"`
	MinExpiration          int `json:"min_expiration"`
	MaxExpiration          int `json:"max_expiration"`
}

// Get handles GET /api/v1/instance
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings := h.loadInstanceSettings(ctx)

	federationDomain := instanceFederationDomain(h.cfg)
	publicDomain := instancePublicDomain(h.cfg)
	if federationDomain == "" {
		federationDomain = defaultInstanceDomainFallback
	}
	if publicDomain == "" {
		publicDomain = defaultInstanceDomainFallback
	}

	thumbnailURL := "https://" + publicDomain + "/images/preview.png"
	streamingAPI := "wss://" + publicDomain

	stats := InstanceStats{}
	if h.queries != nil {
		if count, err := h.queries.CountInstanceUsers(ctx); err == nil {
			stats.UserCount = count
		}
		if count, err := h.queries.CountInstanceStatuses(ctx); err == nil {
			stats.StatusCount = count
		}
		if count, err := h.queries.CountInstanceDomains(ctx); err == nil {
			stats.DomainCount = count
		}
	}

	languages := []string{"en"}
	if len(settings.Languages) > 0 {
		languages = settings.Languages
	}

	resp := InstanceResponse{
		URI:              federationDomain,
		Title:            settings.Title,
		ShortDesc:        settings.ShortDescription,
		Description:      settings.Description,
		Email:            settings.ContactEmail,
		Version:          defaultInstanceVersion,
		URLs:             InstanceURLs{StreamingAPI: streamingAPI},
		Stats:            stats,
		Thumbnail:        thumbnailURL,
		Languages:        languages,
		Registrations:    settings.Registrations,
		ApprovalRequired: settings.ApprovalRequired,
		InvitesEnabled:   settings.InvitesEnabled,
		Configuration: InstanceConfiguration{
			Accounts: InstanceConfigAccounts{
				MaxFeaturedTags: defaultInstanceMaxFeaturedTags,
			},
			Statuses: InstanceConfigStatuses{
				MaxCharacters:            settings.MaxTootChars,
				MaxMediaAttachments:      settings.MaxMedia,
				CharactersReservedPerURL: defaultInstanceURLPlaceholder,
			},
			MediaAttachments: InstanceConfigMediaAttachments{
				SupportedMimeTypes:  instanceSupportedMimeTypes,
				ImageSizeLimit:      settings.MaxImageSize,
				ImageMatrixLimit:    defaultInstanceImageMatrixLimit,
				VideoSizeLimit:      settings.MaxVideoSize,
				VideoFrameRateLimit: defaultInstanceVideoFrameRate,
				VideoMatrixLimit:    defaultInstanceVideoMatrixLimit,
			},
			Polls: InstanceConfigPolls{
				MaxOptions:             settings.MaxPollOptions,
				MaxCharactersPerOption: settings.MaxPollChars,
				MinExpiration:          defaultInstanceMinPollExpiration,
				MaxExpiration:          defaultInstanceMaxPollExpiration,
			},
		},
		ContactAccount: h.contactAccount(ctx, settings.ContactUsername),
		Rules:          instanceRules(ctx, h.queries),
	}

	if resp.ShortDesc == "" {
		resp.ShortDesc = resp.Description
	}

	web.JSON(w, http.StatusOK, resp)
}

func (h *InstanceHandler) loadInstanceSettings(ctx context.Context) instanceSettings {
	var cfg *config.Config
	if h != nil {
		cfg = h.cfg
	}
	registrations, approvalRequired := defaultRegistrations(cfg)
	settings := instanceSettings{
		Title:            defaultInstanceTitle,
		ShortDescription: defaultInstanceDescription,
		Description:      defaultInstanceDescription,
		Languages:        []string{"en"},
		Registrations:    registrations,
		ApprovalRequired: approvalRequired,
		InvitesEnabled:   true,
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
		case settingSiteShortDescription:
			settings.ShortDescription = row.Value.String
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
		case settingInvitesEnabled:
			if parsed, err := strconv.ParseBool(row.Value.String); err == nil {
				settings.InvitesEnabled = parsed
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

	if settings.ShortDescription == "" {
		settings.ShortDescription = settings.Description
	}

	return settings
}

func (h *InstanceHandler) contactAccount(ctx context.Context, username string) *Account {
	if h == nil || h.queries == nil {
		return nil
	}

	handle := strings.TrimSpace(strings.TrimPrefix(username, "@"))
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

	if dataQueries, ok := h.queries.(AccountDataQuerier); ok {
		serialized := serializeAccounts(ctx, dataQueries, h.cfg, []db.Account{acc}, true)
		if api, ok := serialized[acc.ID]; ok {
			return &api
		}
	}
	api := SerializeAccount(h.cfg, acc)
	return &api
}

func instanceRules(ctx context.Context, queries instanceQuerier) []RuleResponse {
	if queries == nil {
		return []RuleResponse{
			{ID: "1", Text: "Be respectful to others."},
			{ID: "2", Text: "No spam or advertising."},
			{ID: "3", Text: "No illegal content."},
		}
	}

	rules, err := queries.ListRules(ctx)
	if err != nil {
		return []RuleResponse{}
	}

	resp := make([]RuleResponse, len(rules))
	for i, rule := range rules {
		resp[i] = RuleResponse{
			ID:   strconv.FormatInt(rule.ID, 10),
			Text: rule.Text,
			Hint: rule.Hint,
		}
	}
	return resp
}

func instancePublicDomain(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if strings.TrimSpace(cfg.Instance.WebDomain) != "" {
		return cfg.Instance.WebDomain
	}
	return cfg.Instance.LocalDomain
}

func instanceFederationDomain(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if strings.TrimSpace(cfg.Instance.LocalDomain) != "" {
		return cfg.Instance.LocalDomain
	}
	return instancePublicDomain(cfg)
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

// RegisterRoutes registers instance routes.
func (h *InstanceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/instance", h.Get)
}
