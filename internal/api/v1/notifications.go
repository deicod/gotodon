// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
)

// NotificationsHandler handles notification endpoints
type NotificationsHandler struct {
	queries   *db.Queries
	publisher streaming.Publisher
	cfg       *config.Config
	redis     *redis.Client
}

// NewNotificationsHandler creates a new notifications handler
func NewNotificationsHandler(queries *db.Queries, publisher streaming.Publisher, cfg *config.Config, redisClient *redis.Client) *NotificationsHandler {
	return &NotificationsHandler{
		queries:   queries,
		publisher: publisher,
		cfg:       cfg,
		redis:     redisClient,
	}
}

func (h *NotificationsHandler) useDB() bool {
	return h.queries != nil
}

func (h *NotificationsHandler) accountWithStats(ctx context.Context, dbAcc db.Account) Account {
	acc := SerializeAccount(h.cfg, dbAcc)
	if h.useDB() {
		stats, err := h.queries.GetAccountStats(ctx, dbAcc.ID)
		if err == nil {
			acc.FollowersCount = stats.FollowersCount
			acc.FollowingCount = stats.FollowingCount
			acc.StatusesCount = stats.StatusesCount
			if stats.LastStatusAt.Valid {
				lastStatus := stats.LastStatusAt.Time.Format("2006-01-02")
				acc.LastStatusAt = &lastStatus
			}
		}
	}
	return acc
}

func (h *NotificationsHandler) notificationRequestFromDB(ctx context.Context, dbReq db.NotificationRequest) NotificationRequest {
	req := NotificationRequest{
		ID:                 strconv.FormatInt(dbReq.ID, 10),
		NotificationsCount: dbReq.NotificationsCount,
	}
	if dbReq.CreatedAt.Valid {
		req.CreatedAt = dbReq.CreatedAt.Time
	}
	if dbReq.UpdatedAt.Valid {
		req.UpdatedAt = dbReq.UpdatedAt.Time
	}
	if dbReq.FromAccountID > 0 {
		dbAcc, err := h.queries.GetAccountByID(ctx, dbReq.FromAccountID)
		if err == nil {
			req.Account = h.accountWithStats(ctx, dbAcc)
		}
	}
	if dbReq.LastStatusID.Valid && h.cfg != nil {
		dbStatus, err := h.queries.GetStatusByID(ctx, dbReq.LastStatusID.Int64)
		if err == nil {
			status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
			req.LastStatus = &status
		}
	}
	return req
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
				Emojis:       []CustomEmoji{},
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
			Emojis:       []CustomEmoji{},
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

// NotificationPolicy represents a user's notification filtering policy
type NotificationPolicy struct {
	ForNotFollowing    string `json:"for_not_following"`
	ForNotFollowers    string `json:"for_not_followers"`
	ForNewAccounts     string `json:"for_new_accounts"`
	ForPrivateMentions string `json:"for_private_mentions"`
	ForLimitedAccounts string `json:"for_limited_accounts"`
	Summary            struct {
		PendingRequestsCount int64 `json:"pending_requests_count"`
		PendingNotifications int64 `json:"pending_notifications_count"`
	} `json:"summary"`
}

// NotificationRequest represents a pending notification permission request
type NotificationRequest struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Account            Account   `json:"account"`
	NotificationsCount int64     `json:"notifications_count"`
	LastStatus         *Status   `json:"last_status,omitempty"`
}

// policyIntToString converts database int32 enum to API string
// 0=accept, 1=filter, 2=drop
func policyIntToString(v int32) string {
	switch v {
	case 0:
		return "accept"
	case 1:
		return "filter"
	case 2:
		return "drop"
	default:
		return "accept"
	}
}

// policyStringToInt converts API string to database int32 enum
func policyStringToInt(s string) int32 {
	switch s {
	case "accept":
		return 0
	case "filter":
		return 1
	case "drop":
		return 2
	default:
		return 0
	}
}

// UnreadCount handles GET /api/v1/notifications/unread_count
func (h *NotificationsHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	count := int64(0)
	if h.useDB() {
		c, err := h.queries.CountUnreadNotifications(r.Context(), accountID)
		if err == nil {
			count = c
		}
	}

	web.JSON(w, http.StatusOK, map[string]int64{"count": count})
}

// GetPolicy handles GET /api/v1/notifications/policy
func (h *NotificationsHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	policy := NotificationPolicy{
		ForNotFollowing:    "accept",
		ForNotFollowers:    "accept",
		ForNewAccounts:     "accept",
		ForPrivateMentions: "accept",
		ForLimitedAccounts: "filter",
	}

	if h.useDB() {
		ctx := r.Context()
		dbPolicy, err := h.queries.GetNotificationPolicy(ctx, accountID)
		if err == nil {
			policy.ForNotFollowing = policyIntToString(dbPolicy.ForNotFollowing)
			policy.ForNotFollowers = policyIntToString(dbPolicy.ForNotFollowers)
			policy.ForNewAccounts = policyIntToString(dbPolicy.ForNewAccounts)
			policy.ForPrivateMentions = policyIntToString(dbPolicy.ForPrivateMentions)
			policy.ForLimitedAccounts = policyIntToString(dbPolicy.ForLimitedAccounts)
		}

		// Get pending request count
		pendingCount, err := h.queries.CountNotificationRequests(ctx, accountID)
		if err == nil {
			policy.Summary.PendingRequestsCount = pendingCount
		}
	}

	web.JSON(w, http.StatusOK, policy)
}

// UpdatePolicy handles PUT /api/v1/notifications/policy
func (h *NotificationsHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req struct {
		ForNotFollowing    *string `json:"for_not_following" form:"for_not_following"`
		ForNotFollowers    *string `json:"for_not_followers" form:"for_not_followers"`
		ForNewAccounts     *string `json:"for_new_accounts" form:"for_new_accounts"`
		ForPrivateMentions *string `json:"for_private_mentions" form:"for_private_mentions"`
		ForLimitedAccounts *string `json:"for_limited_accounts" form:"for_limited_accounts"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	policy := NotificationPolicy{
		ForNotFollowing:    "accept",
		ForNotFollowers:    "accept",
		ForNewAccounts:     "accept",
		ForPrivateMentions: "accept",
		ForLimitedAccounts: "filter",
	}

	if req.ForNotFollowing != nil {
		policy.ForNotFollowing = *req.ForNotFollowing
	}
	if req.ForNotFollowers != nil {
		policy.ForNotFollowers = *req.ForNotFollowers
	}
	if req.ForNewAccounts != nil {
		policy.ForNewAccounts = *req.ForNewAccounts
	}
	if req.ForPrivateMentions != nil {
		policy.ForPrivateMentions = *req.ForPrivateMentions
	}
	if req.ForLimitedAccounts != nil {
		policy.ForLimitedAccounts = *req.ForLimitedAccounts
	}

	if h.useDB() {
		ctx := r.Context()
		_, _ = h.queries.UpsertNotificationPolicy(ctx, db.UpsertNotificationPolicyParams{
			AccountID:          accountID,
			ForNotFollowing:    policyStringToInt(policy.ForNotFollowing),
			ForNotFollowers:    policyStringToInt(policy.ForNotFollowers),
			ForNewAccounts:     policyStringToInt(policy.ForNewAccounts),
			ForPrivateMentions: policyStringToInt(policy.ForPrivateMentions),
			ForLimitedAccounts: policyStringToInt(policy.ForLimitedAccounts),
		})
	}

	web.JSON(w, http.StatusOK, policy)
}

// ListRequests handles GET /api/v1/notifications/requests
func (h *NotificationsHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	limit := int32(40)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 32); err == nil && parsed > 0 && parsed <= 80 {
			limit = int32(parsed)
		}
	}

	if h.useDB() {
		ctx := r.Context()
		dbRequests, err := h.queries.ListNotificationRequests(ctx, db.ListNotificationRequestsParams{
			AccountID: accountID,
			Limit:     limit,
			Offset:    offset,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []NotificationRequest{})
			return
		}

		requests := make([]NotificationRequest, len(dbRequests))
		for i, req := range dbRequests {
			requests[i] = h.notificationRequestFromDB(ctx, req)
		}
		web.JSON(w, http.StatusOK, requests)
		return
	}

	web.JSON(w, http.StatusOK, []NotificationRequest{})
}

// GetRequest handles GET /api/v1/notifications/requests/:id
func (h *NotificationsHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	idStr := web.Param(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		ctx := r.Context()
		dbReq, err := h.queries.GetNotificationRequest(ctx, id)
		if err != nil || dbReq.AccountID != accountID {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.notificationRequestFromDB(ctx, dbReq))
		return
	}

	now := time.Now()
	discoverable := true
	web.JSON(w, http.StatusOK, NotificationRequest{
		ID:                 idStr,
		CreatedAt:          now.Add(-time.Hour),
		UpdatedAt:          now,
		NotificationsCount: 1,
		Account: Account{
			ID:           "2",
			Username:     "other",
			Acct:         "other",
			DisplayName:  "Another User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-48 * time.Hour),
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		},
	})
}

type notificationRequestBulkPayload struct {
	IDs flexibleStringSlice `json:"id"`
}

func parseNotificationRequestIDs(r *http.Request) ([]int64, error) {
	var req notificationRequestBulkPayload
	if err := web.Bind(r, &req); err != nil {
		if errors.Is(err, io.EOF) {
			return []int64{}, nil
		}
		return nil, err
	}
	if len(req.IDs) == 0 {
		return []int64{}, nil
	}
	ids := make([]int64, 0, len(req.IDs))
	seen := make(map[int64]struct{}, len(req.IDs))
	for _, raw := range req.IDs {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

// AcceptRequest handles POST /api/v1/notifications/requests/:id/accept
func (h *NotificationsHandler) AcceptRequest(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	idStr := web.Param(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_ = h.queries.AcceptNotificationRequest(r.Context(), db.AcceptNotificationRequestParams{
			ID:        id,
			AccountID: accountID,
		})
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// DismissRequest handles POST /api/v1/notifications/requests/:id/dismiss
func (h *NotificationsHandler) DismissRequest(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	idStr := web.Param(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_ = h.queries.DismissNotificationRequest(r.Context(), db.DismissNotificationRequestParams{
			ID:        id,
			AccountID: accountID,
		})
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// AcceptRequests handles POST /api/v1/notifications/requests/accept
func (h *NotificationsHandler) AcceptRequests(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ids, err := parseNotificationRequestIDs(r)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(ids) > 0 && h.useDB() {
		if err := h.queries.BulkAcceptNotificationRequests(r.Context(), db.BulkAcceptNotificationRequestsParams{
			AccountID: accountID,
			Ids:       ids,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// DismissRequests handles POST /api/v1/notifications/requests/dismiss
func (h *NotificationsHandler) DismissRequests(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ids, err := parseNotificationRequestIDs(r)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(ids) > 0 && h.useDB() {
		if err := h.queries.BulkDismissNotificationRequests(r.Context(), db.BulkDismissNotificationRequestsParams{
			AccountID: accountID,
			Ids:       ids,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RequestsMerged handles GET /api/v1/notifications/requests/merged
func (h *NotificationsHandler) RequestsMerged(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	merged := true
	if h.redis != nil {
		key := "notification_unfilter_jobs:" + strconv.FormatInt(accountID, 10)
		value, err := h.redis.Get(r.Context(), key).Result()
		if err != nil && err != redis.Nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to check merge status")
			return
		}
		if err == nil {
			count, _ := strconv.Atoi(strings.TrimSpace(value))
			merged = count <= 0
		}
	}

	web.JSON(w, http.StatusOK, map[string]bool{"merged": merged})
}

// RegisterRoutes registers the notifications routes
func (h *NotificationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/notifications", h.List)
	mux.HandleFunc("GET /api/v1/notifications/unread_count", h.UnreadCount)
	mux.HandleFunc("GET /api/v1/notifications/policy", h.GetPolicy)
	mux.HandleFunc("PUT /api/v1/notifications/policy", h.UpdatePolicy)
	mux.HandleFunc("GET /api/v1/notifications/requests", h.ListRequests)
	mux.HandleFunc("GET /api/v1/notifications/requests/{id}", h.GetRequest)
	mux.HandleFunc("POST /api/v1/notifications/requests/{id}/accept", h.AcceptRequest)
	mux.HandleFunc("POST /api/v1/notifications/requests/{id}/dismiss", h.DismissRequest)
	mux.HandleFunc("POST /api/v1/notifications/requests/accept", h.AcceptRequests)
	mux.HandleFunc("POST /api/v1/notifications/requests/dismiss", h.DismissRequests)
	mux.HandleFunc("GET /api/v1/notifications/requests/merged", h.RequestsMerged)
	mux.HandleFunc("GET /api/v1/notifications/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/notifications/clear", h.Clear)
	mux.HandleFunc("POST /api/v1/notifications/{id}/dismiss", h.Dismiss)
}
