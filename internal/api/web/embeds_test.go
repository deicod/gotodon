package web

import (
	"context"
	"encoding/json"
	"errors"
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

type stubEmbedStore struct {
	statuses map[int64]db.Status
	accounts map[int64]db.Account
	blocks   map[string]bool
	blockErr error
}

func (s *stubEmbedStore) GetStatusByID(_ context.Context, id int64) (db.Status, error) {
	if status, ok := s.statuses[id]; ok {
		return status, nil
	}
	return db.Status{}, pgx.ErrNoRows
}

func (s *stubEmbedStore) GetAccountByID(_ context.Context, id int64) (db.Account, error) {
	if account, ok := s.accounts[id]; ok {
		return account, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubEmbedStore) GetBlock(_ context.Context, arg db.GetBlockParams) (db.Block, error) {
	if s.blockErr != nil {
		return db.Block{}, s.blockErr
	}
	key := blockKey(arg.AccountID, arg.TargetAccountID)
	if s.blocks[key] {
		return db.Block{ID: 1, AccountID: arg.AccountID, TargetAccountID: arg.TargetAccountID}, nil
	}
	return db.Block{}, pgx.ErrNoRows
}

func blockKey(a, b int64) string {
	return strconv.FormatInt(a, 10) + ":" + strconv.FormatInt(b, 10)
}

type stubOEmbedFetcher struct {
	payload map[string]any
	err     error
}

func (s *stubOEmbedFetcher) Fetch(_ context.Context, _ string) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.payload, nil
}

func TestEmbedsHandler_LocalPublic_Anonymous(t *testing.T) {
	store := &stubEmbedStore{
		statuses: map[int64]db.Status{
			1: {
				ID:         1,
				AccountID:  10,
				Visibility: visibilityPublic,
				Local:      pgtype.Bool{Bool: true, Valid: true},
			},
		},
		accounts: map[int64]db.Account{
			10: {ID: 10, Username: "alice"},
		},
		blocks: map[string]bool{},
	}
	handler := NewEmbedsHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})

	req := httptest.NewRequest(http.MethodGet, "/api/web/embeds/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	handler.Show(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if html, ok := payload["html"].(string); !ok || strings.TrimSpace(html) == "" {
		t.Fatalf("html missing in response")
	}
}

func TestEmbedsHandler_LocalPrivate_Anonymous(t *testing.T) {
	store := &stubEmbedStore{
		statuses: map[int64]db.Status{
			2: {
				ID:         2,
				AccountID:  10,
				Visibility: 2,
				Local:      pgtype.Bool{Bool: true, Valid: true},
			},
		},
		accounts: map[int64]db.Account{
			10: {ID: 10, Username: "alice"},
		},
		blocks: map[string]bool{},
	}
	h := NewEmbedsHandler(store, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/web/embeds/2", nil)
	req.SetPathValue("id", "2")
	rec := httptest.NewRecorder()
	h.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestEmbedsHandler_Remote_Anonymous(t *testing.T) {
	store := &stubEmbedStore{
		statuses: map[int64]db.Status{
			3: {
				ID:         3,
				AccountID:  20,
				Visibility: visibilityPublic,
				Local:      pgtype.Bool{Bool: false, Valid: true},
				Url:        pgtype.Text{String: "https://remote.example/statuses/3", Valid: true},
			},
		},
		accounts: map[int64]db.Account{
			20: {ID: 20, Username: "remote"},
		},
		blocks: map[string]bool{},
	}
	h := NewEmbedsHandler(store, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/web/embeds/3", nil)
	req.SetPathValue("id", "3")
	rec := httptest.NewRecorder()
	h.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestEmbedsHandler_Remote_WithAuth(t *testing.T) {
	store := &stubEmbedStore{
		statuses: map[int64]db.Status{
			4: {
				ID:         4,
				AccountID:  20,
				Visibility: visibilityPublic,
				Local:      pgtype.Bool{Bool: false, Valid: true},
				Url:        pgtype.Text{String: "https://remote.example/statuses/4", Valid: true},
			},
		},
		accounts: map[int64]db.Account{
			20: {ID: 20, Username: "remote"},
		},
		blocks: map[string]bool{},
	}
	h := NewEmbedsHandler(store, &config.Config{})
	h.fetcher = &stubOEmbedFetcher{payload: map[string]any{"html": "<iframe src=\"https://example.com\"></iframe>"}}

	req := httptest.NewRequest(http.MethodGet, "/api/web/embeds/4", nil)
	req.SetPathValue("id", "4")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(99))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	htmlValue, _ := payload["html"].(string)
	if !strings.Contains(htmlValue, "sandbox=") {
		t.Fatalf("expected sandbox attribute in iframe")
	}
}

func TestEmbedsHandler_Blocked(t *testing.T) {
	store := &stubEmbedStore{
		statuses: map[int64]db.Status{
			5: {
				ID:         5,
				AccountID:  50,
				Visibility: visibilityPublic,
				Local:      pgtype.Bool{Bool: true, Valid: true},
			},
		},
		accounts: map[int64]db.Account{
			50: {ID: 50, Username: "alice"},
		},
		blocks: map[string]bool{
			blockKey(50, 99): true,
		},
	}
	h := NewEmbedsHandler(store, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/web/embeds/5", nil)
	req.SetPathValue("id", "5")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(99))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Show(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestEmbedsHandler_BlockCheckError(t *testing.T) {
	store := &stubEmbedStore{
		statuses: map[int64]db.Status{
			6: {
				ID:         6,
				AccountID:  60,
				Visibility: visibilityPublic,
				Local:      pgtype.Bool{Bool: true, Valid: true},
			},
		},
		accounts: map[int64]db.Account{
			60: {ID: 60, Username: "alice"},
		},
		blocks:   map[string]bool{},
		blockErr: errors.New("db unavailable"),
	}
	h := NewEmbedsHandler(store, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/web/embeds/6", nil)
	req.SetPathValue("id", "6")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(99))
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.Show(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
