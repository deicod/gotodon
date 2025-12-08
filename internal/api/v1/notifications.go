// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
)

// NotificationsHandler handles notification endpoints
type NotificationsHandler struct {
	queries   *db.Queries
	publisher streaming.Publisher
}

// NewNotificationsHandler creates a new notifications handler
func NewNotificationsHandler(queries *db.Queries, publisher streaming.Publisher) *NotificationsHandler {
	return &NotificationsHandler{
		queries:   queries,
		publisher: publisher,
	}
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
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	limit := parseLimit(web.QueryParam(r, "limit"), 40, 80)
	types := r.URL.Query()["types[]"]
	excludeTypes := r.URL.Query()["exclude_types[]"]

	ctx := r.Context()

	if h.useDB() {
		dbNotifs, err := h.queries.ListNotifications(ctx, db.ListNotificationsParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
			Column4:   types,
			Column5:   excludeTypes,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Notification{})
			return
		}

		notifications := make([]Notification, len(dbNotifs))
		for i, n := range dbNotifs {
			notifications[i] = h.dbNotificationToAPI(ctx, n)
		}
		web.JSON(w, http.StatusOK, notifications)
		return
	}

	web.JSON(w, http.StatusOK, []Notification{})
}

// Get handles GET /api/v1/notifications/:id
func (h *NotificationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	if idStr == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbNotif, err := h.queries.GetNotification(ctx, db.GetNotificationParams{
			ID:        id,
			AccountID: accountID,
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbNotificationToAPI(ctx, dbNotif))
		return
	}

	// Mock fallback
	now := time.Now()
	discoverable := true
	web.JSON(w, http.StatusOK, Notification{
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
func (h *NotificationsHandler) Clear(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		_ = h.queries.ClearNotifications(r.Context(), accountID)
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Dismiss handles POST /api/v1/notifications/:id/dismiss
func (h *NotificationsHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_ = h.queries.DeleteNotification(r.Context(), db.DeleteNotificationParams{
			ID:        id,
			AccountID: accountID,
		})
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers the notifications routes
func (h *NotificationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/notifications", h.List)
	mux.HandleFunc("GET /api/v1/notifications/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/notifications/clear", h.Clear)
	mux.HandleFunc("POST /api/v1/notifications/{id}/dismiss", h.Dismiss)
}
