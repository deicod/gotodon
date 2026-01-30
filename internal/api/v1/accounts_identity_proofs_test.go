package v1

import (
	"net/http"
	"testing"
)

func TestAccountsHandler_IdentityProofs_RequiresAuth(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/identity_proofs", "", 0)
	req.SetPathValue("id", "123")
	h.IdentityProofs(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAccountsHandler_IdentityProofs_InvalidID(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/bad/identity_proofs", "", 1)
	req.SetPathValue("id", "bad")
	h.IdentityProofs(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAccountsHandler_IdentityProofs_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/identity_proofs", "", 1)
	req.SetPathValue("id", "123")
	h.IdentityProofs(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
