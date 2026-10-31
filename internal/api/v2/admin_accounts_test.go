package v2

import (
	"net/http"
	"strings"
	"testing"
)

// These call List directly: authorization happens at route registration.
func TestAdminAccountsHandler_ListWithoutDB(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantCode int
		wantBody string
	}{
		{"no database", "/api/v2/admin/accounts", http.StatusInternalServerError, "Admin data unavailable"},
		{"invalid origin", "/api/v2/admin/accounts?origin=bad", http.StatusUnprocessableEntity, "Unknown origin: bad"},
		{"invalid status", "/api/v2/admin/accounts?status=bad", http.StatusUnprocessableEntity, "Unknown status: bad"},
		{"invalid ip matches nothing", "/api/v2/admin/accounts?ip=not-an-ip", http.StatusOK, "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAdminAccountsHandler(nil, nil)
			req, rec := mockAccountRequest(http.MethodGet, tt.path, "")

			h.List(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}
