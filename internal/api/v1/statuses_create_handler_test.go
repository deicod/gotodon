package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
)

func TestStatusesHandlerCreateRejectsPastScheduledAt(t *testing.T) {
	h := NewStatusesHandler(nil, nil, nil, nil, nil)
	payload := map[string]any{
		"status":       "hello",
		"scheduled_at": time.Now().Add(-1 * time.Minute).Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/statuses", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write:statuses")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}
