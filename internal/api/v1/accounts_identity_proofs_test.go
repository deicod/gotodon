package v1

import (
	"encoding/json"
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

func TestAccountsHandler_IdentityProofs_MockMode(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/identity_proofs", "", 1)
	req.SetPathValue("id", "123")
	h.IdentityProofs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var proofs []any
	if err := json.Unmarshal(rec.Body.Bytes(), &proofs); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(proofs) != 0 {
		t.Fatalf("proofs length = %d, want 0", len(proofs))
	}
}
