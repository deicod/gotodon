package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockEmailsRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestEmailsHandler_Confirmations_MockMode(t *testing.T) {
	h := NewEmailsHandler(nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can request confirmation",
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
			req, rec := mockEmailsRequest(http.MethodPost, "/api/v1/emails/confirmations", tt.accountID)

			h.Confirmations(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestEmailsHandler_CheckConfirmation_MockMode(t *testing.T) {
	h := NewEmailsHandler(nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user gets confirmation status",
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
			req, rec := mockEmailsRequest(http.MethodGet, "/api/v1/emails/check_confirmation", tt.accountID)

			h.CheckConfirmation(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var result map[string]bool
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if _, ok := result["confirmed"]; !ok {
					t.Error("expected 'confirmed' field in response")
				}
			}
		})
	}
}
