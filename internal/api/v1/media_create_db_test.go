package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/deicod/gotodon/db/migrations"
	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

// openMediaTestPool connects to a new database with every migration applied
// and drops it when the test ends. Like the query tests in internal/db, it
// needs DATABASE_URL or DB_* / GOTODON_DATABASE_* settings, and skips
// without them or with -short.
func openMediaTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping database-backed test in -short mode")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		for _, key := range []string{
			"DB_HOST", "DB_PORT", "DB_USER", "DB_PASS", "DB_NAME",
			"GOTODON_DATABASE_HOST", "GOTODON_DATABASE_PORT", "GOTODON_DATABASE_USER",
			"GOTODON_DATABASE_PASSWORD", "GOTODON_DATABASE_DATABASE",
		} {
			if os.Getenv(key) != "" {
				cfg, err := config.Load()
				if err != nil {
					t.Fatalf("load config: %v", err)
				}
				dsn = cfg.Database.DSN()
				break
			}
		}
	}
	if dsn == "" {
		t.Skip("skipping database-backed test; set DATABASE_URL, DB_* or GOTODON_DATABASE_* env vars")
	}

	ctx := context.Background()
	baseCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse db config: %v", err)
	}
	base, err := pgx.ConnectConfig(ctx, baseCfg)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	name := fmt.Sprintf("gotodon_v1_media_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		_ = base.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = base.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		_ = base.Close(context.Background())
	})

	testCfg := baseCfg.Copy()
	testCfg.Database = name
	migrationDB := stdlib.OpenDB(*testCfg)
	defer func() { _ = migrationDB.Close() }()
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(migrationDB, "."); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	poolCfg, err := pgxpool.ParseConfig(testCfg.ConnString())
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolCfg.ConnConfig = testCfg
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestMediaCreateURLIsServed uploads through POST /api/v1/media and fetches
// the url the response and GET /api/v1/media/:id return. The server's zone is
// far east of UTC, so the local date differs from the UTC one for much of the
// day.
func TestMediaCreateURLIsServed(t *testing.T) {
	pool := openMediaTestPool(t)
	previous := time.Local
	time.Local = time.FixedZone("UTC+14", 14*60*60)
	t.Cleanup(func() { time.Local = previous })

	ctx := context.Background()
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (username, uri) VALUES ('alice', 'https://social.example/users/alice') RETURNING id`,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	cfg := fileURLConfig(t)
	h := NewMediaHandler(db.New(pool), media.NewUploader(media.NewStorage(cfg)))

	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for x := range 32 {
		img.Set(x, x%16, color.RGBA{R: 200, A: 255})
	}
	var upload bytes.Buffer
	if err := png.Encode(&upload, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "my photo.png")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	_, _ = part.Write(upload.Bytes())
	_ = form.WriteField("description", "a red line")
	_ = form.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	assertNoRelativeFileURLs(t, rec.Body.Bytes())
	var created MediaAttachment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if !strings.HasPrefix(created.URL, "https://web.example/files/media_attachments/files/") {
		t.Fatalf("url = %q, want an absolute URL below https://web.example/files/", created.URL)
	}
	if created.PreviewURL != created.URL {
		t.Fatalf("preview_url = %q, want the url %q of an attachment without thumbnail", created.PreviewURL, created.URL)
	}
	if created.RemoteURL != nil {
		t.Fatalf("remote_url = %q, want null for a local upload", *created.RemoteURL)
	}
	fetchServedFile(t, cfg, created.URL, upload.Bytes())

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+created.ID, nil)
	getReq.SetPathValue("id", created.ID)
	getRec := httptest.NewRecorder()
	h.Get(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", getRec.Code, getRec.Body.String())
	}
	var fetched MediaAttachment
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.URL != created.URL || fetched.PreviewURL != created.PreviewURL {
		t.Fatalf("GET url = %q, preview_url = %q; want %q from the upload", fetched.URL, fetched.PreviewURL, created.URL)
	}
}
