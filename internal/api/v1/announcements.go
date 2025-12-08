// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// AnnouncementsHandler handles announcement endpoints
type AnnouncementsHandler struct {
	queries *db.Queries
}

// NewAnnouncementsHandler creates a new announcements handler
func NewAnnouncementsHandler(queries *db.Queries) *AnnouncementsHandler {
	return &AnnouncementsHandler{queries: queries}
}

// Announcement represents a server announcement
type Announcement struct {
	ID          string                 `json:"id"`
	Content     string                 `json:"content"`
	StartsAt    *time.Time             `json:"starts_at"`
	EndsAt      *time.Time             `json:"ends_at"`
	AllDay      bool                   `json:"all_day"`
	PublishedAt time.Time              `json:"published_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Published   bool                   `json:"published"`
	Read        bool                   `json:"read"`
	Reactions   []AnnouncementReaction `json:"reactions"`
	Mentions    []Mention              `json:"mentions"`
	Statuses    []AnnouncementStatus   `json:"statuses"`
	Tags        []Tag                  `json:"tags"`
	Emojis      []any                  `json:"emojis"`
}

// AnnouncementReaction represents a reaction to an announcement
type AnnouncementReaction struct {
	Name      string `json:"name"`
	Count     int    `json:"count"`
	Me        bool   `json:"me"`
	URL       string `json:"url,omitempty"`
	StaticURL string `json:"static_url,omitempty"`
}

// AnnouncementStatus represents a status referenced in an announcement
type AnnouncementStatus struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// GetAll handles GET /api/v1/announcements
func (h *AnnouncementsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		// Announcements can be public? Usually authenticated in Mastodon
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	withDismissed := web.QueryParam(r, "with_dismissed") == "true"

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []Announcement{})
		return
	}

	anns, err := h.queries.ListAnnouncements(r.Context(), db.ListAnnouncementsParams{
		AccountID:     accountID,
		WithDismissed: withDismissed,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make([]Announcement, len(anns))
	for i, a := range anns {
		// Get reactions
		reactions, _ := h.queries.ListAnnouncementReactions(r.Context(), a.ID)
		reactionMap := make(map[string]int)
		reacted := make(map[string]bool)
		for _, r := range reactions {
			reactionMap[r.Name]++
			if r.AccountID.Int64 == accountID {
				reacted[r.Name] = true
			}
		}

		apiReactions := []AnnouncementReaction{}
		for name, count := range reactionMap {
			apiReactions = append(apiReactions, AnnouncementReaction{
				Name:  name,
				Count: count,
				Me:    reacted[name],
			})
		}

		var startsAt *time.Time
		if a.StartsAt.Valid {
			t := a.StartsAt.Time
			startsAt = &t
		}
		var endsAt *time.Time
		if a.EndsAt.Valid {
			t := a.EndsAt.Time
			endsAt = &t
		}

		result[i] = Announcement{
			ID:          strconv.FormatInt(a.ID, 10),
			Content:     a.Text,
			Published:   a.Published,
			AllDay:      a.AllDay,
			PublishedAt: a.CreatedAt.Time,
			UpdatedAt:   a.UpdatedAt.Time,
			StartsAt:    startsAt,
			EndsAt:      endsAt,
			Reactions:   apiReactions,
			Statuses:    []AnnouncementStatus{},
			Tags:        []Tag{},
			Emojis:      []any{},
			Mentions:    []Mention{},
		}
	}

	web.JSON(w, http.StatusOK, result)
}

// Dismiss handles POST /api/v1/announcements/:id/dismiss
func (h *AnnouncementsHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}

	if h.queries != nil {
		err := h.queries.DismissAnnouncement(r.Context(), db.DismissAnnouncementParams{
			AccountID:      accountID,
			AnnouncementID: id,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// AddReaction handles PUT /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) AddReaction(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}
	name := web.Param(r, "name")

	if h.queries != nil {
		_, err := h.queries.AddAnnouncementReaction(r.Context(), db.AddAnnouncementReactionParams{
			AccountID:      pgtype.Int8{Int64: accountID, Valid: true},
			AnnouncementID: id,
			Name:           name,
		})
		if err != nil {
			// Ignore dup error? or just return OK
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RemoveReaction handles DELETE /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) RemoveReaction(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}
	name := web.Param(r, "name")

	if h.queries != nil {
		_ = h.queries.RemoveAnnouncementReaction(r.Context(), db.RemoveAnnouncementReactionParams{
			AccountID:      pgtype.Int8{Int64: accountID, Valid: true},
			AnnouncementID: id,
			Name:           name,
		})
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers announcement routes
func (h *AnnouncementsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/announcements", h.GetAll)
	mux.HandleFunc("POST /api/v1/announcements/{id}/dismiss", h.Dismiss)
	mux.HandleFunc("PUT /api/v1/announcements/{id}/reactions/{name}", h.AddReaction)
	mux.HandleFunc("DELETE /api/v1/announcements/{id}/reactions/{name}", h.RemoveReaction)
}
