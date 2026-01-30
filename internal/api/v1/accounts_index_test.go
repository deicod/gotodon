package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestAccountsHandler_Index_NoIDs_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAccountsHandler_Index_TooManyIDs(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)
	values := url.Values{}
	for i := 0; i < defaultAccountsLimit+1; i++ {
		values.Add("id[]", strconv.Itoa(i+1))
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?"+values.Encode(), nil)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestAccountsHandler_Index_RequiresReadScopeWhenTokenPresent(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAccountsHandler_Index_AllowsReadScope(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyScopes, "read:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
