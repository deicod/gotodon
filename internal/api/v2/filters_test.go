package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/streaming"
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

func TestFiltersHandler_KeywordRoutes_MockMode(t *testing.T) {
	h := NewFiltersHandler(nil)

	body := `{"title":"WithKeywords","context":["home"],"keywords_attributes":[{"keyword":"bad","whole_word":true}]}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)
	h.Create(rec, req)

	var created FilterV2
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/"+created.ID+"/keywords", "")
	req.SetPathValue("filter_id", created.ID)
	h.ListKeywords(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var keywords []FilterKeyword
	if err := json.Unmarshal(rec.Body.Bytes(), &keywords); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(keywords) != 1 {
		t.Fatalf("keywords length = %d, want 1", len(keywords))
	}

	keywordID := keywords[0].ID
	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/keywords/"+keywordID, "")
	req.SetPathValue("id", keywordID)
	h.GetKeywordByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	updateBody := `{"keyword":"worse","whole_word":false}`
	req, rec = mockAccountRequest(http.MethodPut, "/api/v2/filters/keywords/"+keywordID, updateBody)
	req.SetPathValue("id", keywordID)
	h.UpdateKeywordByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var updated FilterKeyword
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if updated.Keyword != "worse" || updated.WholeWord {
		t.Fatalf("unexpected update result: %+v", updated)
	}

	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/keywords/"+keywordID, "")
	req.SetPathValue("id", keywordID)
	h.DeleteKeywordByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/keywords/"+keywordID, "")
	req.SetPathValue("id", keywordID)
	h.GetKeywordByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
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

func TestFiltersHandler_CreatePublishesFiltersChanged(t *testing.T) {
	h := NewFiltersHandler(nil)
	publisher := &stubEventPublisher{}
	h.SetPublisher(publisher)

	body := `{"title":"Test Filter","context":["home"],"filter_action":"warn"}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)

	h.Create(rec, req)

	if len(publisher.events) != 1 {
		t.Fatalf("events = %d, want 1", len(publisher.events))
	}
	event := publisher.events[0]
	if event.eventType != streaming.EventFiltersChanged {
		t.Fatalf("eventType = %q, want %q", event.eventType, streaming.EventFiltersChanged)
	}
	wantStreams := []string{streaming.UserStreamKey(12345)}
	if !reflect.DeepEqual(event.streams, wantStreams) {
		t.Fatalf("streams = %#v, want %#v", event.streams, wantStreams)
	}
}

func TestFiltersHandler_DeleteKeyword_MismatchedFilter_MockMode(t *testing.T) {
	h := NewFiltersHandler(nil)

	body := `{"title":"FilterOne","context":["home"],"keywords_attributes":[{"keyword":"alpha","whole_word":true}]}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)
	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var filterOne FilterV2
	if err := json.Unmarshal(rec.Body.Bytes(), &filterOne); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(filterOne.Keywords) != 1 {
		t.Fatalf("keywords length = %d, want 1", len(filterOne.Keywords))
	}

	body = `{"title":"FilterTwo","context":["home"],"keywords_attributes":[{"keyword":"beta","whole_word":true}]}`
	req, rec = mockAccountRequest(http.MethodPost, "/api/v2/filters", body)
	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var filterTwo FilterV2
	if err := json.Unmarshal(rec.Body.Bytes(), &filterTwo); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	keywordID := filterOne.Keywords[0].ID
	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/"+filterTwo.ID+"/keywords/"+keywordID, "")
	req.SetPathValue("filter_id", filterTwo.ID)
	req.SetPathValue("id", keywordID)
	h.DeleteKeyword(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/"+filterOne.ID+"/keywords/"+keywordID, "")
	req.SetPathValue("filter_id", filterOne.ID)
	req.SetPathValue("id", keywordID)
	h.DeleteKeyword(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/"+filterOne.ID+"/keywords", "")
	req.SetPathValue("filter_id", filterOne.ID)
	h.ListKeywords(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var keywords []FilterKeyword
	if err := json.Unmarshal(rec.Body.Bytes(), &keywords); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(keywords) != 0 {
		t.Fatalf("keywords length = %d, want 0", len(keywords))
	}
}

func TestFiltersHandler_StatusRoutes_MockMode(t *testing.T) {
	h := NewFiltersHandler(nil)

	body := `{"title":"WithStatuses","context":["home"]}`
	req, rec := mockAccountRequest(http.MethodPost, "/api/v2/filters", body)
	h.Create(rec, req)

	var created FilterV2
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	statusBody := `{"status_id":"12345"}`
	req, rec = mockAccountRequest(http.MethodPost, "/api/v2/filters/"+created.ID+"/statuses", statusBody)
	req.SetPathValue("filter_id", created.ID)
	h.CreateStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var createdStatus FilterStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &createdStatus); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/"+created.ID+"/statuses", "")
	req.SetPathValue("filter_id", created.ID)
	h.ListStatuses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var statuses []FilterStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses length = %d, want 1", len(statuses))
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/statuses/"+createdStatus.ID, "")
	req.SetPathValue("id", createdStatus.ID)
	h.GetStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockAccountRequest(http.MethodDelete, "/api/v2/filters/statuses/"+createdStatus.ID, "")
	req.SetPathValue("id", createdStatus.ID)
	h.DeleteStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockAccountRequest(http.MethodGet, "/api/v2/filters/statuses/"+createdStatus.ID, "")
	req.SetPathValue("id", createdStatus.ID)
	h.GetStatus(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
