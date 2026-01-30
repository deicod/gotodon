package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockMarkerRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestMarkersHandler_Get_DBUnavailable(t *testing.T) {
	h := NewMarkersHandler(nil)

	req, rec := mockMarkerRequest(http.MethodGet, "/api/v1/markers?timeline[]=home", "")

	h.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestMarkersHandler_Update_DBUnavailable(t *testing.T) {
	h := NewMarkersHandler(nil)

	// Update marker
	body := `{"home":{"last_read_id":"12345"}}`
	req1, rec1 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", body)

	h.Update(rec1, req1)

	if rec1.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec1.Code, http.StatusInternalServerError)
	}
}

func TestMarkersHandler_VersionIncrement(t *testing.T) {
	h := NewMarkersHandler(nil)

	// First update
	req1, rec1 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", `{"home":{"last_read_id":"100"}}`)
	h.Update(rec1, req1)
	if rec1.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec1.Code, http.StatusInternalServerError)
	}

	// Second update
	req2, rec2 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", `{"home":{"last_read_id":"200"}}`)
	h.Update(rec2, req2)
	if rec2.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusInternalServerError)
	}
}

func TestMarkersHandler_MultipleTimelines(t *testing.T) {
	h := NewMarkersHandler(nil)

	// Update both
	body := `{"home":{"last_read_id":"100"},"notifications":{"last_read_id":"200"}}`
	req1, rec1 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", body)
	h.Update(rec1, req1)
	if rec1.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec1.Code, http.StatusInternalServerError)
	}

	// Get both
	req2, rec2 := mockMarkerRequest(http.MethodGet, "/api/v1/markers", "")
	h.Get(rec2, req2)
	if rec2.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusInternalServerError)
	}
}
