package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestStatusesHandler_InteractionPolicy_Unauthorized(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/statuses/1/interaction_policy", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	h.InteractionPolicy(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestStatusesHandler_InteractionPolicy_InvalidID(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPut, "/api/v1/statuses/bad/interaction_policy", `{"quote_approval_policy":"public"}`)
	req.SetPathValue("id", "bad")

	h.InteractionPolicy(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStatusesHandler_InteractionPolicy_InvalidPolicy(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPut, "/api/v1/statuses/123/interaction_policy", `{"quote_approval_policy":"bad"}`)
	req.SetPathValue("id", "123")

	h.InteractionPolicy(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestStatusesHandler_InteractionPolicy_MissingScope(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/statuses/123/interaction_policy", strings.NewReader(`{"quote_approval_policy":"public"}`))
	req.SetPathValue("id", "123")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	h.InteractionPolicy(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStatusesHandler_InteractionPolicy_NoDatabase(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req, rec := mockAccountRequest(http.MethodPut, "/api/v1/statuses/123/interaction_policy", `{"quote_approval_policy":"public"}`)
	req.SetPathValue("id", "123")

	h.InteractionPolicy(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
