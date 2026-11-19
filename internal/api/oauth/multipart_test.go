package oauth

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

// multipartOAuthRequest sends fields as curl -F does, in the given order.
func multipartOAuthRequest(t *testing.T, target string, fields ...[2]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := w.WriteField(field[0], field[1]); err != nil {
			t.Fatalf("write field %q: %v", field[0], err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// TestTokenHandler_MultipartBody keeps POST /oauth/token accepting the
// multipart body of Mastodon's client guide, which it read before web.Bind
// parsed multipart.
func TestTokenHandler_MultipartBody(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "urn:ietf:wg:oauth:2.0:oob",
		Scopes:       "read write push",
		Confidential: true,
	}
	store.addApp(app)
	store.updateToken(db.OauthAccessToken{
		ID:            46,
		Token:         "existing-client-token",
		Scopes:        pgtype.Text{String: "read", Valid: true},
		ApplicationID: pgtype.Int8{Int64: app.ID, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})
	handler := NewHandler(store, nil, nil)

	req := multipartOAuthRequest(t, "/oauth/token",
		[2]string{"client_id", "client"},
		[2]string{"client_secret", "secret"},
		[2]string{"redirect_uri", "urn:ietf:wg:oauth:2.0:oob"},
		[2]string{"grant_type", "client_credentials"},
	)
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken != "existing-client-token" {
		t.Fatalf("access_token = %q, want the existing client token", resp.AccessToken)
	}
}

// TestRevoke_MultipartBody keeps POST /oauth/revoke accepting multipart
// bodies. It used to read them through a fallback after web.Bind failed;
// web.Bind now binds them itself.
func TestRevoke_MultipartBody(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           45,
		Uid:          "client45",
		Secret:       "secret45",
		Confidential: true,
	})
	store.updateToken(db.OauthAccessToken{
		ID:            45,
		Token:         "token45",
		ApplicationID: pgtype.Int8{Int64: 45, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	killPublisher := &stubTokenKillPublisher{}
	handler := NewHandler(store, nil, killPublisher)

	req := multipartOAuthRequest(t, "/oauth/revoke",
		[2]string{"client_id", "client45"},
		[2]string{"client_secret", "secret45"},
		[2]string{"token", "token45"},
	)
	rec := httptest.NewRecorder()
	handler.Revoke(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !store.tokensByToken["token45"].RevokedAt.Valid {
		t.Fatal("token not revoked")
	}
	if len(killPublisher.events) != 1 {
		t.Fatalf("kill events = %d, want 1", len(killPublisher.events))
	}
}
