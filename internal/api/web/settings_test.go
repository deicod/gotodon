package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

type stubWebSettingsStore struct {
	called   bool
	lastData []byte
	lastUser int64
	retErr   error
}

func (s *stubWebSettingsStore) UpsertWebSetting(_ context.Context, arg db.UpsertWebSettingParams) (db.WebSetting, error) {
	s.called = true
	s.lastData = arg.Data
	s.lastUser = arg.UserID
	if s.retErr != nil {
		return db.WebSetting{}, s.retErr
	}
	return db.WebSetting{ID: 1, UserID: arg.UserID, Data: arg.Data}, nil
}

func TestSettingsHandler_Update_SignedIn(t *testing.T) {
	store := &stubWebSettingsStore{}
	h := NewSettingsHandler(store)
	body := `{"data":{"onboarded":true}}`

	req := httptest.NewRequest(http.MethodPatch, "/api/web/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(42))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !store.called {
		t.Fatalf("expected UpsertWebSetting to be called")
	}
	if store.lastUser != 42 {
		t.Fatalf("user_id = %d, want %d", store.lastUser, 42)
	}

	var payload map[string]any
	if err := json.Unmarshal(store.lastData, &payload); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if value, ok := payload["onboarded"].(bool); !ok || !value {
		t.Fatalf("onboarded = %v, want true", payload["onboarded"])
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestSettingsHandler_Update_Unsigned(t *testing.T) {
	store := &stubWebSettingsStore{}
	h := NewSettingsHandler(store)
	body := `{"data":{"onboarded":true}}`

	req := httptest.NewRequest(http.MethodPatch, "/api/web/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if store.called {
		t.Fatalf("expected no DB writes when unsigned")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}
