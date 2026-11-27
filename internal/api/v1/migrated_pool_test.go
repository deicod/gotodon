package v1

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/deicod/gotodon/db/migrations"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/testenv"
)

// openMigratedTestPool connects to a new database with every migration applied
// and drops it when the test ends. Like the query tests in internal/db, it
// needs DATABASE_URL or DB_* / GOTODON_DATABASE_* settings, and skips
// without them or with -short.
func openMigratedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		testenv.SkipService(t, "skipping database-backed test in -short mode")
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
		testenv.SkipService(t, "skipping database-backed test; set DATABASE_URL, DB_* or GOTODON_DATABASE_* env vars")
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
	name := fmt.Sprintf("gotodon_v1_test_%d", time.Now().UnixNano())
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
