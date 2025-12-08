// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// DirectoryHandler handles the profile directory endpoint
type DirectoryHandler struct {
	queries *db.Queries
}

// NewDirectoryHandler creates a new directory handler
func NewDirectoryHandler(queries *db.Queries) *DirectoryHandler {
	return &DirectoryHandler{queries: queries}
}

func (h *DirectoryHandler) useDB() bool {
	return h.queries != nil
}

// Get handles GET /api/v1/directory
func (h *DirectoryHandler) Get(w http.ResponseWriter, r *http.Request) {
	limit := parseDirectoryLimit(web.QueryParam(r, "limit"), 40, 80)
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	local := web.QueryParam(r, "local") == "true"
	order := web.QueryParam(r, "order") // "active" or "new"

	if h.useDB() {
		orderActive := order == "active"
		orderNew := order == "new" || order == "" // Default to new

		accounts, err := h.queries.ListDeepDirectory(r.Context(), db.ListDeepDirectoryParams{
			Limit:       int32(limit),
			Offset:      int32(offset),
			Local:       local,
			OrderActive: orderActive,
			OrderNew:    orderNew,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		result := make([]Account, len(accounts))
		for i, acc := range accounts {
			discoverable := acc.Discoverable.Valid && acc.Discoverable.Bool
			result[i] = Account{
				ID:           strconv.FormatInt(acc.ID, 10),
				Username:     acc.Username,
				Acct:         acc.Username,
				DisplayName:  acc.DisplayName,
				Note:         acc.Note,
				Locked:       acc.Locked,
				Bot:          false,
				Discoverable: &discoverable,
				Emojis:       []CustomEmoji{},
				Fields:       []Field{},
			}
			if acc.CreatedAt.Valid {
				result[i].CreatedAt = acc.CreatedAt.Time
			}
			if acc.AvatarRemoteUrl.Valid {
				result[i].Avatar = acc.AvatarRemoteUrl.String
			}
			if acc.HeaderRemoteUrl != "" {
				result[i].Header = acc.HeaderRemoteUrl
			}
		}
		web.JSON(w, http.StatusOK, result)
		return
	}

	// Mock response
	now := time.Now()
	discoverable := true
	web.JSON(w, http.StatusOK, []Account{
		{
			ID:           "1",
			Username:     "admin",
			Acct:         "admin",
			DisplayName:  "Administrator",
			Note:         "<p>Server administrator</p>",
			Locked:       false,
			Bot:          false,
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-365 * 24 * time.Hour),
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		},
	})
}

func parseDirectoryLimit(s string, defaultVal, maxVal int) int {
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

// RegisterRoutes registers directory routes
func (h *DirectoryHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/directory", h.Get)
}
