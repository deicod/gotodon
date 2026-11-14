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

// TestAsyncRefreshHandler_Show_Scopes routes requests through RegisterRoutes:
// Mastodon's Api::V1Alpha::AsyncRefreshesController accepts only read itself,
// so neither another scope, one of read's children nor a token without
// scopes reaches the handler.
func TestAsyncRefreshHandler_Show_Scopes(t *testing.T) {
	handler := NewAsyncRefreshHandler(&config.Config{Secrets: config.SecretsConfig{SecretKeyBase: "secret"}}, nil)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	tests := []struct {
		scopes  string
		allowed bool
	}{
		{"read", true},
		{"read write", true},
		{"write", false},
		{"read:statuses read:accounts", false},
		{"follow push", false},
		{"", false},
	}
	for _, tt := range tests {
		req, _ := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/bad", 123, tt.scopes)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if tt.allowed {
			// The handler runs and rejects the malformed id.
			if rec.Code != http.StatusNotFound {
				t.Errorf("scopes %q: status = %d, want %d", tt.scopes, rec.Code, http.StatusNotFound)
			}
			continue
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("scopes %q: status = %d, want %d", tt.scopes, rec.Code, http.StatusForbidden)
			continue
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body["error"] != middleware.OutsideScopesMessage {
			t.Errorf("scopes %q: error = %q, want %q", tt.scopes, body["error"], middleware.OutsideScopesMessage)
		}
	}

	req, _ := mockAsyncRefreshRequest(http.MethodGet, "/api/v1_alpha/async_refreshes/bad", 0, "")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
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
