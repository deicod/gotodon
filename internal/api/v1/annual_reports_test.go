package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockAnnualReportsRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
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

func TestAnnualReportsHandler_List_MockMode(t *testing.T) {
	h := NewAnnualReportsHandler(nil)

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user gets list",
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
			req, rec := mockAnnualReportsRequest(http.MethodGet, "/api/v1/annual_reports", tt.accountID)

			h.List(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var reports []AnnualReport
				if err := json.Unmarshal(rec.Body.Bytes(), &reports); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
			}
		})
	}
}

func TestAnnualReportsHandler_Get_MockMode(t *testing.T) {
	h := NewAnnualReportsHandler(nil)

	tests := []struct {
		name      string
		year      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user gets report",
			year:      "2023",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			year:      "2023",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "invalid year returns not found",
			year:      "invalid",
			accountID: 12345,
			wantCode:  http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockAnnualReportsRequest(http.MethodGet, "/api/v1/annual_reports/"+tt.year, tt.accountID)
			req.SetPathValue("id", tt.year)

			h.Get(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var report AnnualReport
				if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if report.Year != 2023 {
					t.Errorf("year = %d, want 2023", report.Year)
				}
			}
		})
	}
}

func TestAnnualReportsHandler_MarkAsRead_MockMode(t *testing.T) {
	h := NewAnnualReportsHandler(nil)

	tests := []struct {
		name      string
		year      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can mark as read",
			year:      "2023",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			year:      "2023",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockAnnualReportsRequest(http.MethodPost, "/api/v1/annual_reports/"+tt.year+"/read", tt.accountID)
			req.SetPathValue("id", tt.year)

			h.MarkAsRead(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var report AnnualReport
				if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if report.ViewedAt == nil {
					t.Error("expected viewed_at to be set")
				}
			}
		})
	}
}
