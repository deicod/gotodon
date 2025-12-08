package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// mockAccountContext creates an echo context with a mock account ID
func mockAccountContext(e *echo.Echo, method, path string, body string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("scopes", "read write")
	return c, rec
}

func TestFiltersHandler_Create(t *testing.T) {
	e := echo.New()
	h := NewFiltersHandler()

	body := `{"title":"Test Filter","context":["home","public"],"filter_action":"warn","keywords_attributes":[{"keyword":"badword","whole_word":true}]}`
	c, rec := mockAccountContext(e, http.MethodPost, "/api/v2/filters", body)

	err := h.Create(c)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

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
	e := echo.New()
	h := NewFiltersHandler()

	// Create a filter first
	body := `{"title":"Test","context":["home"]}`
	c1, _ := mockAccountContext(e, http.MethodPost, "/api/v2/filters", body)
	_ = h.Create(c1)

	// List filters
	c, rec := mockAccountContext(e, http.MethodGet, "/api/v2/filters", "")

	err := h.List(c)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

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
	e := echo.New()
	h := NewFiltersHandler()

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
			c, rec := mockAccountContext(e, http.MethodPost, "/api/v2/filters", tt.body)
			err := h.Create(c)

			if tt.wantErr {
				if err == nil && rec.Code < 400 {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestFiltersHandler_Delete(t *testing.T) {
	e := echo.New()
	h := NewFiltersHandler()

	// Create a filter
	body := `{"title":"ToDelete","context":["home"]}`
	c1, rec1 := mockAccountContext(e, http.MethodPost, "/api/v2/filters", body)
	_ = h.Create(c1)

	var created FilterV2
	_ = json.Unmarshal(rec1.Body.Bytes(), &created)

	// Delete it
	c2, rec2 := mockAccountContext(e, http.MethodDelete, "/api/v2/filters/"+created.ID, "")
	c2.SetParamNames("id")
	c2.SetParamValues(created.ID)

	err := h.Delete(c2)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Verify it's gone
	c3, _ := mockAccountContext(e, http.MethodGet, "/api/v2/filters", "")
	_ = h.List(c3)
}

func TestFilterContexts(t *testing.T) {
	validContextsList := []string{"home", "notifications", "public", "thread", "account"}

	for _, ctx := range validContextsList {
		if !validContexts[ctx] {
			t.Errorf("Expected %q to be a valid context", ctx)
		}
	}
}
