package v2

import (
	"net/http"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/web"
)

type InstanceHandler struct {
	cfg *config.Config
}

func NewInstanceHandler(cfg *config.Config) *InstanceHandler {
	return &InstanceHandler{cfg: cfg}
}

// Get handles GET /api/v2/instance
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"domain":      h.cfg.Instance.LocalDomain,
		"title":       "Gotodon",
		"version":     "4.x.x (Gotodon)",
		"source_url":  "https://github.com/deicod/gotodon",
		"description": "A Mastodon-compatible server written in Go.",
		"usage": map[string]any{
			"users": map[string]any{
				"active_month": 0,
			},
		},
		"thumbnail": map[string]any{
			"url": "",
		},
		"languages": []string{"en"},
		"configuration": map[string]any{
			"urls": map[string]any{
				"streaming": "wss://" + h.cfg.Instance.LocalDomain + "/api/v1/streaming",
			},
			"accounts": map[string]any{
				"max_featured_tags": 10,
			},
			"statuses": map[string]any{
				"max_characters":              500,
				"max_media_attachments":       4,
				"characters_reserved_per_url": 23,
			},
			"media_attachments": map[string]any{
				"supported_mime_types": []string{"image/jpeg", "image/png", "image/gif", "video/mp4", "video/webm"},
				"image_size_limit":     10485760,
				"video_size_limit":     41943040,
			},
			"polls": map[string]any{
				"max_options":               4,
				"max_characters_per_option": 50,
				"min_expiration":            300,
				"max_expiration":            2629746,
			},
			"translation": map[string]any{
				"enabled": false,
			},
		},
		"registrations": map[string]any{
			"enabled":           true,
			"approval_required": false,
			"message":           nil,
		},
		"contact": map[string]any{
			"email": "admin@" + h.cfg.Instance.LocalDomain,
			"account": map[string]any{
				"id":           "1", // Admin
				"username":     "admin",
				"display_name": "Admin",
				"url":          "https://" + h.cfg.Instance.LocalDomain + "/@admin",
				"acct":         "admin",
			},
		},
		"rules": []map[string]any{
			{
				"id":   "1",
				"text": "Be nice.",
			},
		},
	}

	web.JSON(w, http.StatusOK, resp)
}

func (h *InstanceHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/instance", h.Get)
}
