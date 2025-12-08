// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// NotificationsHandler handles notification endpoints
type NotificationsHandler struct {
	queries *db.Queries
}

// NewNotificationsHandler creates a new notifications handler
func NewNotificationsHandler(queries *db.Queries) *NotificationsHandler {
	return &NotificationsHandler{queries: queries}
}

// Notification represents a Mastodon notification
type Notification struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Account   Account   `json:"account"`
	Status    *Status   `json:"status,omitempty"`
}

// List handles GET /api/v1/notifications
func (h *NotificationsHandler) List(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// No notifications table in DB yet, return empty
	return c.JSON(http.StatusOK, []Notification{})
}

// Get handles GET /api/v1/notifications/:id
func (h *NotificationsHandler) Get(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Mock response
	now := time.Now()
	discoverable := true

	return c.JSON(http.StatusOK, Notification{
		ID:        id,
		Type:      "favourite",
		CreatedAt: now.Add(-time.Hour),
		Account: Account{
			ID:           "2",
			Username:     "other",
			Acct:         "other",
			DisplayName:  "Another User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-48 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		},
	})
}

// Clear handles POST /api/v1/notifications/clear
func (h *NotificationsHandler) Clear(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// Dismiss handles POST /api/v1/notifications/:id/dismiss
func (h *NotificationsHandler) Dismiss(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers the notifications routes
func (h *NotificationsHandler) RegisterRoutes(_ *echo.Group) {
	// All notifications routes require auth, registered separately
}
