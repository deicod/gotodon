package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/deicod/gotodon/internal/config"
)

type indexExpectation struct {
	name   string
	unique bool
}

var schemaIndexExpectations = []indexExpectation{
	{name: "index_account_domain_blocks_on_account_id_and_domain", unique: true},
	{name: "index_account_migrations_on_target_account_id", unique: false},
	{name: "index_account_pins_on_account_id_and_target_account_id", unique: true},
	{name: "index_account_pins_on_target_account_id", unique: false},
	{name: "index_account_stats_on_last_status_at_and_account_id", unique: false},
	{name: "index_accounts_on_moved_to_account_id", unique: false},
	{name: "index_accounts_on_url", unique: false},
	{name: "index_accounts_on_username_and_domain_lower", unique: true},
	{name: "index_announcement_mutes_on_account_id_and_announcement_id", unique: true},
	{name: "index_announcement_mutes_on_announcement_id", unique: false},
	{name: "index_announcement_reactions_on_account_id_and_announcement_id", unique: true},
	{name: "index_announcement_reactions_on_custom_emoji_id", unique: false},
	{name: "index_appeals_on_approved_by_account_id", unique: false},
	{name: "index_appeals_on_rejected_by_account_id", unique: false},
	{name: "index_bulk_imports_unconfirmed", unique: false},
	{name: "index_conversations_on_parent_status_id", unique: true},
	{name: "index_conversations_on_uri", unique: true},
	{name: "index_custom_emojis_on_shortcode_and_domain", unique: true},
	{name: "index_favourites_on_account_id_and_id", unique: false},
	{name: "index_featured_tags_on_account_id_and_tag_id", unique: true},
	{name: "index_featured_tags_on_tag_id", unique: false},
	{name: "index_list_accounts_on_follow_id", unique: false},
	{name: "index_list_accounts_on_follow_request_id", unique: false},
	{name: "index_markers_on_user_id_and_timeline", unique: true},
	{name: "index_media_attachments_on_scheduled_status_id", unique: false},
	{name: "index_media_attachments_on_shortcode", unique: true},
	{name: "index_notifications_on_account_id_and_group_key", unique: false},
	{name: "index_notifications_on_filtered", unique: false},
	{name: "index_oauth_access_tokens_on_refresh_token", unique: true},
	{name: "index_oauth_access_tokens_on_resource_owner_id", unique: false},
	{name: "index_oauth_applications_on_owner_id_and_owner_type", unique: false},
	{name: "index_oauth_applications_on_superapp", unique: false},
	{name: "index_poll_votes_on_account_id", unique: false},
	{name: "index_polls_on_account_id", unique: false},
	{name: "index_polls_on_status_id", unique: false},
	{name: "index_preview_cards_on_author_account_id", unique: false},
	{name: "index_quotes_on_activity_uri", unique: true},
	{name: "index_quotes_on_approval_uri", unique: false},
	{name: "index_reports_on_action_taken_by_account_id", unique: false},
	{name: "index_reports_on_assigned_account_id", unique: false},
	{name: "index_status_pins_on_account_id_and_status_id", unique: true},
	{name: "index_statuses_20190820", unique: false},
	{name: "index_statuses_local_20190824", unique: false},
	{name: "index_statuses_on_deleted_at", unique: false},
	{name: "index_statuses_on_in_reply_to_account_id", unique: false},
	{name: "index_statuses_on_in_reply_to_id", unique: false},
	{name: "index_statuses_on_uri", unique: true},
	{name: "index_statuses_public_20250129", unique: false},
	{name: "index_tags_on_name_lower_btree", unique: true},
	{name: "index_terms_of_services_on_effective_date", unique: true},
	{name: "index_username_blocks_on_username_lower_btree", unique: true},
	{name: "index_users_on_created_by_application_id", unique: false},
	{name: "index_users_on_reset_password_token", unique: true},
	{name: "index_users_on_role_id", unique: false},
	{name: "index_users_on_unconfirmed_email", unique: false},
	{name: "index_web_push_subscriptions_on_access_token_id", unique: false},
}

func TestSchemaParityIndexes(t *testing.T) {
	db, schema := openSchemaTestDB(t)
	applyMigrations(t, db)

	ctx := context.Background()
	seen := make(map[string]struct{}, len(schemaIndexExpectations))

	for _, exp := range schemaIndexExpectations {
		if _, dup := seen[exp.name]; dup {
			t.Fatalf("duplicate index expectation for %s", exp.name)
		}
		seen[exp.name] = struct{}{}

		unique, err := lookupIndexUnique(ctx, db, schema, exp.name)
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("missing index %s", exp.name)
		}
		if err != nil {
			t.Fatalf("query index %s: %v", exp.name, err)
		}
		if unique != exp.unique {
			t.Fatalf("index %s unique=%t, want %t", exp.name, unique, exp.unique)
		}
	}
}

func openSchemaTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	dsn := schemaTestDSN(t)
	baseCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse db config: %v", err)
	}

	baseDB := stdlib.OpenDB(*baseCfg)
	if err := baseDB.PingContext(context.Background()); err != nil {
		_ = baseDB.Close()
		t.Fatalf("connect db: %v", err)
	}

	schema := fmt.Sprintf("schema_parity_%d", time.Now().UnixNano())
	if _, err := baseDB.ExecContext(context.Background(), fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		_ = baseDB.Close()
		t.Fatalf("create schema: %v", err)
	}

	testCfg := *baseCfg
	testCfg.RuntimeParams = copyRuntimeParams(baseCfg.RuntimeParams)
	testCfg.RuntimeParams["search_path"] = schema
	testDB := stdlib.OpenDB(testCfg)

	if err := testDB.PingContext(context.Background()); err != nil {
		_ = testDB.Close()
		_, _ = baseDB.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		_ = baseDB.Close()
		t.Fatalf("connect schema db: %v", err)
	}

	t.Cleanup(func() {
		_ = testDB.Close()
		_, _ = baseDB.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		_ = baseDB.Close()
	})

	return testDB, schema
}

func schemaTestDSN(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping database-backed migration test in -short mode")
	}

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}

	if !hasExplicitDBEnv() {
		t.Skip("skipping database-backed migration test; set DB_* or GOTODON_DATABASE_* env vars")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	return cfg.Database.DSN()
}

func hasExplicitDBEnv() bool {
	envs := []string{
		"DB_HOST",
		"DB_PORT",
		"DB_USER",
		"DB_PASS",
		"DB_NAME",
		"GOTODON_DATABASE_HOST",
		"GOTODON_DATABASE_PORT",
		"GOTODON_DATABASE_USER",
		"GOTODON_DATABASE_PASSWORD",
		"GOTODON_DATABASE_DATABASE",
	}
	for _, key := range envs {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

func applyMigrations(t *testing.T, db *sql.DB) {
	t.Helper()

	configureGoose(t)
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
}

func configureGoose(t *testing.T) {
	t.Helper()

	goose.SetBaseFS(Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
}

func lookupIndexUnique(ctx context.Context, db *sql.DB, schema, name string) (bool, error) {
	const query = `
		SELECT ix.indisunique
		FROM pg_class i
		JOIN pg_index ix ON ix.indexrelid = i.oid
		JOIN pg_namespace n ON n.oid = i.relnamespace
		WHERE n.nspname = $1 AND i.relname = $2`

	var unique bool
	err := db.QueryRowContext(ctx, query, schema, name).Scan(&unique)
	return unique, err
}

func copyRuntimeParams(input map[string]string) map[string]string {
	output := make(map[string]string, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}
