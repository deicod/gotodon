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

func mockAnnouncementRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Set mock context
	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestAnnouncementsHandler_GetAll(t *testing.T) {
	h := NewAnnouncementsHandler(nil)

	req, rec := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")

	h.GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var announcements []Announcement
	if err := json.Unmarshal(rec.Body.Bytes(), &announcements); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
}

func TestAnnouncementsHandler_Dismiss(t *testing.T) {
	h := NewAnnouncementsHandler(nil)

	// Get an announcement ID first
	req1, rec1 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec1, req1)

	var announcements []Announcement
	_ = json.Unmarshal(rec1.Body.Bytes(), &announcements)

	if len(announcements) == 0 {
		t.Skip("No announcements to dismiss (DB required)")
	}

	// Dismiss it
	req2, rec2 := mockAnnouncementRequest(http.MethodPost, "/api/v1/announcements/"+announcements[0].ID+"/dismiss", "")
	req2.SetPathValue("id", announcements[0].ID)

	h.Dismiss(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Get announcements again - should be empty without with_dismissed
	req3, rec3 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec3, req3)

	var afterDismiss []Announcement
	_ = json.Unmarshal(rec3.Body.Bytes(), &afterDismiss)

	if len(afterDismiss) != 0 {
		t.Error("Dismissed announcement should not appear")
	}
}

func TestAnnouncementsHandler_Reactions(t *testing.T) {
	h := NewAnnouncementsHandler(nil)

	// Get announcement
	req1, rec1 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements", "")
	h.GetAll(rec1, req1)

	var announcements []Announcement
	_ = json.Unmarshal(rec1.Body.Bytes(), &announcements)

	if len(announcements) == 0 {
		t.Skip("No announcements (DB required)")
	}

	id := announcements[0].ID

	// Add reaction
	req2, rec2 := mockAnnouncementRequest(http.MethodPut, "/api/v1/announcements/"+id+"/reactions/👍", "")
	req2.SetPathValue("id", id)
	req2.SetPathValue("name", "👍")

	h.AddReaction(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec2.Code, http.StatusOK)
	}

	// Get announcements and check reaction
	req3, rec3 := mockAnnouncementRequest(http.MethodGet, "/api/v1/announcements?with_dismissed=true", "")
	h.GetAll(rec3, req3)

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
	req4, rec4 := mockAnnouncementRequest(http.MethodDelete, "/api/v1/announcements/"+id+"/reactions/👍", "")
	req4.SetPathValue("id", id)
	req4.SetPathValue("name", "👍")

	h.RemoveReaction(rec4, req4)
}
