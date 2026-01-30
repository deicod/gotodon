package v1

import (
	"net/http"
	"testing"
)

func TestAccountsHandler_FeaturedTags_DBUnavailable(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/featured_tags", "", 0)
	req.SetPathValue("id", "123")
	h.FeaturedTags(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestAccountsHandler_FeaturedTags_InvalidID(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/bad/featured_tags", "", 0)
	req.SetPathValue("id", "bad")
	h.FeaturedTags(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
