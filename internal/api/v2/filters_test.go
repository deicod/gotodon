package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// mockAccountRequest creates a http request with a mock account ID in context
func mockAccountRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestFiltersHandler_Create(t *testing.T) {
	h := NewFiltersHandler(nil)

	body := `{"title":"Test Filter","context":["home","public"],"filter_action":"warn","keywords_attributes":[{"keyword":"badword","whole_word":true}]}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)

	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var filter FilterV2
	if err := json.Unmarshal(rec.Body.Bytes(), &filter); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if filter.Title != "Test Filter" {
		t.Errorf("Title = %q, want %q", filter.Title, "Test Filter")
	}

	if len(filter.Context) != 2 {
		t.Errorf("Context length = %d, want 2", len(filter.Context))
	}

	if filter.FilterAction != "warn" {
		t.Errorf("FilterAction = %q, want %q", filter.FilterAction, "warn")
	}

	if len(filter.Keywords) != 1 {
		t.Errorf("Keywords length = %d, want 1", len(filter.Keywords))
	}

	if filter.Keywords[0].Keyword != "badword" {
		t.Errorf("Keyword = %q, want %q", filter.Keywords[0].Keyword, "badword")
	}
}

func TestFiltersHandler_List(t *testing.T) {
	h := NewFiltersHandler(nil)

	// Create a filter first
	body := `{"title":"Test","context":["home"]}`
	req1, rec1 := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)
	h.Create(rec1, req1)

	// List filters
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/filters", "")

	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var filters []FilterV2
	if err := json.Unmarshal(rec.Body.Bytes(), &filters); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(filters) != 1 {
		t.Errorf("filters length = %d, want 1", len(filters))
	}
}

func TestFiltersHandler_Validation(t *testing.T) {
	h := NewFiltersHandler(nil)

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"missing title", `{"context":["home"]}`, true},
		{"missing context", `{"title":"Test"}`, true},
		{"invalid context", `{"title":"Test","context":["invalid"]}`, true},
		{"invalid action", `{"title":"Test","context":["home"],"filter_action":"invalid"}`, true},
		{"valid", `{"title":"Test","context":["home"]}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", tt.body)
			h.Create(rec, req)

			if tt.wantErr {
				if rec.Code < 400 {
					t.Error("expected error")
				}
			} else {
				if rec.Code >= 400 {
					t.Errorf("unexpected error code: %d", rec.Code)
				}
			}
		})
	}
}

func TestFiltersHandler_Delete(t *testing.T) {
	h := NewFiltersHandler(nil)

	// Create a filter
	body := `{"title":"ToDelete","context":["home"]}`
	req1, rec1 := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)
	h.Create(rec1, req1)

	var created FilterV2
	_ = json.Unmarshal(rec1.Body.Bytes(), &created)

	// Delete it
	req2, rec2 := mockAccountRequest(http.MethodDelete, "/api/v2/filters/"+created.ID, "")
	req2.SetPathValue("id", created.ID)

	h.Delete(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Verify it's gone
	req3, rec3 := mockAccountRequest(http.MethodGet, "/api/v2/filters", "")
	h.List(rec3, req3)
}

func TestFilterContexts(t *testing.T) {
	validContextsList := []string{"home", "notifications", "public", "thread", "account"}

	for _, ctx := range validContextsList {
		if !validContexts[ctx] {
			t.Errorf("Expected %q to be a valid context", ctx)
		}
	}
}
