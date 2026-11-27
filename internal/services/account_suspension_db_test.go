package services

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/deicod/gotodon/db/migrations"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/testenv"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// openMigratedTestDB connects to a new database with every migration
// applied and drops it when the test ends. Like the query tests in
// internal/db, it needs DATABASE_URL or DB_* / GOTODON_DATABASE_* settings,
// and skips without them or with -short.
func openMigratedTestDB(t *testing.T) *pgx.Conn {
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
	name := fmt.Sprintf("gotodon_services_test_%d", time.Now().UnixNano())
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

	conn, err := pgx.ConnectConfig(ctx, testCfg)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

type enqueuedDelivery struct {
	signer   string
	inbox    string
	activity *activitypub.Activity
}

type recordingDeliveryEnqueuer struct {
	deliveries []enqueuedDelivery
}

func (r *recordingDeliveryEnqueuer) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	r.deliveries = append(r.deliveries, enqueuedDelivery{signer: localActor.Username, inbox: inbox, activity: activity})
	return nil
}

func TestAccountSuspensionWithDatabase(t *testing.T) {
	conn := openMigratedTestDB(t)
	queries := db.New(conn)
	ctx := context.Background()
	exec := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	local := func(username string) int64 {
		return exec(`INSERT INTO accounts (username, uri, private_key, public_key) VALUES ($1, $2, 'private-key', 'public-key') RETURNING id`,
			username, "https://local.test/users/"+username)
	}
	remote := func(username, inbox, sharedInbox string) int64 {
		return exec(`INSERT INTO accounts (username, domain, uri, inbox_url, shared_inbox_url) VALUES ($1, 'remote.example', $2, $3, $4) RETURNING id`,
			username, "https://remote.example/users/"+username, inbox, sharedInbox)
	}
	follow := func(table string, follower, target int64, uri string) {
		exec(`INSERT INTO `+table+` (account_id, target_account_id, uri) VALUES ($1, $2, NULLIF($3, '')) RETURNING id`, follower, target, uri)
	}
	trend := func(author int64) {
		status := exec(`INSERT INTO statuses (account_id, text) VALUES ($1, 'hi') RETURNING id`, author)
		exec(`INSERT INTO status_trends (status_id, account_id, allowed) VALUES ($1, $2, true) RETURNING id`, status, author)
	}
	count := func(sql string, args ...any) int64 {
		t.Helper()
		return exec(sql, args...)
	}

	alice := local("alice")
	carol := local("carol")
	bob := remote("bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	dave := remote("dave", "https://remote.example/users/dave/inbox", "https://remote.example/inbox")
	erin := remote("erin", "https://remote.example/users/erin/inbox", "")
	follow("follows", bob, alice, "https://remote.example/follows/1")
	follow("follow_requests", bob, carol, "https://remote.example/follows/2")
	follow("follows", dave, alice, "")
	follow("follows", erin, alice, "")
	follow("follows", alice, dave, "")
	trend(alice)
	trend(bob)
	trend(carol)

	enqueuer := &recordingDeliveryEnqueuer{}
	service := NewAccountSuspensionService(queries, federation.NewOutgoingService(queries, "local.test", enqueuer))

	t.Run("remote account", func(t *testing.T) {
		if err := queries.AdminSuspendAccount(ctx, bob); err != nil {
			t.Fatalf("suspend bob: %v", err)
		}
		for run := 1; run <= 2; run++ {
			enqueuer.deliveries = nil
			if err := service.Suspend(ctx, bob); err != nil {
				t.Fatalf("Suspend run %d: %v", run, err)
			}
			var got []string
			for _, d := range enqueuer.deliveries {
				follow, _ := d.activity.ActivityObject.(*activitypub.Activity)
				if follow == nil {
					t.Fatalf("run %d: %s has no Follow object", run, d.activity.Type)
				}
				got = append(got, fmt.Sprintf("%s by %s to %s of %v", d.activity.Type, d.signer, d.inbox, follow.ID))
			}
			sort.Strings(got)
			var want []string
			if run == 1 {
				want = []string{
					"Reject by alice to https://remote.example/users/bob/inbox of https://remote.example/follows/1",
					"Reject by carol to https://remote.example/users/bob/inbox of https://remote.example/follows/2",
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("run %d: deliveries = %v, want %v", run, got, want)
			}
		}
		if n := count(`SELECT count(*) FROM follows WHERE account_id = $1`, bob); n != 0 {
			t.Fatalf("bob still has %d follows", n)
		}
		if n := count(`SELECT count(*) FROM follow_requests WHERE account_id = $1`, bob); n != 0 {
			t.Fatalf("bob still has %d follow requests", n)
		}
		if n := count(`SELECT count(*) FROM follows WHERE target_account_id = $1`, alice); n != 2 {
			t.Fatalf("alice has %d followers, want dave and erin", n)
		}
		if n := count(`SELECT count(*) FROM status_trends WHERE account_id = $1`, bob); n != 0 {
			t.Fatalf("bob still has %d trending statuses", n)
		}
		if n := count(`SELECT count(*) FROM status_trends`); n != 2 {
			t.Fatalf("%d trending statuses, want alice's and carol's", n)
		}
	})

	t.Run("local account", func(t *testing.T) {
		if err := queries.AdminSuspendAccount(ctx, alice); err != nil {
			t.Fatalf("suspend alice: %v", err)
		}
		var firstIDs []string
		for run := 1; run <= 2; run++ {
			enqueuer.deliveries = nil
			if err := service.Suspend(ctx, alice); err != nil {
				t.Fatalf("Suspend run %d: %v", run, err)
			}
			var inboxes, ids []string
			for _, d := range enqueuer.deliveries {
				actor, ok := d.activity.ActivityObject.(activitypub.Actor)
				if d.activity.Type != activitypub.TypeUpdate || d.signer != "alice" || !ok || !actor.Suspended || actor.ID != "https://local.test/users/alice" {
					t.Fatalf("run %d: delivery = %s by %s of %+v", run, d.activity.Type, d.signer, d.activity.ActivityObject)
				}
				inboxes = append(inboxes, d.inbox)
				ids = append(ids, d.activity.ID)
			}
			if want := "[https://remote.example/inbox https://remote.example/users/erin/inbox]"; fmt.Sprint(inboxes) != want {
				t.Fatalf("run %d: inboxes = %v, want %s", run, inboxes, want)
			}
			if run == 1 {
				firstIDs = ids
			} else if fmt.Sprint(ids) != fmt.Sprint(firstIDs) {
				t.Fatalf("retry sent activities %v, want the same %v", ids, firstIDs)
			}
		}
		if n := count(`SELECT count(*) FROM follows WHERE target_account_id = $1 OR account_id = $1`, alice); n != 3 {
			t.Fatalf("alice's follows changed: %d, want 3", n)
		}
		if n := count(`SELECT count(*) FROM status_trends WHERE account_id = $1`, alice); n != 0 {
			t.Fatalf("alice still has %d trending statuses", n)
		}
	})

	t.Run("account that is not suspended", func(t *testing.T) {
		enqueuer.deliveries = nil
		if err := service.Suspend(ctx, carol); err != nil {
			t.Fatalf("Suspend carol: %v", err)
		}
		if len(enqueuer.deliveries) != 0 {
			t.Fatalf("delivered %d activities for an active account", len(enqueuer.deliveries))
		}
		if n := count(`SELECT count(*) FROM status_trends WHERE account_id = $1`, carol); n != 1 {
			t.Fatalf("carol's trending statuses = %d, want 1", n)
		}
	})
}
