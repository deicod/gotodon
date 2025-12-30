package oembed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubStore struct {
	statuses       map[int64]db.Status
	accountsByID   map[int64]db.Account
	accountErrByID map[int64]error
}

func (s *stubStore) GetStatusByID(_ context.Context, id int64) (db.Status, error) {
	if status, ok := s.statuses[id]; ok {
		return status, nil
	}
	return db.Status{}, pgx.ErrNoRows
}

func (s *stubStore) GetAccountByID(_ context.Context, id int64) (db.Account, error) {
	if err, ok := s.accountErrByID[id]; ok && err != nil {
		return db.Account{}, err
	}
	if account, ok := s.accountsByID[id]; ok {
		return account, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func TestHandler_Show_PublicStatus(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	store := &stubStore{
		statuses: map[int64]db.Status{
			123: {
				ID:         123,
				AccountID:  1,
				Visibility: visibilityPublic,
				Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/123", Valid: true},
			},
		},
		accountsByID: map[int64]db.Account{
			1: {
				ID:          1,
				Username:    "alice",
				DisplayName: "Alice",
			},
		},
	}
	h := NewHandler(store, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=https://example.com/@alice/123", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "private, no-store") {
		t.Fatalf("Cache-Control = %q, want private, no-store", rec.Header().Get("Cache-Control"))
	}

	var resp OEmbedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.Type != oembedType {
		t.Fatalf("type = %q, want %q", resp.Type, oembedType)
	}
	if resp.Version != oembedVersion {
		t.Fatalf("version = %q, want %q", resp.Version, oembedVersion)
	}
	if resp.AuthorName != "Alice" {
		t.Fatalf("author_name = %q, want %q", resp.AuthorName, "Alice")
	}
	if resp.AuthorURL != "https://example.com/@alice" {
		t.Fatalf("author_url = %q, want %q", resp.AuthorURL, "https://example.com/@alice")
	}
	if resp.ProviderName != "example.com" {
		t.Fatalf("provider_name = %q, want %q", resp.ProviderName, "example.com")
	}
	if resp.ProviderURL != "https://example.com/" {
		t.Fatalf("provider_url = %q, want %q", resp.ProviderURL, "https://example.com/")
	}
	if resp.Width != oembedDefaultWidth {
		t.Fatalf("width = %d, want %d", resp.Width, oembedDefaultWidth)
	}
	if resp.Height != nil {
		t.Fatalf("height = %v, want nil", resp.Height)
	}
	if !strings.Contains(resp.HTML, `data-embed-url="https://example.com/@alice/123/embed"`) {
		t.Fatalf("html missing embed url: %q", resp.HTML)
	}
	if !strings.Contains(resp.HTML, `href="https://example.com/@alice/123"`) {
		t.Fatalf("html missing status url: %q", resp.HTML)
	}
	if !strings.Contains(resp.HTML, `data-allowed-prefixes="https://example.com/"`) {
		t.Fatalf("html missing allowed prefixes: %q", resp.HTML)
	}
	if !strings.Contains(resp.HTML, "Post by @alice@example.com") {
		t.Fatalf("html missing author line: %q", resp.HTML)
	}
}

func TestHandler_Show_NonPublicStatus(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	store := &stubStore{
		statuses: map[int64]db.Status{
			123: {
				ID:         123,
				AccountID:  1,
				Visibility: 3,
				Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/123", Valid: true},
			},
		},
		accountsByID: map[int64]db.Account{
			1: {
				ID:       1,
				Username: "alice",
			},
		},
	}
	h := NewHandler(store, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=https://example.com/@alice/123", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandler_Show_MissingURL(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewHandler(&stubStore{}, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/oembed", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandler_Show_WebDomain(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example", WebDomain: "example.com"}}
	store := &stubStore{
		statuses: map[int64]db.Status{
			123: {
				ID:         123,
				AccountID:  1,
				Visibility: visibilityPublic,
				Uri:        pgtype.Text{String: "https://social.example/users/alice/statuses/123", Valid: true},
			},
		},
		accountsByID: map[int64]db.Account{
			1: {
				ID:       1,
				Username: "alice",
			},
		},
	}
	h := NewHandler(store, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=https://example.com/@alice/123", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp OEmbedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if resp.ProviderName != "social.example" {
		t.Fatalf("provider_name = %q, want %q", resp.ProviderName, "social.example")
	}
	if resp.ProviderURL != "https://example.com/" {
		t.Fatalf("provider_url = %q, want %q", resp.ProviderURL, "https://example.com/")
	}
	if !strings.Contains(resp.HTML, `data-embed-url="https://example.com/@alice/123/embed"`) {
		t.Fatalf("html missing embed url: %q", resp.HTML)
	}
	if !strings.Contains(resp.HTML, "Post by @alice@social.example") {
		t.Fatalf("html missing author line: %q", resp.HTML)
	}
}

func TestHandler_Show_RemoteAuthorLine(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	store := &stubStore{
		statuses: map[int64]db.Status{
			123: {
				ID:         123,
				AccountID:  1,
				Visibility: visibilityPublic,
				Uri:        pgtype.Text{String: "https://example.com/users/remote/statuses/123", Valid: true},
			},
		},
		accountsByID: map[int64]db.Account{
			1: {
				ID:       1,
				Username: "remote",
				Domain:   pgtype.Text{String: "remote.social", Valid: true},
			},
		},
	}
	h := NewHandler(store, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=https://example.com/@remote/123", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp OEmbedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if !strings.Contains(resp.HTML, "Post by @remote@remote.social") {
		t.Fatalf("html missing remote author line: %q", resp.HTML)
	}
	if strings.Contains(resp.HTML, "remote.social@example.com") {
		t.Fatalf("html duplicated domain: %q", resp.HTML)
	}
}

func TestHandler_Show_MaxDimensions(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	store := &stubStore{
		statuses: map[int64]db.Status{
			123: {
				ID:         123,
				AccountID:  1,
				Visibility: visibilityPublic,
				Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/123", Valid: true},
			},
		},
		accountsByID: map[int64]db.Account{
			1: {
				ID:       1,
				Username: "alice",
			},
		},
	}
	h := NewHandler(store, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/oembed?url=https://example.com/@alice/123&maxwidth=600&maxheight=800", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp OEmbedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if resp.Width != 600 {
		t.Fatalf("width = %d, want %d", resp.Width, 600)
	}
	if resp.Height == nil || *resp.Height != 800 {
		t.Fatalf("height = %v, want 800", resp.Height)
	}
}
