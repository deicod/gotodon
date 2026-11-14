package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func registerRequest(t *testing.T, username string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"username":  username,
		"email":     username + "@mail.test",
		"password":  "correct horse battery",
		"agreement": true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestRegisterStoresConfiguredActorURI checks that a registered account's
// ActivityPub ID is on the configured federation domain, not the web domain
// and not a placeholder.
func TestRegisterStoresConfiguredActorURI(t *testing.T) {
	pool := openMigratedTestPool(t)
	cfg := &config.Config{Instance: config.InstanceConfig{
		LocalDomain:       "social.example",
		WebDomain:         "web.social.example",
		RegistrationsMode: "open",
	}}
	h := NewAccountsHandler(db.New(pool), pool, cfg, nil)

	rec := httptest.NewRecorder()
	h.Register(rec, registerRequest(t, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var uri string
	if err := pool.QueryRow(context.Background(),
		`SELECT uri FROM accounts WHERE username = 'alice' AND domain IS NULL`,
	).Scan(&uri); err != nil {
		t.Fatalf("select account: %v", err)
	}
	if uri != "https://social.example/users/alice" {
		t.Fatalf("uri = %q, want https://social.example/users/alice", uri)
	}
}

// TestRegisterRefusesWithoutDomain checks that registration creates nothing
// when no federation domain is configured, instead of an account under an
// invented one.
func TestRegisterRefusesWithoutDomain(t *testing.T) {
	pool := openMigratedTestPool(t)
	h := NewAccountsHandler(db.New(pool), pool, &config.Config{Instance: config.InstanceConfig{
		WebDomain:         "web.social.example",
		RegistrationsMode: "open",
	}}, nil)

	rec := httptest.NewRecorder()
	h.Register(rec, registerRequest(t, "bob"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s, want %d", rec.Code, rec.Body.String(), http.StatusInternalServerError)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM accounts WHERE username = 'bob'`).Scan(&count); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d accounts created, want none", count)
	}
}
