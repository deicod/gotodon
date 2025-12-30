package v1alpha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
)

type memAsyncRefreshStore struct {
	data map[string]AsyncRefreshData
}

func (m *memAsyncRefreshStore) Get(_ context.Context, key string) (AsyncRefreshData, bool, error) {
	value, ok := m.data[key]
	return value, ok, nil
}

func mockAsyncRefreshRequest(method, path string, accountID int64, scopes string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)
	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, scopes)
	}
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	return req, rec
}

func TestAsyncRefreshHandler_Show_Unauthorized(t *testing.T) {
	handler := NewAsyncRefreshHandler(&config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}}, nil)

	req, rec := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/123", 0, "")
	req.SetPathValue("id", "123")

	handler.Show(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAsyncRefreshHandler_Show_Forbidden(t *testing.T) {
	handler := NewAsyncRefreshHandler(&config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}}, nil)

	req, rec := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/123", 123, "write")
	req.SetPathValue("id", "123")

	handler.Show(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAsyncRefreshHandler_Show_NotFound_InvalidToken(t *testing.T) {
	handler := NewAsyncRefreshHandler(&config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}}, nil)

	req, rec := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/bad", 123, "read")
	req.SetPathValue("id", "bad")

	handler.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAsyncRefreshHandler_Show_NotFound_MissingKey(t *testing.T) {
	secret := "secret"
	signer := newAsyncRefreshSigner(secret)
	token, err := signer.Sign("refresh:missing")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	store := &memAsyncRefreshStore{data: map[string]AsyncRefreshData{}}
	handler := NewAsyncRefreshHandler(&config.Config{Secrets: config.SecretsConfig{SecretKeyBase: secret}}, store)

	req, rec := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/"+token, 123, "read")
	req.SetPathValue("id", token)

	handler.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAsyncRefreshHandler_Show_Success(t *testing.T) {
	secret := "secret"
	signer := newAsyncRefreshSigner(secret)
	token, err := signer.Sign("refresh:1")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	resultCount := int64(3)
	store := &memAsyncRefreshStore{
		data: map[string]AsyncRefreshData{
			"refresh:1": {Status: "running", ResultCount: &resultCount},
		},
	}
	handler := NewAsyncRefreshHandler(&config.Config{Secrets: config.SecretsConfig{SecretKeyBase: secret}}, store)

	req, rec := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/"+token, 123, "read")
	req.SetPathValue("id", token)

	handler.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp AsyncRefreshResponse
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AsyncRefresh.ID != token {
		t.Errorf("id = %q, want %q", resp.AsyncRefresh.ID, token)
	}
	if resp.AsyncRefresh.Status != "running" {
		t.Errorf("status = %q, want %q", resp.AsyncRefresh.Status, "running")
	}
	if resp.AsyncRefresh.ResultCount == nil || *resp.AsyncRefresh.ResultCount != resultCount {
		t.Errorf("result_count = %v, want %d", resp.AsyncRefresh.ResultCount, resultCount)
	}
}

func TestAsyncRefreshSigner_RoundTrip(t *testing.T) {
	signer := newAsyncRefreshSigner("secret")
	token, err := signer.Sign("refresh:test")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	key, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if key != "refresh:test" {
		t.Fatalf("key = %q, want %q", key, "refresh:test")
	}
}
