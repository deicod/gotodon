package v1

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAccountsHandler_FeaturedTags_MockMode(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/123/featured_tags", "", 0)
	req.SetPathValue("id", "123")
	h.FeaturedTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var tags []FeaturedTag
	if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags length = %d, want 0", len(tags))
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
