package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockNotificationRequest(method, path string, body string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
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

func TestNotificationsHandler_UnreadCount_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user gets count",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockNotificationRequest(http.MethodGet, "/api/v1/notifications/unread_count", "", tt.accountID)

			h.UnreadCount(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var result map[string]int64
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if _, ok := result["count"]; !ok {
					t.Error("expected 'count' field in response")
				}
			}
		})
	}
}

func TestNotificationsHandler_GetPolicy_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodGet, "/api/v1/notifications/policy", "", 12345)
	h.GetPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var policy NotificationPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Check default values
	if policy.ForNotFollowing != "accept" {
		t.Errorf("for_not_following = %q, want 'accept'", policy.ForNotFollowing)
	}
}

func TestNotificationsHandler_UpdatePolicy_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	body := `{"for_not_following":"filter","for_new_accounts":"drop"}`
	req, rec := mockNotificationRequest(http.MethodPut, "/api/v1/notifications/policy", body, 12345)
	h.UpdatePolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
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

func TestNotificationsHandler_ListRequests_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user gets empty list",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockNotificationRequest(http.MethodGet, "/api/v1/notifications/requests", "", tt.accountID)

			h.ListRequests(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestNotificationsHandler_AcceptRequest_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodPost, "/api/v1/notifications/requests/123/accept", "", 12345)
	req.SetPathValue("id", "123")

	h.AcceptRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNotificationsHandler_DismissRequest_MockMode(t *testing.T) {
	h := NewNotificationsHandler(nil, nil)

	req, rec := mockNotificationRequest(http.MethodPost, "/api/v1/notifications/requests/123/dismiss", "", 12345)
	req.SetPathValue("id", "123")

	h.DismissRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestPolicyConversions(t *testing.T) {
	// Test int to string
	if got := policyIntToString(0); got != "accept" {
		t.Errorf("policyIntToString(0) = %q, want 'accept'", got)
	}
	if got := policyIntToString(1); got != "filter" {
		t.Errorf("policyIntToString(1) = %q, want 'filter'", got)
	}
	if got := policyIntToString(2); got != "drop" {
		t.Errorf("policyIntToString(2) = %q, want 'drop'", got)
	}
	if got := policyIntToString(99); got != "accept" {
		t.Errorf("policyIntToString(99) = %q, want 'accept' (default)", got)
	}

	// Test string to int
	if got := policyStringToInt("accept"); got != 0 {
		t.Errorf("policyStringToInt('accept') = %d, want 0", got)
	}
	if got := policyStringToInt("filter"); got != 1 {
		t.Errorf("policyStringToInt('filter') = %d, want 1", got)
	}
	if got := policyStringToInt("drop"); got != 2 {
		t.Errorf("policyStringToInt('drop') = %d, want 2", got)
	}
	if got := policyStringToInt("unknown"); got != 0 {
		t.Errorf("policyStringToInt('unknown') = %d, want 0 (default)", got)
	}
}
