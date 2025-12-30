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

func mockFilterRequest(method, path, body string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func TestFiltersHandler_Create_MockMode(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	body := `{"phrase":"spoilers","context":["home"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)

	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var filter FilterV1
	if err := json.Unmarshal(rec.Body.Bytes(), &filter); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if filter.Phrase != "spoilers" {
		t.Errorf("phrase = %q, want %q", filter.Phrase, "spoilers")
	}
	if len(filter.Context) != 1 || filter.Context[0] != "home" {
		t.Errorf("context = %v, want [home]", filter.Context)
	}
	if !filter.WholeWord {
		t.Errorf("whole_word = %v, want true", filter.WholeWord)
	}
}

func TestFiltersHandler_List_MockMode(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	body := `{"phrase":"one","context":["home"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)
	h.Create(rec, req)

	body = `{"phrase":"two","context":["public"]}`
	req, rec = mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)
	h.Create(rec, req)

	req, rec = mockFilterRequest(http.MethodGet, "/api/v1/filters", "", 12345)
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var filters []FilterV1
	if err := json.Unmarshal(rec.Body.Bytes(), &filters); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(filters) != 2 {
		t.Fatalf("filters length = %d, want 2", len(filters))
	}
}

func TestFiltersHandler_Get_Update_Delete_MockMode(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	body := `{"phrase":"initial","context":["home"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)
	h.Create(rec, req)

	var created FilterV1
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	req, rec = mockFilterRequest(http.MethodGet, "/api/v1/filters/"+created.ID, "", 12345)
	req.SetPathValue("id", created.ID)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	updateBody := `{"phrase":"updated","context":["public"],"whole_word":false,"irreversible":true,"expires_in":3600}`
	req, rec = mockFilterRequest(http.MethodPut, "/api/v1/filters/"+created.ID, updateBody, 12345)
	req.SetPathValue("id", created.ID)
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var updated FilterV1
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if updated.Phrase != "updated" {
		t.Errorf("phrase = %q, want %q", updated.Phrase, "updated")
	}
	if len(updated.Context) != 1 || updated.Context[0] != "public" {
		t.Errorf("context = %v, want [public]", updated.Context)
	}
	if updated.WholeWord {
		t.Errorf("whole_word = %v, want false", updated.WholeWord)
	}
	if !updated.Irreversible {
		t.Errorf("irreversible = %v, want true", updated.Irreversible)
	}
	if updated.ExpiresAt == nil {
		t.Errorf("expires_at = nil, want value")
	}

	req, rec = mockFilterRequest(http.MethodDelete, "/api/v1/filters/"+created.ID, "", 12345)
	req.SetPathValue("id", created.ID)
	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", rec.Code, http.StatusOK)
	}

	req, rec = mockFilterRequest(http.MethodGet, "/api/v1/filters", "", 12345)
	h.List(rec, req)

	var filters []FilterV1
	if err := json.Unmarshal(rec.Body.Bytes(), &filters); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(filters) != 0 {
		t.Fatalf("filters length = %d, want 0", len(filters))
	}
}

func TestFiltersHandler_Create_InvalidContext(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	body := `{"phrase":"test","context":["nope"]}`
	req, rec := mockFilterRequest(http.MethodPost, "/api/v1/filters", body, 12345)
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestFiltersHandler_List_Unauthorized(t *testing.T) {
	h := NewFiltersHandler(nil, nil)

	req, rec := mockFilterRequest(http.MethodGet, "/api/v1/filters", "", 0)
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
