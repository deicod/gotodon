package admin

import (
	"net/http"
	"strings"
	"testing"
)

// Without a database the metrics handlers answer with an empty list. Routed
// requests never get here without one: RegisterRoutes derives the role
// lookup from the same queries and answers 500 first.
func TestMetricsHandlers_GetWithNilQueries(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		body    string
		handler http.HandlerFunc
	}{
		{"dimensions", "/api/v1/admin/dimensions", `{"keys":["languages"]}`, NewDimensionsHandler(nil, nil, nil).Get},
		{"measures", "/api/v1/admin/measures", `{"keys":["active_users"]}`, NewMeasuresHandler(nil).Get},
		{"retention", "/api/v1/admin/retention", `{}`, NewRetentionHandler(nil).Get},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockAdminRequest(http.MethodPost, tt.path, tt.body)
			tt.handler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
				t.Fatalf("body = %s, want []", got)
			}
		})
	}
}
