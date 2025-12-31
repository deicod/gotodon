package v1

import (
	"encoding/json"
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

func TestSuggestionsHandler_Get_NoDatabase(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodGet, "/api/v1/suggestions", "")

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var accounts []Account
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected empty suggestions, got %d", len(accounts))
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

func TestSuggestionsHandler_Delete_NoDatabase(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)
	req, rec := mockAccountRequest(http.MethodDelete, "/api/v1/suggestions/123", "")
	req.SetPathValue("account_id", "123")

	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
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
