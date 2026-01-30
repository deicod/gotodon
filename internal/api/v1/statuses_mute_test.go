package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestStatusesHandler_Mute_Unauthorized(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statuses/1/mute", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.Mute(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestStatusesHandler_Mute_InvalidID(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPost, "/api/v1/statuses/bad/mute", "")
	req.SetPathValue("id", "bad")

	h.Mute(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStatusesHandler_Mute_MissingScope(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statuses/123/mute", nil)
	req.SetPathValue("id", "123")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Mute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStatusesHandler_Mute_DBUnavailable(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPost, "/api/v1/statuses/123/mute", "")
	req.SetPathValue("id", "123")

	h.Mute(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestStatusesHandler_Unmute_Unauthorized(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statuses/1/unmute", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.Unmute(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestStatusesHandler_Unmute_InvalidID(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPost, "/api/v1/statuses/bad/unmute", "")
	req.SetPathValue("id", "bad")

	h.Unmute(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStatusesHandler_Unmute_MissingScope(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statuses/123/unmute", nil)
	req.SetPathValue("id", "123")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Unmute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStatusesHandler_Unmute_DBUnavailable(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPost, "/api/v1/statuses/123/unmute", "")
	req.SetPathValue("id", "123")

	h.Unmute(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
