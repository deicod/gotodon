package v2

import (
	"context"
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

func TestFiltersHandler_Create_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil)

	body := `{"title":"Test Filter","context":["home","public"],"filter_action":"warn","keywords_attributes":[{"keyword":"badword","whole_word":true}]}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)

	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFiltersHandler_List_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil)

	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/filters", "")
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
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
				if rec.Code != http.StatusUnprocessableEntity {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
			}
		})
	}
}

func TestFiltersHandler_Delete_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil)

	req, rec := mockAccountRequest(http.MethodDelete, "/api/v2/filters/123", "")
	req.SetPathValue("id", "123")

	h.Delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFilterContexts(t *testing.T) {
	validContextsList := []string{"home", "notifications", "public", "thread", "account"}

	for _, ctx := range validContextsList {
		if !validContexts[ctx] {
			t.Errorf("Expected %q to be a valid context", ctx)
		}
	}
}

func TestFiltersHandler_KeywordRoutes_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil)

	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/filters/1/keywords", "")
	req.SetPathValue("filter_id", "1")
	h.ListKeywords(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/keywords/1", "")
	req.SetPathValue("id", "1")
	h.GetKeywordByID(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	updateBody := `{"keyword":"worse","whole_word":false}`
	req, rec = mockAccountRequest(http.MethodPut, "/api/v2/filters/keywords/1", updateBody)
	req.SetPathValue("id", "1")
	h.UpdateKeywordByID(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/keywords/1", "")
	req.SetPathValue("id", "1")
	h.DeleteKeywordByID(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	createBody := `{"keyword":"bad","whole_word":true}`
	req, rec = mockAccountRequest(http.MethodPost, "/api/v2/filters/1/keywords", createBody)
	req.SetPathValue("filter_id", "1")
	h.CreateKeyword(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/1/keywords/1", "")
	req.SetPathValue("filter_id", "1")
	req.SetPathValue("id", "1")
	h.DeleteKeyword(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

type stubEventPublisher struct {
	events []publishedEvent
}

type publishedEvent struct {
	eventType string
	payload   any
	streams   []string
}

func (s *stubEventPublisher) PublishEventToRedis(ctx context.Context, eventType string, payload any, streams []string) error {
	s.events = append(s.events, publishedEvent{eventType: eventType, payload: payload, streams: streams})
	return nil
}

func TestFiltersHandler_Create_DBUnavailable_DoesNotPublish(t *testing.T) {
	h := NewFiltersHandler(nil)
	publisher := &stubEventPublisher{}
	h.SetPublisher(publisher)

	body := `{"title":"Test Filter","context":["home"],"filter_action":"warn"}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)

	h.Create(rec, req)

	if len(publisher.events) != 0 {
		t.Fatalf("events = %d, want 0", len(publisher.events))
	}
}

func TestFiltersHandler_DeleteKeyword_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil)

	req, rec := mockAccountRequest(http.MethodDelete, "/api/v2/filters/1/keywords/2", "")
	req.SetPathValue("filter_id", "1")
	req.SetPathValue("id", "2")
	h.DeleteKeyword(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFiltersHandler_StatusRoutes_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil)

	statusBody := `{"status_id":"12345"}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters/1/statuses", statusBody)
	req.SetPathValue("filter_id", "1")
	h.CreateStatus(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/1/statuses", "")
	req.SetPathValue("filter_id", "1")
	h.ListStatuses(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/statuses/1", "")
	req.SetPathValue("id", "1")
	h.GetStatus(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/statuses/1", "")
	req.SetPathValue("id", "1")
	h.DeleteStatus(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
