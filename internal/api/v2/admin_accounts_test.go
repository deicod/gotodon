package v2

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminAccountsHandler_ListEmpty(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts", "")

	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var accounts []AdminAccountV2
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected empty list, got %d", len(accounts))
	}
}

func TestAdminAccountsHandler_InvalidOrigin(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts?origin=bad", "")

	h.List(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestAdminAccountsHandler_InvalidStatus(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts?status=bad", "")

	h.List(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestAdminAccountsHandler_InvalidIP(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts?ip=not-an-ip", "")

	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var accounts []AdminAccountV2
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected empty list, got %d", len(accounts))
	}
}
