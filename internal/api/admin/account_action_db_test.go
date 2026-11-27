package admin

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/testenv"
)

// openMigratedPool connects to a new database with every migration applied
// and drops it when the test ends. Like the query tests in internal/db, it
// needs DATABASE_URL or DB_* / GOTODON_DATABASE_* settings, and skips
// without them or with -short.
func openMigratedPool(t *testing.T) *pgxpool.Pool {
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
	name := fmt.Sprintf("gotodon_admin_test_%d", time.Now().UnixNano())
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
	defer migrationDB.Close()
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

// dbActionFixture is an Owner account, a local user to act on with two
// unresolved reports and a resolved one, a report about someone else, and a
// warning preset.
type dbActionFixture struct {
	pool    *pgxpool.Pool
	handler *AccountsHandler
	mux     *http.ServeMux
	mail    *stubMail

	owner, target, other               int64
	open, openToo, resolved, otherOpen int64
	preset                             int64
}

func newDBActionFixture(t *testing.T, permissions int64) *dbActionFixture {
	pool := openMigratedPool(t)
	ctx := context.Background()
	f := &dbActionFixture{pool: pool, mail: &stubMail{}}

	scan := func(dest *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	account := func(dest *int64, username string) {
		scan(dest, `INSERT INTO accounts (username, uri) VALUES ($1::text, 'https://example.com/users/' || $1::text) RETURNING id`, username)
	}
	account(&f.owner, "olive")
	account(&f.target, "alice")
	account(&f.other, "bob")
	var ownerUser, ignored int64
	scan(&ownerUser, `INSERT INTO users (email, account_id, approved, role_id)
		VALUES ('olive@example.com', $1, true, (SELECT id FROM user_roles WHERE name = 'Owner')) RETURNING id`, f.owner)
	scan(&ignored, `INSERT INTO users (email, account_id, approved, locale) VALUES ('alice@example.com', $1, true, 'en') RETURNING id`, f.target)

	report := func(dest *int64, target int64, resolved bool) {
		scan(dest, `INSERT INTO reports (account_id, target_account_id, status_ids, action_taken_at)
			VALUES ($1, $2, '{}', CASE WHEN $3::boolean THEN now() END) RETURNING id`, f.other, target, resolved)
	}
	report(&f.open, f.target, false)
	report(&f.openToo, f.target, false)
	report(&f.resolved, f.target, true)
	report(&f.otherOpen, f.other, false)
	scan(&f.preset, `INSERT INTO account_warning_presets (title, text) VALUES ('Spam', 'No spam.') RETURNING id`)

	queries := db.New(pool)
	f.handler = NewAccountsHandler(queries, pool, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})
	f.handler.roles = &stubRoles{
		usersByAccount: map[int64]db.User{f.owner: {ID: ownerUser, AccountID: f.owner}},
		roles:          map[int64]db.UserRole{ownerUser: {ID: 1, Name: "Custom", Permissions: permissions, Position: 1000}},
	}
	f.handler.SetMailDeliverer(f.mail)
	f.mux = http.NewServeMux()
	f.handler.RegisterRoutes(f.mux)
	return f
}

func (f *dbActionFixture) act(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/admin/accounts/%d/action", f.target), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withScopes(withIdentity(req, &f.owner, nil), adminToken)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// state is what an account action changes in the database.
type dbActionState struct {
	silenced, resolvedOpen, resolvedOpenToo, resolvedOther bool
	strikes, logs, notifications                           int
}

func (f *dbActionFixture) state(t *testing.T) dbActionState {
	t.Helper()
	var s dbActionState
	if err := f.pool.QueryRow(context.Background(), `
		SELECT
			(SELECT silenced_at IS NOT NULL FROM accounts WHERE id = $1),
			(SELECT action_taken_at IS NOT NULL FROM reports WHERE id = $2),
			(SELECT action_taken_at IS NOT NULL FROM reports WHERE id = $3),
			(SELECT action_taken_at IS NOT NULL FROM reports WHERE id = $4),
			(SELECT count(*) FROM account_warnings WHERE target_account_id = $1),
			(SELECT count(*) FROM admin_action_logs),
			(SELECT count(*) FROM notifications WHERE account_id = $1)`,
		f.target, f.open, f.openToo, f.otherOpen,
	).Scan(&s.silenced, &s.resolvedOpen, &s.resolvedOpenToo, &s.resolvedOther, &s.strikes, &s.logs, &s.notifications); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return s
}

func TestAccountsTakeActionDatabase(t *testing.T) {
	f := newDBActionFixture(t, ownerPermissions)
	before := f.state(t)

	assertOK(t, f.act(t, fmt.Sprintf(`{"type":"silence","warning_preset_id":"%d","text":"Second time."}`, f.preset)))

	got := f.state(t)
	want := dbActionState{silenced: true, resolvedOpen: true, resolvedOpenToo: true, strikes: 1, logs: before.logs + 3, notifications: 1}
	if got != want {
		t.Fatalf("state = %+v, want %+v", got, want)
	}

	var (
		action    int32
		text      string
		accountID int64
		statusIDs []string
	)
	if err := f.pool.QueryRow(context.Background(),
		`SELECT action, text, account_id, status_ids FROM account_warnings WHERE target_account_id = $1`, f.target,
	).Scan(&action, &text, &accountID, &statusIDs); err != nil {
		t.Fatalf("read strike: %v", err)
	}
	if action != 3000 || text != "No spam.\n\nSecond time." || accountID != f.owner || statusIDs != nil {
		t.Fatalf("strike = %d %q by %d citing %#v", action, text, accountID, statusIDs)
	}

	var resolvedBy int64
	if err := f.pool.QueryRow(context.Background(), `SELECT action_taken_by_account_id FROM reports WHERE id = $1`, f.open).Scan(&resolvedBy); err != nil || resolvedBy != f.owner {
		t.Fatalf("report resolved by %d (%v), want %d", resolvedBy, err, f.owner)
	}

	rows, err := f.pool.Query(context.Background(), `SELECT action, target_type, human_identifier FROM admin_action_logs ORDER BY id`)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	var logs []string
	for rows.Next() {
		var action, targetType, identifier string
		if err := rows.Scan(&action, &targetType, &identifier); err != nil {
			t.Fatalf("scan log: %v", err)
		}
		logs = append(logs, action+":"+targetType+":"+identifier)
	}
	rows.Close()
	wantLogs := []string{"silence:Account:alice", fmt.Sprintf("resolve:Report:%d", f.open), fmt.Sprintf("resolve:Report:%d", f.openToo)}
	if strings.Join(logs, ",") != strings.Join(wantLogs, ",") {
		t.Fatalf("logs = %v, want %v", logs, wantLogs)
	}

	if len(f.mail.mails) != 1 || f.mail.mails[0].To != "alice@example.com" || f.mail.mails[0].Params["Acct"] != "@alice@example.com" {
		t.Fatalf("mails = %+v, want the warning to alice", f.mail.mails)
	}
}

// TestAccountsTakeActionDatabaseRefusals checks that a refusal found in the
// transaction leaves the database as it was.
func TestAccountsTakeActionDatabaseRefusals(t *testing.T) {
	t.Run("unknown preset", func(t *testing.T) {
		f := newDBActionFixture(t, ownerPermissions)
		before := f.state(t)
		rec := f.act(t, fmt.Sprintf(`{"type":"silence","report_id":"%d","warning_preset_id":"%d"}`, f.open, f.preset+1))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d (%s), want 404", rec.Code, rec.Body.String())
		}
		if got := f.state(t); got != before {
			t.Fatalf("state = %+v, want %+v", got, before)
		}
	})

	t.Run("reports without manage_reports", func(t *testing.T) {
		f := newDBActionFixture(t, auth.PermissionManageUsers)
		before := f.state(t)
		rec := f.act(t, `{"type":"silence"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d (%s), want 403", rec.Code, rec.Body.String())
		}
		if got := f.state(t); got != before {
			t.Fatalf("state = %+v, want %+v", got, before)
		}
	})
}

// TestPoolTransactor checks that the transactor commits work that succeeds
// and rolls back work that fails, on a real transaction.
func TestPoolTransactor(t *testing.T) {
	f := newDBActionFixture(t, ownerPermissions)
	ctx := context.Background()
	tx := f.handler.actions
	before := f.state(t)

	failing := errors.New("step failed")
	err := tx.InTx(ctx, func(store accountActionStore) error {
		if err := store.AdminSilenceAccount(ctx, f.target); err != nil {
			return err
		}
		if _, err := store.CreateAccountWarning(ctx, db.CreateAccountWarningParams{}); err != nil {
			return err
		}
		return failing
	})
	if !errors.Is(err, failing) {
		t.Fatalf("InTx() error = %v, want %v", err, failing)
	}
	if got := f.state(t); got != before {
		t.Fatalf("after a failure state = %+v, want %+v", got, before)
	}

	if err := tx.InTx(ctx, func(store accountActionStore) error {
		return store.AdminSilenceAccount(ctx, f.target)
	}); err != nil {
		t.Fatalf("InTx() error = %v", err)
	}
	if got := f.state(t); !got.silenced {
		t.Fatalf("state = %+v, want the account silenced", got)
	}
}
