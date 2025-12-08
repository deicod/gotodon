package v1

import (
	"context"
	"encoding/json"
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

func TestMarkersHandler_GetEmpty(t *testing.T) {
	h := NewMarkersHandler(nil)

	req, rec := mockMarkerRequest(http.MethodGet, "/api/v1/markers?timeline[]=home", "")

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result MarkersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Should be empty since no markers set
	if len(result) != 0 {
		t.Errorf("result length = %d, want 0", len(result))
	}
}

func TestMarkersHandler_UpdateAndGet(t *testing.T) {
	h := NewMarkersHandler(nil)

	// Update marker
	body := `{"home":{"last_read_id":"12345"}}`
	req1, rec1 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", body)

	h.Update(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec1.Code, http.StatusOK)
	}

	var updateResult MarkersResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &updateResult); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if updateResult["home"] == nil {
		t.Fatal("home marker should be set")
	}

	if updateResult["home"].LastReadID != "12345" {
		t.Errorf("LastReadID = %q, want %q", updateResult["home"].LastReadID, "12345")
	}

	// Get marker
	req2, rec2 := mockMarkerRequest(http.MethodGet, "/api/v1/markers?timeline[]=home", "")
	h.Get(rec2, req2)

	var getResult MarkersResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &getResult); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if getResult["home"] == nil {
		t.Fatal("home marker should exist")
	}

	if getResult["home"].LastReadID != "12345" {
		t.Errorf("LastReadID = %q, want %q", getResult["home"].LastReadID, "12345")
	}
}

func TestMarkersHandler_VersionIncrement(t *testing.T) {
	h := NewMarkersHandler(nil)

	// First update
	req1, rec1 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", `{"home":{"last_read_id":"100"}}`)
	h.Update(rec1, req1)

	// Second update
	req2, rec2 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", `{"home":{"last_read_id":"200"}}`)
	h.Update(rec2, req2)

	var result MarkersResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if result["home"].Version != 1 {
		t.Errorf("Version = %d, want 1", result["home"].Version)
	}
}

func TestMarkersHandler_MultipleTimelines(t *testing.T) {
	h := NewMarkersHandler(nil)

	// Update both
	body := `{"home":{"last_read_id":"100"},"notifications":{"last_read_id":"200"}}`
	req1, rec1 := mockMarkerRequest(http.MethodPost, "/api/v1/markers", body)
	h.Update(rec1, req1)

	// Get both
	req2, rec2 := mockMarkerRequest(http.MethodGet, "/api/v1/markers", "")
	h.Get(rec2, req2)

	var result MarkersResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if result["home"] == nil || result["notifications"] == nil {
		t.Error("Both markers should exist")
	}

	if result["home"].LastReadID != "100" {
		t.Errorf("home LastReadID = %q, want %q", result["home"].LastReadID, "100")
	}

	if result["notifications"].LastReadID != "200" {
		t.Errorf("notifications LastReadID = %q, want %q", result["notifications"].LastReadID, "200")
	}
}
