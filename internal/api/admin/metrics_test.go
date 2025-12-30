package admin

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDimensionsHandler_GetWithNilQueries(t *testing.T) {
	handler := NewDimensionsHandler(nil, nil, nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/dimensions", `{"keys":["languages"]}`)
	handler.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp []dimensionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("response length = %d, want 0", len(resp))
	}
}

func TestMeasuresHandler_GetWithNilQueries(t *testing.T) {
	handler := NewMeasuresHandler(nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/measures", `{"keys":["active_users"]}`)
	handler.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp []measureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("response length = %d, want 0", len(resp))
	}
}

func TestRetentionHandler_GetWithNilQueries(t *testing.T) {
	handler := NewRetentionHandler(nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/retention", `{}`)
	handler.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp []retentionCohort
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp) != 0 {
		t.Fatalf("response length = %d, want 0", len(resp))
	}
}
