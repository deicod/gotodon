package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestAccountsHandler_Endorsements_MockMode(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/endorsements", "", 0)
	req.SetPathValue("id", "123")
	h.Endorsements(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var accounts []Account
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts length = %d, want 0", len(accounts))
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
