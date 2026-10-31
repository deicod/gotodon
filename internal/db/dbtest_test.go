package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/deicod/gotodon/db/migrations"
	"github.com/deicod/gotodon/internal/config"
)

// openMigratedDB connects to a new database with every migration applied
// and drops the database when the test ends. A database of its own, unlike
// the schemas of the migration tests, keeps the extensions that the
// migrations create from colliding when both packages run at once. Like the
// migration tests, it needs DATABASE_URL or DB_* / GOTODON_DATABASE_*
// settings, and skips without them or with -short.
func openMigratedDB(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(testDSN(t))
	if err != nil {
		t.Fatalf("parse db config: %v", err)
	}

	base, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	name := fmt.Sprintf("gotodon_db_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		_ = base.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = base.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		_ = base.Close(context.Background())
	})

	testCfg := cfg.Copy()
	testCfg.Database = name

	migrationDB := stdlib.OpenDB(*testCfg)
	defer migrationDB.Close()
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(migrationDB, "."); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	conn, err := pgx.ConnectConfig(ctx, testCfg)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// testDSN returns the database to test against, read as the migration tests
// read it.
func testDSN(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping database-backed query test in -short mode")
	}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
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
			return cfg.Database.DSN()
		}
	}
	t.Skip("skipping database-backed query test; set DATABASE_URL, DB_* or GOTODON_DATABASE_* env vars")
	return ""
}
