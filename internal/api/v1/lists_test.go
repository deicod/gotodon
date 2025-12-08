package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func mockListRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestListsHandler_Create(t *testing.T) {
	h := NewListsHandler(nil)

	body := `{"title":"My List","replies_policy":"followed"}`
	req, rec := mockListRequest(http.MethodPost, "/api/v1/lists", body)

	h.Create(rec, req)

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
	h := NewListsHandler(nil)

	// Create lists
	req1, rec1 := mockListRequest(http.MethodPost, "/api/v1/lists", `{"title":"List 1"}`)
	h.Create(rec1, req1)
	req2, rec2 := mockListRequest(http.MethodPost, "/api/v1/lists", `{"title":"List 2"}`)
	h.Create(rec2, req2)

	// Get all
	req, rec := mockListRequest(http.MethodGet, "/api/v1/lists", "")
	h.GetAll(rec, req)

	var lists []List
	_ = json.Unmarshal(rec.Body.Bytes(), &lists)

	if len(lists) != 2 {
		t.Errorf("lists length = %d, want 2", len(lists))
	}
}

func TestListsHandler_Delete(t *testing.T) {
	h := NewListsHandler(nil)

	// Create
	req1, rec1 := mockListRequest(http.MethodPost, "/api/v1/lists", `{"title":"ToDelete"}`)
	h.Create(rec1, req1)

	var created List
	_ = json.Unmarshal(rec1.Body.Bytes(), &created)

	// Delete
	req2, rec2 := mockListRequest(http.MethodDelete, "/api/v1/lists/"+created.ID, "")
	req2.SetPathValue("id", created.ID)

	h.Delete(rec2, req2)

	// Verify gone
	req3, rec3 := mockListRequest(http.MethodGet, "/api/v1/lists", "")
	h.GetAll(rec3, req3)
}

func TestListsHandler_Validation(t *testing.T) {
	h := NewListsHandler(nil)

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
			req, rec := mockListRequest(http.MethodPost, "/api/v1/lists", tt.body)
			h.Create(rec, req)

			if tt.wantErr {
				if rec.Code < 400 {
					t.Error("expected error")
				}
			} else {
				if rec.Code >= 400 {
					t.Errorf("unexpected error code: %d", rec.Code)
				}
			}
		})
	}
}
