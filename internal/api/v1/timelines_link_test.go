package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestTimelinesHandler_Link_MissingURL(t *testing.T) {
	h := NewTimelinesHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/link", nil)
	rec := httptest.NewRecorder()

	h.Link(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTimelinesHandler_Link_MockMode(t *testing.T) {
	h := NewTimelinesHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/link?url=https://example.com", nil)
	rec := httptest.NewRecorder()

	h.Link(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var statuses []Status
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("statuses length = %d, want 0", len(statuses))
	}
}

func TestTimelinesHandler_Link_MissingScope(t *testing.T) {
	h := NewTimelinesHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/link?url=https://example.com", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Link(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestTimelinesHandler_Link_InvalidPagination(t *testing.T) {
	h := NewTimelinesHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/link?url=https://example.com&max_id=bad", nil)
	rec := httptest.NewRecorder()

	h.Link(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}
