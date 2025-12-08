package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func mockMarkerContext(e *echo.Echo, method, path string, body string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("scopes", "read write")
	return c, rec
}

func TestMarkersHandler_GetEmpty(t *testing.T) {
	e := echo.New()
	h := NewMarkersHandler(nil)

	c, rec := mockMarkerContext(e, http.MethodGet, "/api/v1/markers?timeline[]=home", "")

	err := h.Get(c)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result MarkersResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &result)

	// Should be empty since no markers set
	if len(result) != 0 {
		t.Errorf("result length = %d, want 0", len(result))
	}
}

func TestMarkersHandler_UpdateAndGet(t *testing.T) {
	e := echo.New()
	h := NewMarkersHandler(nil)

	// Update marker
	body := `{"home":{"last_read_id":"12345"}}`
	c1, rec1 := mockMarkerContext(e, http.MethodPost, "/api/v1/markers", body)

	err := h.Update(c1)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if rec1.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec1.Code, http.StatusOK)
	}

	var updateResult MarkersResponse
	_ = json.Unmarshal(rec1.Body.Bytes(), &updateResult)

	if updateResult["home"] == nil {
		t.Fatal("home marker should be set")
	}

	if updateResult["home"].LastReadID != "12345" {
		t.Errorf("LastReadID = %q, want %q", updateResult["home"].LastReadID, "12345")
	}

	// Get marker
	c2, rec2 := mockMarkerContext(e, http.MethodGet, "/api/v1/markers?timeline[]=home", "")
	_ = h.Get(c2)

	var getResult MarkersResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &getResult)

	if getResult["home"] == nil {
		t.Fatal("home marker should exist")
	}

	if getResult["home"].LastReadID != "12345" {
		t.Errorf("LastReadID = %q, want %q", getResult["home"].LastReadID, "12345")
	}
}

func TestMarkersHandler_VersionIncrement(t *testing.T) {
	e := echo.New()
	h := NewMarkersHandler(nil)

	// First update
	c1, _ := mockMarkerContext(e, http.MethodPost, "/api/v1/markers", `{"home":{"last_read_id":"100"}}`)
	_ = h.Update(c1)

	// Second update
	c2, rec2 := mockMarkerContext(e, http.MethodPost, "/api/v1/markers", `{"home":{"last_read_id":"200"}}`)
	_ = h.Update(c2)

	var result MarkersResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &result)

	if result["home"].Version != 1 {
		t.Errorf("Version = %d, want 1", result["home"].Version)
	}
}

func TestMarkersHandler_MultipleTimelines(t *testing.T) {
	e := echo.New()
	h := NewMarkersHandler(nil)

	// Update both
	body := `{"home":{"last_read_id":"100"},"notifications":{"last_read_id":"200"}}`
	c1, _ := mockMarkerContext(e, http.MethodPost, "/api/v1/markers", body)
	_ = h.Update(c1)

	// Get both
	c2, rec2 := mockMarkerContext(e, http.MethodGet, "/api/v1/markers", "")
	_ = h.Get(c2)

	var result MarkersResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &result)

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
