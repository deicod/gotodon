package v2

import (
	"net/http"
	"testing"
)

func TestAdminAccountsHandler_NoDB(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts", "")

	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAdminAccountsHandler_InvalidOrigin(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts?origin=bad", "")

	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAdminAccountsHandler_InvalidStatus(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts?status=bad", "")

	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAdminAccountsHandler_InvalidIP(t *testing.T) {
	h := NewAdminAccountsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/admin/accounts?ip=not-an-ip", "")

	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
