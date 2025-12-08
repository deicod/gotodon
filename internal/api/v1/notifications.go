// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// NotificationsHandler handles notification endpoints
type NotificationsHandler struct {
	// TODO: Add database queries dependency
}

// NewNotificationsHandler creates a new notifications handler
func NewNotificationsHandler() *NotificationsHandler {
	return &NotificationsHandler{}
}

// Notification represents a Mastodon notification
type Notification struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // mention, status, reblog, follow, follow_request, favourite, poll, update, admin.sign_up, admin.report
	CreatedAt time.Time `json:"created_at"`
	Account   Account   `json:"account"`
	Status    *Status   `json:"status,omitempty"`
}

// List handles GET /api/v1/notifications
func (h *NotificationsHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Parse params
	limit := parseLimit(c.QueryParam("limit"), 40, 80)
	maxID := c.QueryParam("max_id")
	sinceID := c.QueryParam("since_id")
	minID := c.QueryParam("min_id")
	types := c.QueryParams()["types[]"]
	excludeTypes := c.QueryParams()["exclude_types[]"]

	// TODO: Fetch notifications from database
	_ = accountID
	_ = maxID
	_ = sinceID
	_ = minID
	_ = types
	_ = excludeTypes

	notifications := make([]Notification, 0, limit)

	return c.JSON(http.StatusOK, notifications)
}

// Get handles GET /api/v1/notifications/:id
func (h *NotificationsHandler) Get(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Fetch notification from database
	_ = accountID

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
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Clear all notifications for account
	_ = accountID

	return c.JSON(http.StatusOK, map[string]any{})
}

// Dismiss handles POST /api/v1/notifications/:id/dismiss
func (h *NotificationsHandler) Dismiss(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Dismiss specific notification
	_ = id
	_ = accountID

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers the notifications routes (all require auth)
func (h *NotificationsHandler) RegisterRoutes(_ *echo.Group) {
	// All notifications routes require auth, registered in main router
}
