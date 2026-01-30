package v2

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func TestSearchHandler_Search_NoDB(t *testing.T) {
	h := NewSearchHandler(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/search?q=test", nil)
	rec := httptest.NewRecorder()

	h.Search(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestTagURL_PrefersWebDomain(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "local.example",
			WebDomain:   "public.example",
		},
	}

	url := tagURL("golang", cfg)
	if url != "https://public.example/tags/golang" {
		t.Fatalf("url = %q, want %q", url, "https://public.example/tags/golang")
	}
}

func TestTagDisplayName_UsesDisplayName(t *testing.T) {
	tag := db.Tag{
		Name: "golang",
	}
	name := tagDisplayName(tag)
	if name != "golang" {
		t.Fatalf("name = %q, want %q", name, "golang")
	}
}

func TestBuildHashtagHistories(t *testing.T) {
	day := time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)
	rows := []db.ListTagHistoriesRow{
		{TagID: 10, Day: pgtype.Date{Time: day, Valid: true}, Uses: 3, Accounts: 2},
		{TagID: 11, Day: pgtype.Date{Time: day, Valid: true}, Uses: 1, Accounts: 1},
	}

	histories := buildHashtagHistories(rows)
	if len(histories[10]) != 1 {
		t.Fatalf("history len = %d, want 1", len(histories[10]))
	}
	if histories[10][0].Day != strconv.FormatInt(day.Unix(), 10) {
		t.Fatalf("day = %q, want %q", histories[10][0].Day, strconv.FormatInt(day.Unix(), 10))
	}
	if histories[10][0].Uses != "3" || histories[10][0].Accounts != "2" {
		t.Fatalf("uses/accounts = %q/%q, want 3/2", histories[10][0].Uses, histories[10][0].Accounts)
	}
}
