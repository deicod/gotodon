package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	pkgoauth "github.com/deicod/gotodon/pkg/oauth"
)

type stubAppsQueries struct {
	app db.OauthApplication
	err error
}

type appsTestTokenValidator struct {
	err error
}

func (s appsTestTokenValidator) ValidateToken(_ context.Context, _ string) (*middleware.TokenInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &middleware.TokenInfo{TokenID: 1, AppID: 1, Scopes: "read"}, nil
}

func (s *stubAppsQueries) CreateOAuthApplication(_ context.Context, _ db.CreateOAuthApplicationParams) (db.OauthApplication, error) {
	return db.OauthApplication{}, nil
}

func (s *stubAppsQueries) GetOAuthApplication(_ context.Context, _ int64) (db.OauthApplication, error) {
	if s.err != nil {
		return db.OauthApplication{}, s.err
	}
	return s.app, nil
}

func TestAppsHandler_VerifyCredentials_NoAppContextReturnsUnauthorized(t *testing.T) {
	h := NewAppsHandler(nil, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var errResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if errResp["error"] != "The access token is invalid" {
		t.Fatalf("error = %v, want %q", errResp["error"], "The access token is invalid")
	}
}

func TestAppsHandler_VerifyCredentials_SuccessPayloadShape(t *testing.T) {
	stub := &stubAppsQueries{
		app: db.OauthApplication{
			ID:          42,
			Name:        "Parity App",
			Scopes:      "read write:statuses",
			RedirectUri: "https://example.com/callback\n https://example.com/alt\t",
			Website: pgtype.Text{
				String: "   ",
				Valid:  true,
			},
		},
	}

	cfg := &config.Config{}
	cfg.Secrets.VAPIDPublic = "test-vapid-key"
	h := NewAppsHandler(stub, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAppID, int64(42))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got := resp["id"]; got != "42" {
		t.Fatalf("id = %v, want %q", got, "42")
	}
	if got := resp["name"]; got != "Parity App" {
		t.Fatalf("name = %v, want %q", got, "Parity App")
	}
	if got := resp["website"]; got != nil {
		t.Fatalf("website = %v, want nil", got)
	}
	if got := resp["vapid_key"]; got != "test-vapid-key" {
		t.Fatalf("vapid_key = %v, want %q", got, "test-vapid-key")
	}

	scopes, ok := resp["scopes"].([]any)
	if !ok {
		t.Fatalf("scopes type = %T, want []any", resp["scopes"])
	}
	if len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "write:statuses" {
		t.Fatalf("scopes = %#v, want [read write:statuses]", scopes)
	}

	redirectURIs, ok := resp["redirect_uris"].([]any)
	if !ok {
		t.Fatalf("redirect_uris type = %T, want []any", resp["redirect_uris"])
	}
	if len(redirectURIs) != 2 || redirectURIs[0] != "https://example.com/callback" || redirectURIs[1] != "https://example.com/alt" {
		t.Fatalf("redirect_uris = %#v, want [https://example.com/callback https://example.com/alt]", redirectURIs)
	}
	if got := resp["redirect_uri"]; got != "https://example.com/callback" {
		t.Fatalf("redirect_uri = %v, want %q", got, "https://example.com/callback")
	}

	if _, ok := resp["client_id"]; ok {
		t.Fatalf("client_id must be omitted, got %v", resp["client_id"])
	}
	if _, ok := resp["client_secret"]; ok {
		t.Fatalf("client_secret must be omitted, got %v", resp["client_secret"])
	}
}

func TestAppsHandler_VerifyCredentials_AppContextWithoutReadScopeStillSucceeds(t *testing.T) {
	stub := &stubAppsQueries{
		app: db.OauthApplication{
			ID:          7,
			Name:        "Writer App",
			Scopes:      "write",
			RedirectUri: "https://example.com/callback",
		},
	}
	h := NewAppsHandler(stub, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAppID, int64(7))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAppsHandler_VerifyCredentials_AppLessTokenUsesCompatibilityResponse(t *testing.T) {
	cfg := &config.Config{}
	cfg.Secrets.VAPIDPublic = "test-vapid-key"
	h := NewAppsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAppID, int64(0))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write follow push")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got := resp["id"]; got != "0" {
		t.Fatalf("id = %v, want %q", got, "0")
	}
	if got := resp["name"]; got != "Test App" {
		t.Fatalf("name = %v, want %q", got, "Test App")
	}
	if got := resp["website"]; got != nil {
		t.Fatalf("website = %v, want nil", got)
	}
	if got := resp["redirect_uri"]; got != "" {
		t.Fatalf("redirect_uri = %v, want empty string", got)
	}
	if got := resp["vapid_key"]; got != "test-vapid-key" {
		t.Fatalf("vapid_key = %v, want %q", got, "test-vapid-key")
	}

	scopes, ok := resp["scopes"].([]any)
	if !ok {
		t.Fatalf("scopes type = %T, want []any", resp["scopes"])
	}
	if len(scopes) != 4 || scopes[0] != "read" || scopes[1] != "write" || scopes[2] != "follow" || scopes[3] != "push" {
		t.Fatalf("scopes = %#v, want [read write follow push]", scopes)
	}

	redirectURIs, ok := resp["redirect_uris"].([]any)
	if !ok {
		t.Fatalf("redirect_uris type = %T, want []any", resp["redirect_uris"])
	}
	if len(redirectURIs) != 0 {
		t.Fatalf("redirect_uris = %#v, want empty array", redirectURIs)
	}
}

func TestAppsHandler_VerifyCredentials_InvalidAppIDReturnsUnauthorized(t *testing.T) {
	stub := &stubAppsQueries{err: pgx.ErrNoRows}
	h := NewAppsHandler(stub, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAppID, int64(123))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var errResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if errResp["error"] != "The access token is invalid" {
		t.Fatalf("error = %v, want %q", errResp["error"], "The access token is invalid")
	}
}

func TestAppsHandler_VerifyCredentials_RequireAuthRevokedTokenMessage(t *testing.T) {
	h := NewAppsHandler(nil, &config.Config{})
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/apps/verify_credentials",
		middleware.RequireAuth(appsTestTokenValidator{err: pkgoauth.ErrRevokedToken})(http.HandlerFunc(h.VerifyCredentials)),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	req.Header.Set("Authorization", "Bearer revoked")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var errResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if errResp["error"] != "The access token was revoked" {
		t.Fatalf("error = %v, want %q", errResp["error"], "The access token was revoked")
	}
}
