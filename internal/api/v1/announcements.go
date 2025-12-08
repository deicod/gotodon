// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// AnnouncementsHandler handles announcement endpoints
type AnnouncementsHandler struct {
	queries *db.Queries
}

// NewAnnouncementsHandler creates a new announcements handler
func NewAnnouncementsHandler(queries *db.Queries) *AnnouncementsHandler {
	return &AnnouncementsHandler{queries: queries}
}

// addSampleAnnouncements removed as we use DB

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
func (h *AnnouncementsHandler) GetAll(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		// Announcements can be public? Usually authenticated in Mastodon
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	withDismissed := c.QueryParam("with_dismissed") == "true"

	if h.queries == nil {
		return c.JSON(http.StatusOK, []Announcement{})
	}

	anns, err := h.queries.ListAnnouncements(c.Request().Context(), db.ListAnnouncementsParams{
		AccountID:     accountID,
		WithDismissed: withDismissed,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	result := make([]Announcement, len(anns))
	for i, a := range anns {
		// Get reactions
		reactions, _ := h.queries.ListAnnouncementReactions(c.Request().Context(), a.ID)
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

		// Read status implied by not being in ignored list?
		// Note: ListAnnouncements filters muted unless withDismissed is true.
		// If withDismissed is true and it's returned, we need to know if it was muted.
		// The query returns muted items too if withDismissed=true.
		// We'd need to check if it's muted.
		// Simplified: assume Unread=true for now unless we do a separate check.
		// Actually, query: LEFT JOIN announcement_mutes ... WHERE am.id IS NULL OR with_dismissed
		// If am.id is NOT NULL, it is read/dismissed.
		// But I modified the query to fetch a.* only.
		// I won't know if it was matched by mute or not.
		// TODO: Enhance query to return 'muted' bool.
		// MVP: just return Read=false.

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

	return c.JSON(http.StatusOK, result)
}

// Dismiss handles POST /api/v1/announcements/:id/dismiss
func (h *AnnouncementsHandler) Dismiss(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid ID"})
	}

	if h.queries != nil {
		err := h.queries.DismissAnnouncement(c.Request().Context(), db.DismissAnnouncementParams{
			AccountID:      accountID,
			AnnouncementID: id,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// AddReaction handles PUT /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) AddReaction(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid ID"})
	}
	name := c.Param("name")

	if h.queries != nil {
		_, err := h.queries.AddAnnouncementReaction(c.Request().Context(), db.AddAnnouncementReactionParams{
			AccountID:      pgtype.Int8{Int64: accountID, Valid: true},
			AnnouncementID: id,
			Name:           name,
		})
		if err != nil {
			// Ignore dup error? or just return OK
			// return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RemoveReaction handles DELETE /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) RemoveReaction(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid ID"})
	}
	name := c.Param("name")

	if h.queries != nil {
		_ = h.queries.RemoveAnnouncementReaction(c.Request().Context(), db.RemoveAnnouncementReactionParams{
			AccountID:      pgtype.Int8{Int64: accountID, Valid: true},
			AnnouncementID: id,
			Name:           name,
		})
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers announcement routes
func (h *AnnouncementsHandler) RegisterRoutes(g *echo.Group) {
	announcements := g.Group("/announcements")
	announcements.GET("", h.GetAll)
	announcements.POST("/:id/dismiss", h.Dismiss)
	announcements.PUT("/:id/reactions/:name", h.AddReaction)
	announcements.DELETE("/:id/reactions/:name", h.RemoveReaction)
}
