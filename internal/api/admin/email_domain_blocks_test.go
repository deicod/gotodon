package admin

import (
	"context"
	"net/http"
	"testing"
)

func TestEmailDomainBlocksHandler_Create_InvalidDomain(t *testing.T) {
	h := NewEmailDomainBlocksHandler(nil, nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", `{"domain":"  "}`)
	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestEmailDomainBlocksHandler_Create_Duplicate(t *testing.T) {
	h := NewEmailDomainBlocksHandler(nil, nil)

	body := `{"domain":"example.com"}`
	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", body)
	h.Create(rec, req)

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", body)
	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestEmailDomainBlocksHandler_NoDB_Unavailable(t *testing.T) {
	h := NewEmailDomainBlocksHandler(nil, nil)

	req, rec := mockAdminRequest(http.MethodGet, "/api/v1/admin/email_domain_blocks", "")
	h.GetAll(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", `{"domain":"example.com"}`)
	h.Create(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAdminRequest(http.MethodGet, "/api/v1/admin/email_domain_blocks/1", "")
	req.SetPathValue("id", "1")
	h.Get(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAdminRequest(http.MethodDelete, "/api/v1/admin/email_domain_blocks/1", "")
	req.SetPathValue("id", "1")
	h.Delete(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestEmailDomainBlockHistory_NoRedis(t *testing.T) {
	history := emailDomainBlockHistory(context.Background(), nil, 42)
	if len(history) != emailDomainHistoryDays {
		t.Fatalf("history len = %d, want %d", len(history), emailDomainHistoryDays)
	}
	for _, entry := range history {
		if entry.Uses != "0" {
			t.Errorf("uses = %q, want 0", entry.Uses)
		}
		if entry.Accounts != "0" {
			t.Errorf("accounts = %q, want 0", entry.Accounts)
		}
	}
}
