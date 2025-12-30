package v1

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDomainBlocksPreview_MissingDomain(t *testing.T) {
	h := NewDomainBlocksHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v1/domain_blocks/preview", "")

	h.Preview(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDomainBlocksPreview_InvalidDomain(t *testing.T) {
	h := NewDomainBlocksHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v1/domain_blocks/preview?domain=invalid_domain", "")

	h.Preview(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestDomainBlocksPreview_NoDatabase(t *testing.T) {
	h := NewDomainBlocksHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v1/domain_blocks/preview?domain=example.org", "")

	h.Preview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp DomainBlockPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.FollowersCount != 0 || resp.FollowingCount != 0 {
		t.Fatalf("counts = %d/%d, want 0/0", resp.FollowingCount, resp.FollowersCount)
	}
}
