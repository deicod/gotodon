package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestAccountsHandler_Endorsements_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/endorsements", "", 0)
	req.SetPathValue("id", "123")
	h.Endorsements(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAccountsHandler_Endorsements_MissingScope(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/123/endorsements", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	req.SetPathValue("id", "123")
	rec := httptest.NewRecorder()

	h.Endorsements(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAccountsHandler_Endorsements_InvalidID(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/bad/endorsements", "", 1)
	req.SetPathValue("id", "bad")
	h.Endorsements(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
