package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockFilterRequest(method, path, body string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
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

func TestFiltersHandler_Create_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	body := `{"phrase":"spoilers","context":["home"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)

	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFiltersHandler_List_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	req, rec := mockFilterRequest(http.MethodGet, "/api/v1/filters", "", 12345)
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFiltersHandler_Get_Update_Delete_DBUnavailable(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	req, rec := mockFilterRequest(http.MethodGet, "/api/v1/filters/123", "", 12345)
	req.SetPathValue("id", "123")
	h.Get(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	updateBody := `{"phrase":"updated","context":["public"],"whole_word":false,"irreversible":true,"expires_in":3600}`
	req, rec = mockFilterRequest(http.MethodPut, "/api/v1/filters/123", updateBody, 12345)
	req.SetPathValue("id", "123")
	h.Update(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockFilterRequest(http.MethodDelete, "/api/v1/filters/123", "", 12345)
	req.SetPathValue("id", "123")
	h.Delete(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFiltersHandler_Create_InvalidContext(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	body := `{"phrase":"test","context":["nope"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestFiltersHandler_List_Unauthorized(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	req, rec := mockFilterRequest(http.MethodGet, "/api/v1/filters", "", 0)
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
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

func TestFiltersHandler_PublishesFiltersChanged(t *testing.T) {
	h := NewFiltersHandler(nil, nil)
	publisher := &stubEventPublisher{}
	h.SetPublisher(publisher)

	body := `{"phrase":"spoilers","context":["home"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)

	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("events = %d, want 0", len(publisher.events))
	}
}
