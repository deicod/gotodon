package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func mockListContext(e *echo.Echo, method, path string, body string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("account_id", int64(12345))
	c.Set("scopes", "read write")
	return c, rec
}

func TestListsHandler_Create(t *testing.T) {
	e := echo.New()
	h := NewListsHandler()

	body := `{"title":"My List","replies_policy":"followed"}`
	c, rec := mockListContext(e, http.MethodPost, "/api/v1/lists", body)

	err := h.Create(c)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var list List
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if list.Title != "My List" {
		t.Errorf("Title = %q, want %q", list.Title, "My List")
	}

	if list.RepliesPolicy != "followed" {
		t.Errorf("RepliesPolicy = %q, want %q", list.RepliesPolicy, "followed")
	}
}

func TestListsHandler_GetAll(t *testing.T) {
	e := echo.New()
	h := NewListsHandler()

	// Create lists
	c1, _ := mockListContext(e, http.MethodPost, "/api/v1/lists", `{"title":"List 1"}`)
	_ = h.Create(c1)
	c2, _ := mockListContext(e, http.MethodPost, "/api/v1/lists", `{"title":"List 2"}`)
	_ = h.Create(c2)

	// Get all
	c, rec := mockListContext(e, http.MethodGet, "/api/v1/lists", "")
	err := h.GetAll(c)
	if err != nil {
		t.Fatalf("GetAll() error = %v", err)
	}

	var lists []List
	_ = json.Unmarshal(rec.Body.Bytes(), &lists)

	if len(lists) != 2 {
		t.Errorf("lists length = %d, want 2", len(lists))
	}
}

func TestListsHandler_Delete(t *testing.T) {
	e := echo.New()
	h := NewListsHandler()

	// Create
	c1, rec1 := mockListContext(e, http.MethodPost, "/api/v1/lists", `{"title":"ToDelete"}`)
	_ = h.Create(c1)

	var created List
	_ = json.Unmarshal(rec1.Body.Bytes(), &created)

	// Delete
	c2, _ := mockListContext(e, http.MethodDelete, "/api/v1/lists/"+created.ID, "")
	c2.SetParamNames("id")
	c2.SetParamValues(created.ID)

	err := h.Delete(c2)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify gone
	c3, _ := mockListContext(e, http.MethodGet, "/api/v1/lists", "")
	_ = h.GetAll(c3)
}

func TestListsHandler_Validation(t *testing.T) {
	e := echo.New()
	h := NewListsHandler()

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"empty title", `{"title":""}`, true},
		{"invalid policy", `{"title":"Test","replies_policy":"invalid"}`, true},
		{"valid", `{"title":"Test","replies_policy":"list"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := mockListContext(e, http.MethodPost, "/api/v1/lists", tt.body)
			err := h.Create(c)

			if tt.wantErr {
				if err == nil && rec.Code < 400 {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
