package v2

import (
	"net/http"
	"testing"
)

func TestSuggestionsHandler_Get_NoDB(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)

	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/suggestions", "")
	h.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestSuggestionsHandler_Delete_InvalidAccountID(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)

	req, rec := mockAccountRequest(http.MethodDelete, "/api/v2/suggestions/bad", "")
	req.SetPathValue("account_id", "bad")
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSuggestionsHandler_Delete_NoDB(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)

	req, rec := mockAccountRequest(http.MethodDelete, "/api/v2/suggestions/123", "")
	req.SetPathValue("account_id", "123")
	h.Delete(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
