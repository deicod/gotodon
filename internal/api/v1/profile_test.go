package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockProfileRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
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

func TestProfileHandler_DeleteAvatar_MockMode(t *testing.T) {
	h := NewProfileHandler(nil, nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can delete avatar",
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
			req, rec := mockProfileRequest(http.MethodDelete, "/api/v1/profile/avatar", tt.accountID)

			h.DeleteAvatar(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var cred CredentialAccount
				if err := json.Unmarshal(rec.Body.Bytes(), &cred); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
			}
		})
	}
}

func TestProfileHandler_DeleteHeader_MockMode(t *testing.T) {
	h := NewProfileHandler(nil, nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can delete header",
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
			req, rec := mockProfileRequest(http.MethodDelete, "/api/v1/profile/header", tt.accountID)

			h.DeleteHeader(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var cred CredentialAccount
				if err := json.Unmarshal(rec.Body.Bytes(), &cred); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
			}
		})
	}
}
