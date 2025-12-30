package v2

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

type InstanceHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

func NewInstanceHandler(queries *db.Queries, cfg *config.Config) *InstanceHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &InstanceHandler{queries: queries, cfg: cfg}
}

// Get handles GET /api/v2/instance
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	domain := instancePublicDomain(h.cfg)
	baseURL := ""
	if domain != "" {
		baseURL = "https://" + domain
	}

	streamingURL := ""
	if domain != "" {
		streamingURL = "wss://" + domain + "/api/v1/streaming"
	}

	termsURL := any(nil)
	if domain != "" && hasCurrentTermsOfService(r.Context(), h.queries) {
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

	regEnabled, approvalRequired, reasonRequired := registrationsConfig(h.cfg)

	contactAccount := map[string]any{
		"id":              "1",
		"username":        "admin",
		"acct":            "admin",
		"display_name":    "Admin",
		"url":             baseURL + "/@admin",
		"avatar":          baseURL + "/avatars/original/missing.png",
		"avatar_static":   baseURL + "/avatars/original/missing.png",
		"header":          baseURL + "/headers/original/missing.png",
		"header_static":   baseURL + "/headers/original/missing.png",
		"locked":          false,
		"bot":             false,
		"discoverable":    true,
		"indexable":       true,
		"group":           false,
		"created_at":      time.Now().UTC().Format(time.RFC3339),
		"note":            "",
		"uri":             baseURL + "/@admin",
		"followers_count": 0,
		"following_count": 0,
		"statuses_count":  0,
		"emojis":          []any{},
		"fields":          []any{},
	}

	rules := instanceRules(r.Context(), h.queries)

	resp := map[string]any{
		"domain":      domain,
		"title":       "Gotodon",
		"version":     "4.5.3",
		"source_url":  "https://github.com/deicod/gotodon",
		"description": "A Mastodon-compatible server written in Go.",
		"usage": map[string]any{
			"users": map[string]any{
				"active_month": 0,
			},
		},
		"thumbnail": map[string]any{
			"url": thumbnailURL,
		},
		"icon":      icons,
		"languages": []string{"en"},
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
				"max_featured_tags":   10,
				"max_pinned_statuses": 5,
			},
			"statuses": map[string]any{
				"max_characters":              500,
				"max_media_attachments":       4,
				"characters_reserved_per_url": 23,
			},
			"media_attachments": map[string]any{
				"description_limit":      1500,
				"image_matrix_limit":     33177600,
				"image_size_limit":       10485760,
				"supported_mime_types":   []string{"image/jpeg", "image/png", "image/gif", "video/mp4", "video/webm"},
				"video_frame_rate_limit": 120,
				"video_matrix_limit":     8294400,
				"video_size_limit":       41943040,
			},
			"polls": map[string]any{
				"max_options":               4,
				"max_characters_per_option": 50,
				"min_expiration":            300,
				"max_expiration":            2629746,
			},
			"translation": map[string]any{
				"enabled": h.cfg.Translation.Enabled(),
			},
			"timelines_access": map[string]any{
				"live_feeds": map[string]any{
					"local":  true,
					"remote": true,
				},
				"hashtag_feeds": map[string]any{
					"local":  true,
					"remote": true,
				},
				"trending_link_feeds": map[string]any{
					"local":  true,
					"remote": true,
				},
			},
			"limited_federation": false,
		},
		"registrations": map[string]any{
			"enabled":           regEnabled,
			"approval_required": approvalRequired,
			"reason_required":   reasonRequired,
			"message":           nil,
			"min_age":           nil,
			"url":               nil,
		},
		"contact": map[string]any{
			"email":   "admin@" + domain,
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

func registrationsConfig(cfg *config.Config) (enabled, approvalRequired, reasonRequired bool) {
	mode := ""
	singleUser := false
	if cfg != nil {
		mode = strings.ToLower(strings.TrimSpace(cfg.Instance.RegistrationsMode))
		singleUser = cfg.Instance.SingleUserMode
	}
	if mode == "" {
		mode = "open"
	}
	enabled = mode != "none" && !singleUser
	approvalRequired = mode == "approved"
	reasonRequired = approvalRequired
	return enabled, approvalRequired, reasonRequired
}

func instanceRules(ctx context.Context, queries *db.Queries) []v1.RuleResponse {
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

func hasCurrentTermsOfService(ctx context.Context, queries *db.Queries) bool {
	if queries == nil {
		return false
	}
	_, err := queries.GetCurrentTermsOfService(ctx)
	return err == nil
}
