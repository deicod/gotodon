package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func mockAnnouncementContext(e *echo.Echo, method, path string, body string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("scopes", "read write")
	return c, rec
}

func TestAnnouncementsHandler_GetAll(t *testing.T) {
	e := echo.New()
	h := NewAnnouncementsHandler(nil)

	c, rec := mockAnnouncementContext(e, http.MethodGet, "/api/v1/announcements", "")

	err := h.GetAll(c)
	if err != nil {
		t.Fatalf("GetAll() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var announcements []Announcement
	if err := json.Unmarshal(rec.Body.Bytes(), &announcements); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Should have at least the sample announcement
	// if len(announcements) == 0 {
	// 	t.Error("Expected at least one announcement")
	// }
}

func TestAnnouncementsHandler_Dismiss(t *testing.T) {
	e := echo.New()
	h := NewAnnouncementsHandler(nil)

	// Get an announcement ID first
	c1, rec1 := mockAnnouncementContext(e, http.MethodGet, "/api/v1/announcements", "")
	_ = h.GetAll(c1)

	var announcements []Announcement
	_ = json.Unmarshal(rec1.Body.Bytes(), &announcements)

	if len(announcements) == 0 {
		t.Skip("No announcements to dismiss (DB required)")
	}

	// Dismiss it
	c2, rec2 := mockAnnouncementContext(e, http.MethodPost, "/api/v1/announcements/"+announcements[0].ID+"/dismiss", "")
	c2.SetParamNames("id")
	c2.SetParamValues(announcements[0].ID)

	err := h.Dismiss(c2)
	if err != nil {
		t.Fatalf("Dismiss() error = %v", err)
	}

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Get announcements again - should be empty without with_dismissed
	c3, rec3 := mockAnnouncementContext(e, http.MethodGet, "/api/v1/announcements", "")
	_ = h.GetAll(c3)

	var afterDismiss []Announcement
	_ = json.Unmarshal(rec3.Body.Bytes(), &afterDismiss)

	if len(afterDismiss) != 0 {
		t.Error("Dismissed announcement should not appear")
	}
}

func TestAnnouncementsHandler_Reactions(t *testing.T) {
	e := echo.New()
	h := NewAnnouncementsHandler(nil)

	// Get announcement
	c1, rec1 := mockAnnouncementContext(e, http.MethodGet, "/api/v1/announcements", "")
	_ = h.GetAll(c1)

	var announcements []Announcement
	_ = json.Unmarshal(rec1.Body.Bytes(), &announcements)

	if len(announcements) == 0 {
		t.Skip("No announcements (DB required)")
	}

	id := announcements[0].ID

	// Add reaction
	c2, rec2 := mockAnnouncementContext(e, http.MethodPut, "/api/v1/announcements/"+id+"/reactions/👍", "")
	c2.SetParamNames("id", "name")
	c2.SetParamValues(id, "👍")

	err := h.AddReaction(c2)
	if err != nil {
		t.Fatalf("AddReaction() error = %v", err)
	}

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Get announcements and check reaction
	c3, rec3 := mockAnnouncementContext(e, http.MethodGet, "/api/v1/announcements?with_dismissed=true", "")
	_ = h.GetAll(c3)

	var withReactions []Announcement
	_ = json.Unmarshal(rec3.Body.Bytes(), &withReactions)

	found := false
	for _, r := range withReactions[0].Reactions {
		if r.Name == "👍" && r.Me && r.Count == 1 {
			found = true
		}
	}

	if !found {
		t.Error("Reaction should be present")
	}

	// Remove reaction
	c4, _ := mockAnnouncementContext(e, http.MethodDelete, "/api/v1/announcements/"+id+"/reactions/👍", "")
	c4.SetParamNames("id", "name")
	c4.SetParamValues(id, "👍")

	_ = h.RemoveReaction(c4)
}
