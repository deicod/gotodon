// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// AnnouncementsHandler handles announcement endpoints
type AnnouncementsHandler struct {
	idGen *snowflake.Generator
	// In-memory storage
	announcements map[int64]*Announcement
	reactions     map[int64]map[int64][]string // announcement_id -> account_id -> emoji names
	dismissed     map[int64]map[int64]bool     // announcement_id -> account_id -> dismissed
}

// NewAnnouncementsHandler creates a new announcements handler
func NewAnnouncementsHandler() *AnnouncementsHandler {
	h := &AnnouncementsHandler{
		idGen:         snowflake.NewGenerator(),
		announcements: make(map[int64]*Announcement),
		reactions:     make(map[int64]map[int64][]string),
		dismissed:     make(map[int64]map[int64]bool),
	}

	// Add some sample announcements
	h.addSampleAnnouncements()

	return h
}

func (h *AnnouncementsHandler) addSampleAnnouncements() {
	now := time.Now()

	// Welcome announcement
	id1 := h.idGen.Generate()
	h.announcements[id1] = &Announcement{
		ID:          strconv.FormatInt(id1, 10),
		Content:     "<p>Welcome to Gotodon! This is a Mastodon-compatible server built in Go.</p>",
		StartsAt:    nil,
		EndsAt:      nil,
		AllDay:      false,
		PublishedAt: now.Add(-24 * time.Hour),
		UpdatedAt:   now.Add(-24 * time.Hour),
		Published:   true,
		Read:        false,
		Reactions:   []AnnouncementReaction{},
		Mentions:    []Mention{},
		Statuses:    []AnnouncementStatus{},
		Tags:        []Tag{},
		Emojis:      []any{},
	}
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
func (h *AnnouncementsHandler) GetAll(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)

	withDismissed := c.QueryParam("with_dismissed") == "true"

	result := make([]Announcement, 0)

	for announcementID, announcement := range h.announcements {
		if !announcement.Published {
			continue
		}

		// Check if dismissed
		if ok && !withDismissed {
			if h.dismissed[announcementID] != nil && h.dismissed[announcementID][accountID] {
				continue
			}
		}

		// Build with user-specific read/reaction state
		ann := *announcement
		if ok {
			ann.Read = h.dismissed[announcementID] != nil && h.dismissed[announcementID][accountID]
			ann.Reactions = h.buildReactionsForUser(announcementID, accountID)
		}

		result = append(result, ann)
	}

	return c.JSON(http.StatusOK, result)
}

// Dismiss handles POST /api/v1/announcements/:id/dismiss
func (h *AnnouncementsHandler) Dismiss(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if _, exists := h.announcements[announcementID]; !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if h.dismissed[announcementID] == nil {
		h.dismissed[announcementID] = make(map[int64]bool)
	}
	h.dismissed[announcementID][accountID] = true

	return c.JSON(http.StatusOK, map[string]any{})
}

// AddReaction handles PUT /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) AddReaction(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if _, exists := h.announcements[announcementID]; !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Name can't be blank",
		})
	}

	// Add reaction
	if h.reactions[announcementID] == nil {
		h.reactions[announcementID] = make(map[int64][]string)
	}

	// Check if already reacted with this emoji
	for _, r := range h.reactions[announcementID][accountID] {
		if r == name {
			return c.JSON(http.StatusOK, map[string]any{})
		}
	}

	h.reactions[announcementID][accountID] = append(h.reactions[announcementID][accountID], name)

	return c.JSON(http.StatusOK, map[string]any{})
}

// RemoveReaction handles DELETE /api/v1/announcements/:id/reactions/:name
func (h *AnnouncementsHandler) RemoveReaction(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if _, exists := h.announcements[announcementID]; !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	name := c.Param("name")

	if h.reactions[announcementID] != nil {
		newReactions := make([]string, 0)
		for _, r := range h.reactions[announcementID][accountID] {
			if r != name {
				newReactions = append(newReactions, r)
			}
		}
		h.reactions[announcementID][accountID] = newReactions
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

func (h *AnnouncementsHandler) buildReactionsForUser(announcementID, accountID int64) []AnnouncementReaction {
	// Aggregate reactions
	reactionCounts := make(map[string]int)
	userReacted := make(map[string]bool)

	if h.reactions[announcementID] != nil {
		for uid, reactions := range h.reactions[announcementID] {
			for _, name := range reactions {
				reactionCounts[name]++
				if uid == accountID {
					userReacted[name] = true
				}
			}
		}
	}

	result := make([]AnnouncementReaction, 0, len(reactionCounts))
	for name, count := range reactionCounts {
		result = append(result, AnnouncementReaction{
			Name:  name,
			Count: count,
			Me:    userReacted[name],
		})
	}

	return result
}

// RegisterRoutes registers announcement routes
func (h *AnnouncementsHandler) RegisterRoutes(g *echo.Group) {
	announcements := g.Group("/announcements")
	announcements.GET("", h.GetAll)
	announcements.POST("/:id/dismiss", h.Dismiss)
	announcements.PUT("/:id/reactions/:name", h.AddReaction)
	announcements.DELETE("/:id/reactions/:name", h.RemoveReaction)
}
