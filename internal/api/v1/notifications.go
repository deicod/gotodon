// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"
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

func (h *NotificationsHandler) useDB() bool {
	return h.queries != nil
}

// Notification represents a Mastodon notification
type Notification struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Account   Account   `json:"account"`
	Status    *Status   `json:"status,omitempty"`
}

func (h *NotificationsHandler) dbNotificationToAPI(ctx context.Context, dbNotif db.Notification) Notification {
	createdAt := time.Now()
	if dbNotif.CreatedAt.Valid {
		createdAt = dbNotif.CreatedAt.Time
	}

	notifType := ""
	if dbNotif.Type.Valid {
		notifType = dbNotif.Type.String
	}

	notif := Notification{
		ID:        strconv.FormatInt(dbNotif.ID, 10),
		Type:      notifType,
		CreatedAt: createdAt,
	}

	// Load account
	if h.useDB() && dbNotif.FromAccountID > 0 {
		dbAcc, err := h.queries.GetAccountByID(ctx, dbNotif.FromAccountID)
		if err == nil {
			discoverable := dbAcc.Discoverable.Valid && dbAcc.Discoverable.Bool
			notif.Account = Account{
				ID:           strconv.FormatInt(dbAcc.ID, 10),
				Username:     dbAcc.Username,
				Acct:         dbAcc.Username,
				DisplayName:  dbAcc.DisplayName,
				Discoverable: &discoverable,
				Emojis:       []any{},
				Fields:       []Field{},
			}
			if dbAcc.CreatedAt.Valid {
				notif.Account.CreatedAt = dbAcc.CreatedAt.Time
			}
		}
	}

	return notif
}

// List handles GET /api/v1/notifications
func (h *NotificationsHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	limit := parseLimit(c.QueryParam("limit"), 40, 80)
	types := c.QueryParams()["types[]"]
	excludeTypes := c.QueryParams()["exclude_types[]"]

	ctx := context.Background()

	if h.useDB() {
		dbNotifs, err := h.queries.ListNotifications(ctx, db.ListNotificationsParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
			Column4:   types,
			Column5:   excludeTypes,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Notification{})
		}

		notifications := make([]Notification, len(dbNotifs))
		for i, n := range dbNotifs {
			notifications[i] = h.dbNotificationToAPI(ctx, n)
		}
		return c.JSON(http.StatusOK, notifications)
	}

	return c.JSON(http.StatusOK, []Notification{})
}

// Get handles GET /api/v1/notifications/:id
func (h *NotificationsHandler) Get(c echo.Context) error {
	idStr := c.Param("id")
	if idStr == "" {
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

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		dbNotif, err := h.queries.GetNotification(ctx, db.GetNotificationParams{
			ID:        id,
			AccountID: accountID,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		return c.JSON(http.StatusOK, h.dbNotificationToAPI(ctx, dbNotif))
	}

	// Mock fallback
	now := time.Now()
	discoverable := true
	return c.JSON(http.StatusOK, Notification{
		ID:        idStr,
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

	ctx := context.Background()

	if h.useDB() {
		_ = h.queries.ClearNotifications(ctx, accountID)
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// Dismiss handles POST /api/v1/notifications/:id/dismiss
func (h *NotificationsHandler) Dismiss(c echo.Context) error {
	idStr := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		_ = h.queries.DeleteNotification(ctx, db.DeleteNotificationParams{
			ID:        id,
			AccountID: accountID,
		})
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// RegisterRoutes registers the notifications routes
func (h *NotificationsHandler) RegisterRoutes(g *echo.Group) {
	notifications := g.Group("/notifications")
	notifications.GET("", h.List)
	notifications.GET("/:id", h.Get)
	notifications.POST("/clear", h.Clear)
	notifications.POST("/:id/dismiss", h.Dismiss)
}
