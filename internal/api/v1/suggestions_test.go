package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSuggestionsHandler_Get_RequiresAuth(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/suggestions", nil)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSuggestionsHandler_Get_DBUnavailable(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v1/suggestions", "")

	h.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestSuggestionsHandler_Delete_RequiresAuth(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/suggestions/123", nil)
	req.SetPathValue("account_id", "123")
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSuggestionsHandler_Delete_DBUnavailable(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodDelete, "/api/v1/suggestions/123", "")
	req.SetPathValue("account_id", "123")

	h.Delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestSuggestionsHandler_Delete_InvalidID(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodDelete, "/api/v1/suggestions/bad", "")
	req.SetPathValue("account_id", "bad")

	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
