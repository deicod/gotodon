package v2

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSuggestionsHandler_Get_MockMode(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)

	req, rec := mockAccountRequest(http.MethodGet, "/api/v2/suggestions", "")
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp []SuggestionV2
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("len = %d, want 0", len(resp))
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

func TestSuggestionsHandler_Delete_MockMode(t *testing.T) {
	h := NewSuggestionsHandler(nil, nil)

	req, rec := mockAccountRequest(http.MethodDelete, "/api/v2/suggestions/123", "")
	req.SetPathValue("account_id", "123")
	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
