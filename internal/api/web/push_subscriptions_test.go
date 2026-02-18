package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubWebPushStore struct {
	nextID int64
	subs   map[int64]db.WebPushSubscription
}

func newStubWebPushStore() *stubWebPushStore {
	return &stubWebPushStore{nextID: 1, subs: make(map[int64]db.WebPushSubscription)}
}

func (s *stubWebPushStore) CreatePushSubscription(_ context.Context, arg db.CreatePushSubscriptionParams) (db.WebPushSubscription, error) {
	id := s.nextID
	s.nextID++
	sub := db.WebPushSubscription{
		ID:            id,
		Endpoint:      arg.Endpoint,
		KeyP256dh:     arg.KeyP256dh,
		KeyAuth:       arg.KeyAuth,
		Data:          arg.Data,
		AccessTokenID: arg.AccessTokenID,
		UserID:        arg.UserID,
		Standard:      arg.Standard,
	}
	s.subs[id] = sub
	return sub, nil
}

func (s *stubWebPushStore) DeletePushSubscription(_ context.Context, accessTokenID pgtype.Int8) error {
	if !accessTokenID.Valid {
		return nil
	}
	for id, sub := range s.subs {
		if sub.AccessTokenID.Valid && sub.AccessTokenID.Int64 == accessTokenID.Int64 {
			delete(s.subs, id)
		}
	}
	return nil
}

func (s *stubWebPushStore) DeletePushSubscriptionByID(_ context.Context, id int64) error {
	delete(s.subs, id)
	return nil
}

func (s *stubWebPushStore) GetPushSubscriptionByID(_ context.Context, id int64) (db.WebPushSubscription, error) {
	if sub, ok := s.subs[id]; ok {
		return sub, nil
	}
	return db.WebPushSubscription{}, pgx.ErrNoRows
}

func (s *stubWebPushStore) UpdatePushSubscriptionByID(_ context.Context, arg db.UpdatePushSubscriptionByIDParams) (db.WebPushSubscription, error) {
	sub, ok := s.subs[arg.ID]
	if !ok {
		return db.WebPushSubscription{}, pgx.ErrNoRows
	}
	sub.Data = arg.Data
	s.subs[arg.ID] = sub
	return sub, nil
}

func TestPushSubscriptionsHandler_Create_InvalidNestedParams(t *testing.T) {
	store := newStubWebPushStore()
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test"}})

	req := httptest.NewRequest(http.MethodPost, "/api/web/push_subscriptions", strings.NewReader(`{"subscription":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPushSubscriptionsHandler_Create_Valid(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{ID: 1, AccessTokenID: pgtype.Int8{Int64: 5, Valid: true}}
	store.nextID = 2

	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test", VAPIDPublic: "vapid"}})

	body := `{"subscription":{"endpoint":"https://push.example.com","keys":{"p256dh":"key","auth":"auth"},"standard":"1"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/web/push_subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(store.subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(store.subs))
	}
	for _, sub := range store.subs {
		if sub.Endpoint != "https://push.example.com" {
			t.Fatalf("endpoint = %q, want https://push.example.com", sub.Endpoint)
		}
		if !sub.Standard {
			t.Fatalf("standard = false, want true")
		}
	}
}

func TestPushSubscriptionsHandler_Create_HTTPRejected(t *testing.T) {
	store := newStubWebPushStore()
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test"}})

	body := `{"subscription":{"endpoint":"http://push.example.com","keys":{"p256dh":"key","auth":"auth"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/web/push_subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestPushSubscriptionsHandler_Create_HTTPLocalhostAllowed(t *testing.T) {
	store := newStubWebPushStore()
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test"}})

	body := `{"subscription":{"endpoint":"http://localhost:1234","keys":{"p256dh":"key","auth":"auth"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/web/push_subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(store.subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(store.subs))
	}
}

func TestPushSubscriptionsHandler_Create_WithAlerts(t *testing.T) {
	store := newStubWebPushStore()
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test", VAPIDPublic: "vapid"}})

	body := `{"subscription":{"endpoint":"https://push.example.com","keys":{"p256dh":"key","auth":"auth"}},"data":{"policy":"all","alerts":{"follow":true,"reblog":true}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/web/push_subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(store.subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(store.subs))
	}
	for _, sub := range store.subs {
		data := webPushDataFromBytes(sub.Data)
		if data.Policy != "all" {
			t.Fatalf("policy = %q, want all", data.Policy)
		}
		if !data.Alerts["follow"] || !data.Alerts["reblog"] {
			t.Fatalf("alerts not applied: %+v", data.Alerts)
		}
	}
}

func TestPushSubscriptionsHandler_Update_InvalidNestedParams(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{ID: 1}
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test"}})

	req := httptest.NewRequest(http.MethodPut, "/api/web/push_subscriptions/1", strings.NewReader(`{"data":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPushSubscriptionsHandler_Update_Valid(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{
		ID:     1,
		Data:   []byte(`{"policy":"all","alerts":{"follow":false}}`),
		UserID: pgtype.Int8{Int64: 10, Valid: true},
	}
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test"}})

	body := `{"data":{"policy":"all","alerts":{"follow":true}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/web/push_subscriptions/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	updated := store.subs[1]
	data := webPushDataFromBytes(updated.Data)
	if !data.Alerts["follow"] {
		t.Fatalf("alert follow = false, want true")
	}
}

func TestPushSubscriptionsHandler_Update_WrongUser(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{
		ID:     1,
		Data:   []byte(`{"policy":"all","alerts":{"follow":false}}`),
		UserID: pgtype.Int8{Int64: 11, Valid: true},
	}
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test"}})

	body := `{"data":{"policy":"all","alerts":{"follow":true}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/web/push_subscriptions/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPushSubscriptionsHandler_Delete_Token(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{ID: 1}

	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}})
	fixed := time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return fixed }

	token, err := buildWebPushToken("secret", 1, fixed)
	if err != nil {
		t.Fatalf("token build: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/web/push_subscriptions/"+token, nil)
	req.SetPathValue("id", token)

	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(store.subs) != 0 {
		t.Fatalf("subscriptions = %d, want 0", len(store.subs))
	}
}

func TestPushSubscriptionsHandler_Delete_FutureToken(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{ID: 1}

	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}})
	fixed := time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return fixed }

	token, err := buildWebPushToken("secret", 1, fixed.Add(time.Hour))
	if err != nil {
		t.Fatalf("token build: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/web/push_subscriptions/"+token, nil)
	req.SetPathValue("id", token)

	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(store.subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(store.subs))
	}
}

func TestPushSubscriptionsHandler_Delete_InvalidToken(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{ID: 1}

	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}})
	req := httptest.NewRequest(http.MethodDelete, "/api/web/push_subscriptions/invalid", nil)
	req.SetPathValue("id", "invalid")

	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(store.subs) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(store.subs))
	}
}

func TestPushSubscriptionsHandler_Update_ResponseAlerts(t *testing.T) {
	store := newStubWebPushStore()
	store.subs[1] = db.WebPushSubscription{
		ID:     1,
		Data:   []byte(`{"policy":"all","alerts":{"follow":true}}`),
		UserID: pgtype.Int8{Int64: 10, Valid: true},
	}
	h := NewPushSubscriptionsHandler(store, &config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "test", VAPIDPublic: "vapid"}})

	body := `{"data":{"alerts":{"follow":true}}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/web/push_subscriptions/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(99))
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, int64(5))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp webPushSubscriptionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ServerKey != "vapid" {
		t.Fatalf("server_key = %q, want vapid", resp.ServerKey)
	}
}
