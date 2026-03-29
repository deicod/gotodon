package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgoauth "github.com/deicod/gotodon/pkg/oauth"
)

type stubTokenValidator struct {
	info *TokenInfo
	err  error
}

func (s stubTokenValidator) ValidateToken(ctx context.Context, token string) (*TokenInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.info != nil {
		return s.info, nil
	}
	return &TokenInfo{TokenID: 1, AppID: 1, Scopes: "read"}, nil
}

func TestRequireAuth_ErrorMessages(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "revoked token",
			err:      pkgoauth.ErrRevokedToken,
			expected: "The access token was revoked",
		},
		{
			name:     "expired token",
			err:      pkgoauth.ErrExpiredToken,
			expected: "The access token expired",
		},
		{
			name:     "invalid token",
			err:      pkgoauth.ErrInvalidToken,
			expected: "The access token is invalid",
		},
		{
			name:     "unknown error",
			err:      errors.New("boom"),
			expected: "The access token is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mw := RequireAuth(stubTokenValidator{err: tt.err})
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
			req.Header.Set("Authorization", "Bearer token")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			var errResp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("unmarshal error response: %v", err)
			}
			if errResp["error"] != tt.expected {
				t.Fatalf("error = %v, want %q", errResp["error"], tt.expected)
			}
		})
	}
}

func TestAuthenticate_RevokedTokenMessage(t *testing.T) {
	mw := Authenticate(stubTokenValidator{err: pkgoauth.ErrRevokedToken})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/verify_credentials", nil)
	req.Header.Set("Authorization", "Bearer revoked")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

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

