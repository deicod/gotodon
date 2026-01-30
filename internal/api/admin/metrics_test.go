package admin

import (
	"net/http"
	"testing"
)

func TestDimensionsHandler_GetWithNilQueries(t *testing.T) {
	handler := NewDimensionsHandler(nil, nil, nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/dimensions", `{"keys":["languages"]}`)
	handler.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestMeasuresHandler_GetWithNilQueries(t *testing.T) {
	handler := NewMeasuresHandler(nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/measures", `{"keys":["active_users"]}`)
	handler.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestRetentionHandler_GetWithNilQueries(t *testing.T) {
	handler := NewRetentionHandler(nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/retention", `{}`)
	handler.Get(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
