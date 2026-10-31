package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type pushSubscriptionStub struct {
	db.Querier
	subscription db.WebPushSubscription
	getErr       error
	updateErr    error
	updateParams db.UpdatePushSubscriptionParams
	createErr    error
	createParams db.CreatePushSubscriptionParams
	createCalled bool
	upsertErr    error
	upsertParams db.UpdatePushSubscriptionDetailsParams
	upsertCalled bool
	deleteParams []pgtype.Int8
}

func (s *pushSubscriptionStub) GetPushSubscription(ctx context.Context, arg db.GetPushSubscriptionParams) (db.WebPushSubscription, error) {
	if s.getErr != nil {
		return db.WebPushSubscription{}, s.getErr
	}
	if !arg.AccessTokenID.Valid || arg.AccessTokenID.Int64 != s.subscription.AccessTokenID.Int64 {
		return db.WebPushSubscription{}, pgx.ErrNoRows
	}
	return s.subscription, nil
}

func (s *pushSubscriptionStub) UpdatePushSubscription(ctx context.Context, arg db.UpdatePushSubscriptionParams) (db.WebPushSubscription, error) {
	s.updateParams = arg
	if s.updateErr != nil {
		return db.WebPushSubscription{}, s.updateErr
	}
	if !arg.AccessTokenID.Valid || arg.AccessTokenID.Int64 != s.subscription.AccessTokenID.Int64 {
		return db.WebPushSubscription{}, pgx.ErrNoRows
	}
	updated := s.subscription
	updated.Data = arg.Data
	s.subscription = updated
	return updated, nil
}

func (s *pushSubscriptionStub) CreatePushSubscription(ctx context.Context, arg db.CreatePushSubscriptionParams) (db.WebPushSubscription, error) {
	s.createCalled = true
	s.createParams = arg
	if s.createErr != nil {
		return db.WebPushSubscription{}, s.createErr
	}
	return s.subscription, nil
}

func (s *pushSubscriptionStub) UpdatePushSubscriptionDetails(ctx context.Context, arg db.UpdatePushSubscriptionDetailsParams) (db.WebPushSubscription, error) {
	s.upsertCalled = true
	s.upsertParams = arg
	if s.upsertErr != nil {
		return db.WebPushSubscription{}, s.upsertErr
	}
	if !arg.AccessTokenID.Valid || arg.AccessTokenID.Int64 != s.subscription.AccessTokenID.Int64 {
		return db.WebPushSubscription{}, pgx.ErrNoRows
	}
	updated := s.subscription
	updated.Endpoint = arg.Endpoint
	updated.KeyP256dh = arg.KeyP256dh
	updated.KeyAuth = arg.KeyAuth
	updated.Data = arg.Data
	updated.UserID = arg.UserID
	s.subscription = updated
	return updated, nil
}

func (s *pushSubscriptionStub) DeletePushSubscription(ctx context.Context, accessTokenID pgtype.Int8) error {
	s.deleteParams = append(s.deleteParams, accessTokenID)
	return nil
}

func mockPushRequest(method, path, body string, accountID, tokenID, userID int64) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := req.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
	ctx = context.WithValue(ctx, middleware.ContextKeyTokenID, tokenID)
	if userID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyUserID, userID)
	}
	rec := httptest.NewRecorder()
	return req.WithContext(ctx), rec
}

func TestPushHandler_Update(t *testing.T) {
	tokenID := int64(99)
	seedData, err := json.Marshal(map[string]any{
		"alerts": map[string]any{
			"follow":  true,
			"mention": false,
		},
	})
	if err != nil {
		t.Fatalf("marshal seed data: %v", err)
	}

	stub := &pushSubscriptionStub{
		subscription: db.WebPushSubscription{
			ID:            1,
			Endpoint:      "https://push.example.com",
			Data:          seedData,
			AccessTokenID: pgtype.Int8{Int64: tokenID, Valid: true},
		},
	}
	cfg := &config.Config{}
	cfg.Secrets.VAPIDPublic = "vapid-test"
	handler := NewPushHandler(stub, cfg)

	body := `{"data":{"alerts":{"mention":true,"favourite":false}}}`
	req, rec := mockPushRequest(http.MethodPut, "/api/v1/push/subscription", body, 123, tokenID, 0)
	handler.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp PushSubscription
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "1" {
		t.Fatalf("id = %q, want %q", resp.ID, "1")
	}
	if resp.Endpoint != "https://push.example.com" {
		t.Fatalf("endpoint = %q, want %q", resp.Endpoint, "https://push.example.com")
	}
	if resp.ServerKey != "vapid-test" {
		t.Fatalf("server_key = %q, want %q", resp.ServerKey, "vapid-test")
	}
	if !resp.Alerts["follow"] {
		t.Fatalf("follow alert = %v, want true", resp.Alerts["follow"])
	}
	if !resp.Alerts["mention"] {
		t.Fatalf("mention alert = %v, want true", resp.Alerts["mention"])
	}
	if value, ok := resp.Alerts["favourite"]; !ok || value {
		t.Fatalf("favourite alert = %v (ok=%v), want false", value, ok)
	}
	if !stub.updateParams.AccessTokenID.Valid || stub.updateParams.AccessTokenID.Int64 != tokenID {
		t.Fatalf("access_token_id = %+v, want %d", stub.updateParams.AccessTokenID, tokenID)
	}
}

func TestPushHandler_Update_NotFound(t *testing.T) {
	stub := &pushSubscriptionStub{getErr: pgx.ErrNoRows}
	cfg := &config.Config{}
	handler := NewPushHandler(stub, cfg)

	body := `{"data":{"alerts":{"follow":true}}}`
	req, rec := mockPushRequest(http.MethodPut, "/api/v1/push/subscription", body, 123, 5, 0)
	handler.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPushHandler_Create_RejectsInvalidEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"http://push.example.com/push",
		"https://localhost/push",
		"https://127.0.0.1:8080/push",
		"https://[fd00::1]/push",
		"https://169.254.169.254/latest/meta-data/",
		"https://user@push.example.com/push",
		"javascript:alert(1)",
	} {
		t.Run(endpoint, func(t *testing.T) {
			stub := &pushSubscriptionStub{upsertErr: pgx.ErrNoRows}
			handler := NewPushHandler(stub, &config.Config{})

			body := `{"subscription":{"endpoint":` + strconv.Quote(endpoint) + `,"keys":{"p256dh":"key","auth":"auth"}},"data":{"alerts":{"follow":true}}}`
			req, rec := mockPushRequest(http.MethodPost, "/api/v1/push/subscription", body, 123, 77, 55)
			handler.Create(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}
			if stub.upsertCalled || stub.createCalled {
				t.Fatalf("upsert = %v, create = %v, want the subscription not stored", stub.upsertCalled, stub.createCalled)
			}
		})
	}
}

func TestPushHandler_Create_NewSubscription(t *testing.T) {
	tokenID := int64(77)
	userID := int64(55)
	stub := &pushSubscriptionStub{
		subscription: db.WebPushSubscription{
			ID:            10,
			Endpoint:      "https://push.example.com",
			AccessTokenID: pgtype.Int8{Int64: tokenID, Valid: true},
			UserID:        pgtype.Int8{Int64: userID, Valid: true},
		},
		upsertErr: pgx.ErrNoRows,
	}
	cfg := &config.Config{}
	cfg.Secrets.VAPIDPublic = "vapid-test"
	handler := NewPushHandler(stub, cfg)

	body := `{"subscription":{"endpoint":"https://push.example.com","keys":{"p256dh":"key","auth":"auth"}},"data":{"alerts":{"follow":true}}}`
	req, rec := mockPushRequest(http.MethodPost, "/api/v1/push/subscription", body, 123, tokenID, userID)
	handler.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.upsertCalled {
		t.Fatalf("expected upsert attempt")
	}
	if !stub.createCalled {
		t.Fatalf("expected create to be called")
	}
	if !stub.createParams.AccessTokenID.Valid || stub.createParams.AccessTokenID.Int64 != tokenID {
		t.Fatalf("access_token_id = %+v, want %d", stub.createParams.AccessTokenID, tokenID)
	}
	if !stub.createParams.UserID.Valid || stub.createParams.UserID.Int64 != userID {
		t.Fatalf("user_id = %+v, want %d", stub.createParams.UserID, userID)
	}
	if stub.createParams.Endpoint != "https://push.example.com" {
		t.Fatalf("endpoint = %q, want %q", stub.createParams.Endpoint, "https://push.example.com")
	}
	if stub.createParams.KeyP256dh.String != "key" {
		t.Fatalf("key_p256dh = %q, want %q", stub.createParams.KeyP256dh.String, "key")
	}
	if stub.createParams.KeyAuth.String != "auth" {
		t.Fatalf("key_auth = %q, want %q", stub.createParams.KeyAuth.String, "auth")
	}

	var resp PushSubscription
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "10" {
		t.Fatalf("id = %q, want %q", resp.ID, "10")
	}
	if resp.ServerKey != "vapid-test" {
		t.Fatalf("server_key = %q, want %q", resp.ServerKey, "vapid-test")
	}
	if !resp.Alerts["follow"] {
		t.Fatalf("follow alert = %v, want true", resp.Alerts["follow"])
	}
}

func TestPushHandler_Create_UpdatesExisting(t *testing.T) {
	tokenID := int64(42)
	userID := int64(21)
	stub := &pushSubscriptionStub{
		subscription: db.WebPushSubscription{
			ID:            3,
			Endpoint:      "https://old.example.com",
			AccessTokenID: pgtype.Int8{Int64: tokenID, Valid: true},
			UserID:        pgtype.Int8{Int64: userID, Valid: true},
		},
	}
	cfg := &config.Config{}
	cfg.Secrets.VAPIDPublic = "vapid-test"
	handler := NewPushHandler(stub, cfg)

	body := `{"subscription":{"endpoint":"https://push.example.com","keys":{"p256dh":"key","auth":"auth"}},"data":{"alerts":{"mention":true}}}`
	req, rec := mockPushRequest(http.MethodPost, "/api/v1/push/subscription", body, 123, tokenID, userID)
	handler.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.upsertCalled {
		t.Fatalf("expected update to be called")
	}
	if stub.createCalled {
		t.Fatalf("did not expect create to be called")
	}
	if stub.upsertParams.Endpoint != "https://push.example.com" {
		t.Fatalf("endpoint = %q, want %q", stub.upsertParams.Endpoint, "https://push.example.com")
	}
	if stub.upsertParams.KeyP256dh.String != "key" {
		t.Fatalf("key_p256dh = %q, want %q", stub.upsertParams.KeyP256dh.String, "key")
	}
	if stub.upsertParams.KeyAuth.String != "auth" {
		t.Fatalf("key_auth = %q, want %q", stub.upsertParams.KeyAuth.String, "auth")
	}

	var resp PushSubscription
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "3" {
		t.Fatalf("id = %q, want %q", resp.ID, "3")
	}
	if !resp.Alerts["mention"] {
		t.Fatalf("mention alert = %v, want true", resp.Alerts["mention"])
	}
}

func TestPushHandler_Get(t *testing.T) {
	tokenID := int64(12)
	seedData, err := json.Marshal(map[string]any{
		"alerts": map[string]any{
			"follow": true,
		},
	})
	if err != nil {
		t.Fatalf("marshal seed data: %v", err)
	}

	stub := &pushSubscriptionStub{
		subscription: db.WebPushSubscription{
			ID:            5,
			Endpoint:      "https://push.example.com",
			Data:          seedData,
			AccessTokenID: pgtype.Int8{Int64: tokenID, Valid: true},
		},
	}
	cfg := &config.Config{}
	cfg.Secrets.VAPIDPublic = "vapid-test"
	handler := NewPushHandler(stub, cfg)

	req, rec := mockPushRequest(http.MethodGet, "/api/v1/push/subscription", "", 123, tokenID, 0)
	handler.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp PushSubscription
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "5" {
		t.Fatalf("id = %q, want %q", resp.ID, "5")
	}
	if resp.Endpoint != "https://push.example.com" {
		t.Fatalf("endpoint = %q, want %q", resp.Endpoint, "https://push.example.com")
	}
	if resp.ServerKey != "vapid-test" {
		t.Fatalf("server_key = %q, want %q", resp.ServerKey, "vapid-test")
	}
	if !resp.Alerts["follow"] {
		t.Fatalf("follow alert = %v, want true", resp.Alerts["follow"])
	}
}

func TestPushHandler_Get_NotFound(t *testing.T) {
	stub := &pushSubscriptionStub{getErr: pgx.ErrNoRows}
	cfg := &config.Config{}
	handler := NewPushHandler(stub, cfg)

	req, rec := mockPushRequest(http.MethodGet, "/api/v1/push/subscription", "", 123, 1, 0)
	handler.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPushHandler_Delete(t *testing.T) {
	stub := &pushSubscriptionStub{}
	handler := NewPushHandler(stub, &config.Config{})

	tokenID := int64(88)
	req, rec := mockPushRequest(http.MethodDelete, "/api/v1/push/subscription", "", 123, tokenID, 0)
	handler.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if len(stub.deleteParams) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(stub.deleteParams))
	}
	if !stub.deleteParams[0].Valid || stub.deleteParams[0].Int64 != tokenID {
		t.Fatalf("access_token_id = %+v, want %d", stub.deleteParams[0], tokenID)
	}
}
