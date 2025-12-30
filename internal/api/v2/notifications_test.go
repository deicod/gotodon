package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

func mockNotificationRequest(method, path, body string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestNotificationsHandler_List_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodGet, "/api/v2/notifications", "", 12345)
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp GroupedNotificationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if resp.NotificationGroups == nil {
		t.Error("expected notification_groups field in response")
	}

	req, rec = mockNotificationRequest(http.MethodGet, "/api/v2/notifications", "", 0)
	h.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNotificationsHandler_UnreadCount_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodGet, "/api/v2/notifications/unread_count", "", 12345)
	h.UnreadCount(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]int64
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if _, ok := result["count"]; !ok {
		t.Error("expected 'count' field in response")
	}

	req, rec = mockNotificationRequest(http.MethodGet, "/api/v2/notifications/unread_count", "", 0)
	h.UnreadCount(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNotificationsHandler_GetPolicy_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodGet, "/api/v2/notifications/policy", "", 12345)
	h.GetPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var policy NotificationPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if policy.ForNotFollowing != "accept" {
		t.Errorf("for_not_following = %q, want 'accept'", policy.ForNotFollowing)
	}
	if policy.ForPrivateMentions != "filter" {
		t.Errorf("for_private_mentions = %q, want 'filter'", policy.ForPrivateMentions)
	}
	if policy.Summary.PendingRequestsCount != 0 {
		t.Errorf("pending_requests_count = %d, want 0", policy.Summary.PendingRequestsCount)
	}

	req, rec = mockNotificationRequest(http.MethodGet, "/api/v2/notifications/policy", "", 0)
	h.GetPolicy(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNotificationsHandler_UpdatePolicy_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	body := `{"for_not_following":"filter","for_new_accounts":"drop"}`
	req, rec := mockNotificationRequest(http.MethodPut, "/api/v2/notifications/policy", body, 12345)
	h.UpdatePolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var policy NotificationPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if policy.ForNotFollowing != "filter" {
		t.Errorf("for_not_following = %q, want 'filter'", policy.ForNotFollowing)
	}
	if policy.ForNewAccounts != "drop" {
		t.Errorf("for_new_accounts = %q, want 'drop'", policy.ForNewAccounts)
	}
}

func TestNotificationsHandler_UpdatePolicy_InvalidValue(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodPut, "/api/v2/notifications/policy", `{"for_not_following":"nope"}`, 12345)
	h.UpdatePolicy(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestNotificationsHandler_Get_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodGet, "/api/v2/notifications/mention:Status:123", "", 12345)
	req.SetPathValue("group_key", "mention:Status:123")

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp GroupedNotificationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(resp.NotificationGroups) == 0 || resp.NotificationGroups[0].GroupKey != "mention:Status:123" {
		t.Error("expected group_key in response")
	}
}

func TestNotificationsHandler_Dismiss_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodPost, "/api/v2/notifications/mention:Status:123/dismiss", "", 12345)
	req.SetPathValue("group_key", "mention:Status:123")

	h.Dismiss(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNotificationsHandler_Clear_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodPost, "/api/v2/notifications/clear", "", 12345)
	h.Clear(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockNotificationRequest(http.MethodPost, "/api/v2/notifications/clear", "", 0)
	h.Clear(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNotificationsHandler_Accounts_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodGet, "/api/v2/notifications/mention:Status:123/accounts", "", 12345)
	req.SetPathValue("group_key", "mention:Status:123")

	h.Accounts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var accounts []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
}

func TestNotificationsHandler_GroupNotifications(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)
	now := time.Now()

	notifications := []db.Notification{
		{
			ID:            10,
			FromAccountID: 1,
			Type:          pgtype.Text{String: "mention", Valid: true},
			ActivityType:  "Status",
			ActivityID:    42,
			CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		},
		{
			ID:            9,
			FromAccountID: 2,
			Type:          pgtype.Text{String: "mention", Valid: true},
			ActivityType:  "Status",
			ActivityID:    42,
			CreatedAt:     pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
		},
	}

	groups, accountIDs, statusIDs := h.groupNotifications(notifications)
	if len(groups) != 1 {
		t.Fatalf("groups length = %d, want 1", len(groups))
	}
	if groups[0].GroupKey != "mention:Status:42" {
		t.Errorf("GroupKey = %q, want %q", groups[0].GroupKey, "mention:Status:42")
	}
	if groups[0].NotificationsCount != 2 {
		t.Errorf("NotificationsCount = %d, want 2", groups[0].NotificationsCount)
	}
	if len(groups[0].SampleAccountIDs) != 2 {
		t.Errorf("SampleAccountIDs length = %d, want 2", len(groups[0].SampleAccountIDs))
	}
	if len(accountIDs) != 2 {
		t.Errorf("accountIDs length = %d, want 2", len(accountIDs))
	}
	if len(statusIDs) != 1 || statusIDs[0] != 42 {
		t.Errorf("statusIDs = %v, want [42]", statusIDs)
	}
}
