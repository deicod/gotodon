package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestEndorsementsHandler_EndorseAliases_MockMode(t *testing.T) {
	h := NewEndorsementsHandler(nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	accountID := int64(123)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/456/endorse", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/endorsements", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var accounts []Account
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != "456" {
		t.Fatalf("unexpected endorsements: %+v", accounts)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/456/unendorse", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/endorsements", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected empty endorsements, got %d", len(accounts))
	}
}

func TestEndorsementsHandler_EndorseAlias_RequiresAuth(t *testing.T) {
	h := NewEndorsementsHandler(nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/456/endorse", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
