package v1

import (
	"context"
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

func TestListsHandler_Create_DBUnavailable(t *testing.T) {
	h := NewListsHandler(nil, nil)

	body := `{"title":"My List","replies_policy":"followed"}`
	req, rec := mockListRequest(http.MethodPost, "/api/v1/lists", body)

	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestListsHandler_GetAll_DBUnavailable(t *testing.T) {
	h := NewListsHandler(nil, nil)

	req, rec := mockListRequest(http.MethodGet, "/api/v1/lists", "")
	h.GetAll(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestListsHandler_Delete_DBUnavailable(t *testing.T) {
	h := NewListsHandler(nil, nil)

	req, rec := mockListRequest(http.MethodDelete, "/api/v1/lists/123", "")
	req.SetPathValue("id", "123")

	h.Delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestListsHandler_Validation(t *testing.T) {
	h := NewListsHandler(nil, nil)

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
				if rec.Code != http.StatusUnprocessableEntity {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
			}
		})
	}
}

func TestListsHandler_GetListsForAccount_DBUnavailable(t *testing.T) {
	h := NewListsHandler(nil, nil)

	req, rec := mockListRequest(http.MethodGet, "/api/v1/accounts/777/lists", "")
	req.SetPathValue("id", "777")
	h.GetListsForAccount(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
