package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func mockAdminContext(e *echo.Echo, method, path string, body string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("role", "admin")
	c.Set("scopes", "admin:read admin:write")
	return c, rec
}

func TestAccountsHandler_GetAll(t *testing.T) {
	e := echo.New()
	h := NewAccountsHandler()

	c, rec := mockAdminContext(e, http.MethodGet, "/api/v1/admin/accounts", "")

	err := h.GetAll(c)
	if err != nil {
		t.Fatalf("GetAll() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_RequireAdmin(t *testing.T) {
	e := echo.New()
	h := NewAccountsHandler()

	// Without admin role
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("role", "user") // Not admin

	err := h.GetAll(c)
	if err == nil {
		t.Error("Expected error for non-admin")
	}
}

func TestReportsHandler_GetAll(t *testing.T) {
	e := echo.New()
	h := NewReportsHandler()

	c, rec := mockAdminContext(e, http.MethodGet, "/api/v1/admin/reports", "")

	err := h.GetAll(c)
	if err != nil {
		t.Fatalf("GetAll() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestDomainBlocksHandler_CRUD(t *testing.T) {
	e := echo.New()
	h := NewDomainBlocksHandler()

	// Create
	c1, rec1 := mockAdminContext(e, http.MethodPost, "/api/v1/admin/domain_blocks", `{"domain":"spam.example.com","severity":"suspend"}`)
	err := h.Create(c1)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if rec1.Code != http.StatusOK {
		t.Errorf("Create status = %d, want %d", rec1.Code, http.StatusOK)
	}

	var block DomainBlock
	_ = json.Unmarshal(rec1.Body.Bytes(), &block)

	if block.Domain != "spam.example.com" {
		t.Errorf("Domain = %q, want %q", block.Domain, "spam.example.com")
	}

	if block.Severity != "suspend" {
		t.Errorf("Severity = %q, want %q", block.Severity, "suspend")
	}

	// GetAll
	c2, rec2 := mockAdminContext(e, http.MethodGet, "/api/v1/admin/domain_blocks", "")
	_ = h.GetAll(c2)

	var blocks []DomainBlock
	_ = json.Unmarshal(rec2.Body.Bytes(), &blocks)

	if len(blocks) != 1 {
		t.Errorf("blocks length = %d, want 1", len(blocks))
	}
}

func TestInstanceHandler_GetUpdate(t *testing.T) {
	e := echo.New()
	h := NewInstanceHandler()

	// Get
	c1, rec1 := mockAdminContext(e, http.MethodGet, "/api/v1/admin/instance", "")
	err := h.Get(c1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if rec1.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec1.Code, http.StatusOK)
	}

	var config InstanceConfig
	_ = json.Unmarshal(rec1.Body.Bytes(), &config)

	if config.Title != "Gotodon" {
		t.Errorf("Title = %q, want %q", config.Title, "Gotodon")
	}

	// Update
	c2, rec2 := mockAdminContext(e, http.MethodPatch, "/api/v1/admin/instance", `{"title":"My Instance"}`)
	err = h.Update(c2)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	var updated InstanceConfig
	_ = json.Unmarshal(rec2.Body.Bytes(), &updated)

	if updated.Title != "My Instance" {
		t.Errorf("Updated Title = %q, want %q", updated.Title, "My Instance")
	}
}
