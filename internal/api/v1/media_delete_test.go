package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockAccountRequest(method, path, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	return req, httptest.NewRecorder()
}

func TestMediaHandler_Delete_Unauthorized(t *testing.T) {
	h := NewMediaHandler(nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/media/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMediaHandler_Delete_InvalidID(t *testing.T) {
	h := NewMediaHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodDelete, "/api/v1/media/bad", "")
	req.SetPathValue("id", "bad")

	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMediaHandler_Delete_NoDatabase(t *testing.T) {
	h := NewMediaHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodDelete, "/api/v1/media/123", "")
	req.SetPathValue("id", "123")

	h.Delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
