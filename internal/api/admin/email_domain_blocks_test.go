package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestEmailDomainBlocksHandler_MockMode_CRUD(t *testing.T) {
	h := NewEmailDomainBlocksHandler(nil, nil)

	createBody := `{"domain":"example.com","allow_with_approval":true}`
	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", createBody)
	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d", rec.Code, http.StatusOK)
	}

	var created EmailDomainBlock
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Domain != "example.com" {
		t.Errorf("domain = %q, want %q", created.Domain, "example.com")
	}
	if !created.AllowWithApproval {
		t.Errorf("allow_with_approval = %v, want true", created.AllowWithApproval)
	}
	if len(created.History) != emailDomainHistoryDays {
		t.Errorf("history len = %d, want %d", len(created.History), emailDomainHistoryDays)
	}

	req, rec = mockAdminRequest(http.MethodGet, "/api/v1/admin/email_domain_blocks", "")
	h.GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", rec.Code, http.StatusOK)
	}

	var blocks []EmailDomainBlock
	if err := json.Unmarshal(rec.Body.Bytes(), &blocks); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("list len = %d, want 1", len(blocks))
	}

	req, rec = mockAdminRequest(http.MethodGet, "/api/v1/admin/email_domain_blocks/"+created.ID, "")
	req.SetPathValue("id", created.ID)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockAdminRequest(http.MethodDelete, "/api/v1/admin/email_domain_blocks/"+created.ID, "")
	req.SetPathValue("id", created.ID)
	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockAdminRequest(http.MethodGet, "/api/v1/admin/email_domain_blocks", "")
	h.GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list after delete status = %d, want %d", rec.Code, http.StatusOK)
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &blocks); err != nil {
		t.Fatalf("decode list after delete response: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("list after delete len = %d, want 0", len(blocks))
	}
}

func TestEmailDomainBlocksHandler_Create_InvalidDomain(t *testing.T) {
	h := NewEmailDomainBlocksHandler(nil, nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", `{"domain":"  "}`)
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestEmailDomainBlocksHandler_Create_Duplicate(t *testing.T) {
	h := NewEmailDomainBlocksHandler(nil, nil)

	body := `{"domain":"example.com"}`
	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", body)
	h.Create(rec, req)

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/email_domain_blocks", body)
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
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
