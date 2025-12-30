package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestStatusesHandler_Index_NoIDs_MockMode(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/statuses", nil)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var statuses []Status
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("statuses length = %d, want 0", len(statuses))
	}
}

func TestStatusesHandler_Index_TooManyIDs(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	values := url.Values{}
	for i := 0; i < defaultStatusesLimit+1; i++ {
		values.Add("id[]", strconv.Itoa(i+1))
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/statuses?"+values.Encode(), nil)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestStatusesHandler_Index_RequiresReadScopeWhenTokenPresent(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/statuses", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStatusesHandler_Index_AllowsReadScope(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/statuses", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyScopes, "read:statuses")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
