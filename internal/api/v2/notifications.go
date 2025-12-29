// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

const (
	defaultNotificationsLimit = 40
	maxNotificationsLimit     = 80
	sampleAccountsLimit       = 8
)

// NotificationsHandler handles grouped notification endpoints for v2.
type NotificationsHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewNotificationsHandler creates a new v2 notifications handler.
func NewNotificationsHandler(queries *db.Queries, cfg *config.Config) *NotificationsHandler {
	return &NotificationsHandler{queries: queries, cfg: cfg}
}

func (h *NotificationsHandler) useDB() bool {
	return h.queries != nil
}

// NotificationGroup is the grouped notification response entry.
type NotificationGroup struct {
	GroupKey                 string     `json:"group_key"`
	NotificationsCount       int64      `json:"notifications_count"`
	Type                     string     `json:"type"`
	MostRecentNotificationID string     `json:"most_recent_notification_id"`
	PageMinID                *string    `json:"page_min_id,omitempty"`
	PageMaxID                *string    `json:"page_max_id,omitempty"`
	LatestPageNotificationAt *time.Time `json:"latest_page_notification_at,omitempty"`
	SampleAccountIDs         []string   `json:"sample_account_ids"`
	StatusID                 *string    `json:"status_id,omitempty"`
}

// GroupedNotificationsResponse wraps grouped notifications with referenced accounts and statuses.
type GroupedNotificationsResponse struct {
	Accounts           []v1.Account        `json:"accounts"`
	Statuses           []v1.Status         `json:"statuses"`
	NotificationGroups []NotificationGroup `json:"notification_groups"`
}

// NotificationPolicy represents a user's notification filtering policy.
type NotificationPolicy struct {
	ForNotFollowing    string `json:"for_not_following"`
	ForNotFollowers    string `json:"for_not_followers"`
	ForNewAccounts     string `json:"for_new_accounts"`
	ForPrivateMentions string `json:"for_private_mentions"`
	ForLimitedAccounts string `json:"for_limited_accounts"`
	Summary            struct {
		PendingRequestsCount      int64 `json:"pending_requests_count"`
		PendingNotificationsCount int64 `json:"pending_notifications_count"`
	} `json:"summary"`
}

type notificationGroupData struct {
	groupKey           string
	notificationType   string
	mostRecentID       int64
	mostRecentAt       time.Time
	minID              int64
	maxID              int64
	notificationsCount int64
	sampleAccountIDs   []int64
	statusID           *int64
}

func (h *NotificationsHandler) notificationType(dbNotif db.Notification) string {
	if dbNotif.Type.Valid {
		return dbNotif.Type.String
	}
	return ""
}

func (h *NotificationsHandler) groupKeyForNotification(dbNotif db.Notification) string {
	if dbNotif.GroupKey.Valid && dbNotif.GroupKey.String != "" {
		return dbNotif.GroupKey.String
	}

	notifType := h.notificationType(dbNotif)
	if notifType == "" {
		return fmt.Sprintf("ungrouped-%d", dbNotif.ID)
	}
	if dbNotif.ActivityType != "" && dbNotif.ActivityID > 0 {
		return fmt.Sprintf("%s:%s:%d", notifType, dbNotif.ActivityType, dbNotif.ActivityID)
	}
	if dbNotif.ActivityID > 0 {
		return fmt.Sprintf("%s:%d", notifType, dbNotif.ActivityID)
	}
	if dbNotif.FromAccountID > 0 {
		return fmt.Sprintf("%s:%d", notifType, dbNotif.FromAccountID)
	}
	return fmt.Sprintf("ungrouped-%d", dbNotif.ID)
}

func defaultNotificationPolicy() NotificationPolicy {
	return NotificationPolicy{
		ForNotFollowing:    "accept",
		ForNotFollowers:    "accept",
		ForNewAccounts:     "accept",
		ForPrivateMentions: "filter",
		ForLimitedAccounts: "filter",
	}
}

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

func policyStringToInt(v string) (int32, bool) {
	switch v {
	case "accept":
		return 0, true
	case "filter":
		return 1, true
	case "drop":
		return 2, true
	default:
		return 0, false
	}
}

func parseGroupKey(groupKey string) (string, string, int64, bool) {
	parts := strings.Split(groupKey, ":")
	if len(parts) != 3 {
		return "", "", 0, false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", 0, false
	}
	activityID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", "", 0, false
	}
	return parts[0], parts[1], activityID, true
}

func parseUngroupedKey(groupKey string) (int64, bool) {
	if !strings.HasPrefix(groupKey, "ungrouped-") {
		return 0, false
	}
	idStr := strings.TrimPrefix(groupKey, "ungrouped-")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (h *NotificationsHandler) groupNotifications(notifications []db.Notification) ([]NotificationGroup, []int64, []int64) {
	groupMap := make(map[string]*notificationGroupData)
	order := make([]string, 0)

	for _, notif := range notifications {
		groupKey := h.groupKeyForNotification(notif)
		group, ok := groupMap[groupKey]
		if !ok {
			createdAt := time.Now()
			if notif.CreatedAt.Valid {
				createdAt = notif.CreatedAt.Time
			}

			group = &notificationGroupData{
				groupKey:         groupKey,
				notificationType: h.notificationType(notif),
				mostRecentID:     notif.ID,
				mostRecentAt:     createdAt,
				minID:            notif.ID,
				maxID:            notif.ID,
			}
			order = append(order, groupKey)
			groupMap[groupKey] = group
		}

		group.notificationsCount++
		if notif.ID > group.maxID {
			group.maxID = notif.ID
		}
		if group.minID == 0 || notif.ID < group.minID {
			group.minID = notif.ID
		}

		if len(group.sampleAccountIDs) < sampleAccountsLimit {
			already := false
			for _, id := range group.sampleAccountIDs {
				if id == notif.FromAccountID {
					already = true
					break
				}
			}
			if !already && notif.FromAccountID > 0 {
				group.sampleAccountIDs = append(group.sampleAccountIDs, notif.FromAccountID)
			}
		}

		if group.statusID == nil && strings.EqualFold(notif.ActivityType, "Status") && notif.ActivityID > 0 {
			activityID := notif.ActivityID
			group.statusID = &activityID
		}
	}

	orderedAccountIDs := make([]int64, 0)
	seenAccountIDs := make(map[int64]bool)
	orderedStatusIDs := make([]int64, 0)
	seenStatusIDs := make(map[int64]bool)
	groups := make([]NotificationGroup, 0, len(order))

	for _, key := range order {
		group := groupMap[key]
		if group == nil {
			continue
		}
		sampleIDs := make([]string, 0, len(group.sampleAccountIDs))
		for _, id := range group.sampleAccountIDs {
			sampleIDs = append(sampleIDs, strconv.FormatInt(id, 10))
			if !seenAccountIDs[id] {
				seenAccountIDs[id] = true
				orderedAccountIDs = append(orderedAccountIDs, id)
			}
		}

		var statusID *string
		if group.statusID != nil {
			idStr := strconv.FormatInt(*group.statusID, 10)
			statusID = &idStr
			if !seenStatusIDs[*group.statusID] {
				seenStatusIDs[*group.statusID] = true
				orderedStatusIDs = append(orderedStatusIDs, *group.statusID)
			}
		}

		minIDStr := strconv.FormatInt(group.minID, 10)
		maxIDStr := strconv.FormatInt(group.maxID, 10)
		mostRecentIDStr := strconv.FormatInt(group.mostRecentID, 10)

		createdAt := group.mostRecentAt
		groups = append(groups, NotificationGroup{
			GroupKey:                 group.groupKey,
			NotificationsCount:       group.notificationsCount,
			Type:                     group.notificationType,
			MostRecentNotificationID: mostRecentIDStr,
			PageMinID:                &minIDStr,
			PageMaxID:                &maxIDStr,
			LatestPageNotificationAt: &createdAt,
			SampleAccountIDs:         sampleIDs,
			StatusID:                 statusID,
		})
	}

	return groups, orderedAccountIDs, orderedStatusIDs
}

func (h *NotificationsHandler) buildGroupedResponse(ctx context.Context, notifications []db.Notification) (GroupedNotificationsResponse, error) {
	groups, accountIDs, statusIDs := h.groupNotifications(notifications)

	resp := GroupedNotificationsResponse{
		Accounts:           []v1.Account{},
		Statuses:           []v1.Status{},
		NotificationGroups: groups,
	}

	if !h.useDB() {
		return resp, nil
	}

	if len(accountIDs) > 0 {
		dbAccounts, err := h.queries.GetAccountsByIDs(ctx, accountIDs)
		if err != nil {
			return resp, err
		}
		accountMap := make(map[int64]v1.Account, len(dbAccounts))
		for _, acc := range dbAccounts {
			accountMap[acc.ID] = v1.SerializeAccount(h.cfg, acc)
		}
		for _, id := range accountIDs {
			if acc, ok := accountMap[id]; ok {
				resp.Accounts = append(resp.Accounts, acc)
			}
		}
	}

	if len(statusIDs) > 0 {
		dbStatuses, err := h.queries.GetStatusesByIDs(ctx, statusIDs)
		if err != nil {
			return resp, err
		}
		statusMap := make(map[int64]v1.Status, len(dbStatuses))
		for _, st := range dbStatuses {
			statusMap[st.ID] = v1.SerializeStatusSimple(ctx, h.queries, h.cfg, st, nil)
		}
		for _, id := range statusIDs {
			if status, ok := statusMap[id]; ok {
				resp.Statuses = append(resp.Statuses, status)
			}
		}
	}

	return resp, nil
}

func (h *NotificationsHandler) listNotificationsForGroup(ctx context.Context, accountID int64, groupKey string) ([]db.Notification, error) {
	if !h.useDB() {
		return []db.Notification{}, nil
	}

	if id, ok := parseUngroupedKey(groupKey); ok {
		dbNotif, err := h.queries.GetNotification(ctx, db.GetNotificationParams{
			ID:        id,
			AccountID: accountID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return []db.Notification{}, nil
			}
			return nil, err
		}
		return []db.Notification{dbNotif}, nil
	}

	notifications := make([]db.Notification, 0)
	seen := make(map[int64]bool)

	dbNotifs, err := h.queries.ListNotificationsByGroupKey(ctx, db.ListNotificationsByGroupKeyParams{
		AccountID: accountID,
		GroupKey:  pgtype.Text{String: groupKey, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	for _, notif := range dbNotifs {
		if !seen[notif.ID] {
			seen[notif.ID] = true
			notifications = append(notifications, notif)
		}
	}

	if notifType, activityType, activityID, ok := parseGroupKey(groupKey); ok {
		dbNotifs, err := h.queries.ListNotificationsByTypeAndActivity(ctx, db.ListNotificationsByTypeAndActivityParams{
			AccountID:    accountID,
			Type:         pgtype.Text{String: notifType, Valid: true},
			ActivityType: activityType,
			ActivityID:   activityID,
		})
		if err != nil {
			return nil, err
		}
		for _, notif := range dbNotifs {
			if !seen[notif.ID] {
				seen[notif.ID] = true
				notifications = append(notifications, notif)
			}
		}
	}

	sort.Slice(notifications, func(i, j int) bool {
		return notifications[i].ID > notifications[j].ID
	})

	return notifications, nil
}

// GetPolicy handles GET /api/v2/notifications/policy
func (h *NotificationsHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	policy := defaultNotificationPolicy()

	if h.useDB() {
		ctx := r.Context()
		dbPolicy, err := h.queries.GetNotificationPolicy(ctx, accountID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		if err == nil {
			policy.ForNotFollowing = policyIntToString(dbPolicy.ForNotFollowing)
			policy.ForNotFollowers = policyIntToString(dbPolicy.ForNotFollowers)
			policy.ForNewAccounts = policyIntToString(dbPolicy.ForNewAccounts)
			policy.ForPrivateMentions = policyIntToString(dbPolicy.ForPrivateMentions)
			policy.ForLimitedAccounts = policyIntToString(dbPolicy.ForLimitedAccounts)
		}

		pendingCount, err := h.queries.CountNotificationRequests(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		pendingNotifications, err := h.queries.SumNotificationRequestNotifications(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		policy.Summary.PendingRequestsCount = pendingCount
		policy.Summary.PendingNotificationsCount = pendingNotifications
	}

	web.JSON(w, http.StatusOK, policy)
}

// UpdatePolicy handles PUT /api/v2/notifications/policy
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

	policy := defaultNotificationPolicy()

	if h.useDB() {
		ctx := r.Context()
		dbPolicy, err := h.queries.GetNotificationPolicy(ctx, accountID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		if err == nil {
			policy.ForNotFollowing = policyIntToString(dbPolicy.ForNotFollowing)
			policy.ForNotFollowers = policyIntToString(dbPolicy.ForNotFollowers)
			policy.ForNewAccounts = policyIntToString(dbPolicy.ForNewAccounts)
			policy.ForPrivateMentions = policyIntToString(dbPolicy.ForPrivateMentions)
			policy.ForLimitedAccounts = policyIntToString(dbPolicy.ForLimitedAccounts)
		}
	}

	if req.ForNotFollowing != nil {
		if _, ok := policyStringToInt(*req.ForNotFollowing); !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: for_not_following is invalid")
			return
		}
		policy.ForNotFollowing = *req.ForNotFollowing
	}
	if req.ForNotFollowers != nil {
		if _, ok := policyStringToInt(*req.ForNotFollowers); !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: for_not_followers is invalid")
			return
		}
		policy.ForNotFollowers = *req.ForNotFollowers
	}
	if req.ForNewAccounts != nil {
		if _, ok := policyStringToInt(*req.ForNewAccounts); !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: for_new_accounts is invalid")
			return
		}
		policy.ForNewAccounts = *req.ForNewAccounts
	}
	if req.ForPrivateMentions != nil {
		if _, ok := policyStringToInt(*req.ForPrivateMentions); !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: for_private_mentions is invalid")
			return
		}
		policy.ForPrivateMentions = *req.ForPrivateMentions
	}
	if req.ForLimitedAccounts != nil {
		if _, ok := policyStringToInt(*req.ForLimitedAccounts); !ok {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: for_limited_accounts is invalid")
			return
		}
		policy.ForLimitedAccounts = *req.ForLimitedAccounts
	}

	if h.useDB() {
		ctx := r.Context()
		forNotFollowing, _ := policyStringToInt(policy.ForNotFollowing)
		forNotFollowers, _ := policyStringToInt(policy.ForNotFollowers)
		forNewAccounts, _ := policyStringToInt(policy.ForNewAccounts)
		forPrivateMentions, _ := policyStringToInt(policy.ForPrivateMentions)
		forLimitedAccounts, _ := policyStringToInt(policy.ForLimitedAccounts)

		if _, err := h.queries.UpsertNotificationPolicy(ctx, db.UpsertNotificationPolicyParams{
			AccountID:          accountID,
			ForNotFollowing:    forNotFollowing,
			ForNotFollowers:    forNotFollowers,
			ForNewAccounts:     forNewAccounts,
			ForPrivateMentions: forPrivateMentions,
			ForLimitedAccounts: forLimitedAccounts,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		pendingCount, err := h.queries.CountNotificationRequests(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		pendingNotifications, err := h.queries.SumNotificationRequestNotifications(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		policy.Summary.PendingRequestsCount = pendingCount
		policy.Summary.PendingNotificationsCount = pendingNotifications
	}

	web.JSON(w, http.StatusOK, policy)
}

// List handles GET /api/v2/notifications
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	limit := parseLimit(web.QueryParam(r, "limit"), defaultNotificationsLimit, maxNotificationsLimit)
	types := r.URL.Query()["types[]"]
	excludeTypes := r.URL.Query()["exclude_types[]"]
	if len(types) == 0 {
		types = nil
	}
	if len(excludeTypes) == 0 {
		excludeTypes = nil
	}

	if !h.useDB() {
		web.JSON(w, http.StatusOK, GroupedNotificationsResponse{
			Accounts:           []v1.Account{},
			Statuses:           []v1.Status{},
			NotificationGroups: []NotificationGroup{},
		})
		return
	}

	dbNotifs, err := h.queries.ListNotifications(r.Context(), db.ListNotificationsParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    0,
		Column4:   types,
		Column5:   excludeTypes,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	resp, err := h.buildGroupedResponse(r.Context(), dbNotifs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, resp)
}

// Get handles GET /api/v2/notifications/{group_key}
func (h *NotificationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	groupKey := web.Param(r, "group_key")
	if groupKey == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if !h.useDB() {
		resp := GroupedNotificationsResponse{
			Accounts:           []v1.Account{},
			Statuses:           []v1.Status{},
			NotificationGroups: []NotificationGroup{},
		}
		resp.NotificationGroups = append(resp.NotificationGroups, NotificationGroup{
			GroupKey:                 groupKey,
			NotificationsCount:       1,
			Type:                     "mention",
			MostRecentNotificationID: "1",
			PageMinID:                stringPtr("1"),
			PageMaxID:                stringPtr("1"),
			SampleAccountIDs:         []string{"2"},
		})
		web.JSON(w, http.StatusOK, resp)
		return
	}

	notifications, err := h.listNotificationsForGroup(r.Context(), accountID, groupKey)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if len(notifications) == 0 {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	resp, err := h.buildGroupedResponse(r.Context(), notifications)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, resp)
}

// Dismiss handles POST /api/v2/notifications/{group_key}/dismiss
func (h *NotificationsHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	groupKey := web.Param(r, "group_key")
	if groupKey == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		if id, ok := parseUngroupedKey(groupKey); ok {
			if err := h.queries.DeleteNotification(r.Context(), db.DeleteNotificationParams{
				ID:        id,
				AccountID: accountID,
			}); err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
		} else {
			if err := h.queries.DeleteNotificationsByGroupKey(r.Context(), db.DeleteNotificationsByGroupKeyParams{
				AccountID: accountID,
				GroupKey:  pgtype.Text{String: groupKey, Valid: true},
			}); err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			if notifType, activityType, activityID, ok := parseGroupKey(groupKey); ok {
				if err := h.queries.DeleteNotificationsByTypeAndActivity(r.Context(), db.DeleteNotificationsByTypeAndActivityParams{
					AccountID:    accountID,
					Type:         pgtype.Text{String: notifType, Valid: true},
					ActivityType: activityType,
					ActivityID:   activityID,
				}); err != nil {
					web.JSONError(w, http.StatusInternalServerError, "Database error")
					return
				}
			}
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Accounts handles GET /api/v2/notifications/{group_key}/accounts
func (h *NotificationsHandler) Accounts(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	groupKey := web.Param(r, "group_key")
	if groupKey == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseLimit(web.QueryParam(r, "limit"), defaultNotificationsLimit, maxNotificationsLimit)

	if !h.useDB() {
		web.JSON(w, http.StatusOK, []v1.Account{})
		return
	}

	notifications, err := h.listNotificationsForGroup(r.Context(), accountID, groupKey)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	accountIDs := make([]int64, 0, len(notifications))
	seen := make(map[int64]bool)
	for _, notif := range notifications {
		if notif.FromAccountID == 0 || seen[notif.FromAccountID] {
			continue
		}
		seen[notif.FromAccountID] = true
		accountIDs = append(accountIDs, notif.FromAccountID)
		if len(accountIDs) >= limit {
			break
		}
	}

	if len(accountIDs) == 0 {
		web.JSON(w, http.StatusOK, []v1.Account{})
		return
	}

	dbAccounts, err := h.queries.GetAccountsByIDs(r.Context(), accountIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	accountMap := make(map[int64]v1.Account, len(dbAccounts))
	for _, acc := range dbAccounts {
		accountMap[acc.ID] = v1.SerializeAccount(h.cfg, acc)
	}

	accounts := make([]v1.Account, 0, len(accountIDs))
	for _, id := range accountIDs {
		if acc, ok := accountMap[id]; ok {
			accounts = append(accounts, acc)
		}
	}

	web.JSON(w, http.StatusOK, accounts)
}

// UnreadCount handles GET /api/v2/notifications/unread_count
func (h *NotificationsHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if !h.useDB() {
		web.JSON(w, http.StatusOK, map[string]int64{"count": 0})
		return
	}

	count, err := h.queries.CountUnreadNotifications(r.Context(), accountID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, map[string]int64{"count": count})
}

func parseLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

func stringPtr(s string) *string {
	return &s
}

// RegisterRoutes registers v2 notification routes.
func (h *NotificationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/notifications", h.List)
	mux.HandleFunc("GET /api/v2/notifications/unread_count", h.UnreadCount)
	mux.HandleFunc("GET /api/v2/notifications/policy", h.GetPolicy)
	mux.HandleFunc("PUT /api/v2/notifications/policy", h.UpdatePolicy)
	mux.HandleFunc("GET /api/v2/notifications/{group_key}", h.Get)
	mux.HandleFunc("POST /api/v2/notifications/{group_key}/dismiss", h.Dismiss)
	mux.HandleFunc("GET /api/v2/notifications/{group_key}/accounts", h.Accounts)
}
