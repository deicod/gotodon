package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
)

func TestFeaturedTagsHandler_Suggestions_DBUnavailable(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewFeaturedTagsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/featured_tags/suggestions", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Suggestions(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFeaturedTagsHandler_Suggestions_RequiresAuth(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewFeaturedTagsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/featured_tags/suggestions", nil)
	rec := httptest.NewRecorder()

	h.Suggestions(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestFeaturedTagsHandler_Suggestions_MissingScope(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewFeaturedTagsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/featured_tags/suggestions", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Suggestions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
