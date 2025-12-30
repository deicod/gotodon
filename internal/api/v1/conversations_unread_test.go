package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConversationsHandler_MarkUnread_Unauthorized(t *testing.T) {
	h := NewConversationsHandler(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/1/unread", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.MarkUnread(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestConversationsHandler_MarkUnread_InvalidID(t *testing.T) {
	h := NewConversationsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodPost, "/api/v1/conversations/bad/unread", "")
	req.SetPathValue("id", "bad")

	h.MarkUnread(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestConversationsHandler_MarkUnread_NoDatabase(t *testing.T) {
	h := NewConversationsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodPost, "/api/v1/conversations/123/unread", "")
	req.SetPathValue("id", "123")

	h.MarkUnread(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
