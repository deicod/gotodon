package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/deicod/gotodon/db/migrations"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/internal/testenv"
	"github.com/deicod/gotodon/pkg/activitypub"
)

var _ DeliveryStore = (*db.Queries)(nil)

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
	name := fmt.Sprintf("gotodon_workers_test_%d", time.Now().UnixNano())
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

// taskEnqueuer builds each delivery's task as DeliveryEnqueuer.Enqueue
// does, and keeps it for the test to process instead of handing it to
// Asynq.
type taskEnqueuer struct {
	tasks []*asynq.Task
}

func (e *taskEnqueuer) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	task, err := NewDeliverActivityTask(localActor.ID, inbox, activity)
	if err != nil {
		return err
	}
	e.tasks = append(e.tasks, task)
	return nil
}

// delivered is what DeliveryHandler hands the HTTP delivery for one task.
type delivered struct {
	signer    string
	suspended bool
	inbox     string
	activity  *activitypub.Activity
}

// TestAccountSuspensionDeliveredByWorker suspends a remote and a local
// account with AccountSuspensionService and runs every task it queues
// through DeliveryHandler against the database, as the delivery worker
// does: the Rejects and the suspended actor's Update reach the HTTP
// delivery, signed by the accounts the worker loads, and a repeated
// suspension changes nothing more.
func TestAccountSuspensionDeliveredByWorker(t *testing.T) {
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

	enqueuer := &taskEnqueuer{}
	service := services.NewAccountSuspensionService(queries, federation.NewOutgoingService(queries, "local.test", enqueuer))
	var deliveries []delivered
	handler := &DeliveryHandler{
		store: queries,
		deliver: func(_ context.Context, actor db.Account, inbox string, activity *activitypub.Activity) error {
			deliveries = append(deliveries, delivered{signer: actor.Username, suspended: actor.SuspendedAt.Valid, inbox: inbox, activity: activity})
			return nil
		},
	}
	// suspend runs the suspension of accountID and then every task it
	// queued through the delivery worker.
	suspend := func(t *testing.T, accountID int64) []delivered {
		t.Helper()
		enqueuer.tasks, deliveries = nil, nil
		if err := service.Suspend(ctx, accountID); err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		for _, task := range enqueuer.tasks {
			if err := handler.ProcessTask(ctx, task); err != nil {
				t.Fatalf("ProcessTask: %v", err)
			}
		}
		if len(deliveries) != len(enqueuer.tasks) {
			t.Fatalf("worker delivered %d of %d tasks", len(deliveries), len(enqueuer.tasks))
		}
		return deliveries
	}

	t.Run("remote account", func(t *testing.T) {
		if _, err := queries.AdminSuspendAccount(ctx, bob); err != nil {
			t.Fatalf("suspend bob: %v", err)
		}
		for run := 1; run <= 2; run++ {
			var got []string
			for _, d := range suspend(t, bob) {
				follow, _ := d.activity.ActivityObject.(map[string]any)
				got = append(got, fmt.Sprintf("%s by %s to %s of %v", d.activity.Type, d.signer, d.inbox, follow["id"]))
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
		if _, err := queries.AdminSuspendAccount(ctx, alice); err != nil {
			t.Fatalf("suspend alice: %v", err)
		}
		var firstIDs []string
		for run := 1; run <= 2; run++ {
			var inboxes, ids []string
			for _, d := range suspend(t, alice) {
				actor, _ := d.activity.ActivityObject.(map[string]any)
				if d.activity.Type != activitypub.TypeUpdate || d.signer != "alice" || !d.suspended ||
					actor["id"] != "https://local.test/users/alice" || actor["suspended"] != true {
					t.Fatalf("run %d: delivery = %s by %s (suspended %v) of %v", run, d.activity.Type, d.signer, d.suspended, d.activity.ActivityObject)
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
		if got := suspend(t, carol); len(got) != 0 {
			t.Fatalf("delivered %d activities for an active account", len(got))
		}
		if n := count(`SELECT count(*) FROM status_trends WHERE account_id = $1`, carol); n != 1 {
			t.Fatalf("carol's trending statuses = %d, want 1", n)
		}
	})
}

// TestAccountSuspensionWaitsForItsTransaction runs the suspension's side
// effects, as a worker that picks up the task at once does, while the
// transaction that suspended the account and queued them is still open:
// they wait for it to end, deliver nothing when it rolls back, and deliver
// the suspended actor once it commits.
func TestAccountSuspensionWaitsForItsTransaction(t *testing.T) {
	conn := openMigratedTestDB(t)
	queries := db.New(conn)
	ctx := context.Background()
	connect := func() *pgx.Conn {
		t.Helper()
		c, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	}
	workerConn, observer := connect(), connect()

	var alice, bob int64
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (username, uri, private_key, public_key)
		VALUES ('alice', 'https://local.test/users/alice', 'private-key', 'public-key') RETURNING id`).Scan(&alice); err != nil {
		t.Fatalf("insert alice: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (username, domain, uri, inbox_url)
		VALUES ('bob', 'remote.example', 'https://remote.example/users/bob', 'https://remote.example/users/bob/inbox') RETURNING id`).Scan(&bob); err != nil {
		t.Fatalf("insert bob: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2)`, bob, alice); err != nil {
		t.Fatalf("bob follows alice: %v", err)
	}

	enqueuer := &taskEnqueuer{}
	suspension := services.NewAccountSuspensionService(db.New(workerConn), federation.NewOutgoingService(db.New(workerConn), "local.test", enqueuer))

	// suspendIn suspends alice in a transaction, runs the side effects
	// meanwhile, ends the transaction with end, and returns the tasks the
	// side effects queued.
	suspendIn := func(t *testing.T, end func(pgx.Tx) error) []*asynq.Task {
		t.Helper()
		enqueuer.tasks = nil
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if n, err := queries.WithTx(tx).AdminSuspendAccount(ctx, alice); err != nil || n != 1 {
			t.Fatalf("AdminSuspendAccount: %d, %v; want 1 account", n, err)
		}

		done := make(chan error, 1)
		go func() { done <- suspension.Suspend(ctx, alice) }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var waiting int64
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock'`, workerConn.PgConn().PID()).Scan(&waiting); err != nil {
				t.Fatalf("pg_stat_activity: %v", err)
			}
			if waiting == 1 {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("side effects ended (%v) while the suspension's transaction was open", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("the side effects never waited for the suspension's transaction")
			}
			time.Sleep(10 * time.Millisecond)
		}

		if err := end(tx); err != nil {
			t.Fatalf("end the transaction: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Suspend: %v", err)
		}
		return enqueuer.tasks
	}

	if tasks := suspendIn(t, func(tx pgx.Tx) error { return tx.Rollback(ctx) }); len(tasks) != 0 {
		t.Fatalf("rolled-back suspension queued %d deliveries, want none", len(tasks))
	}
	tasks := suspendIn(t, func(tx pgx.Tx) error { return tx.Commit(ctx) })
	if len(tasks) != 1 {
		t.Fatalf("committed suspension queued %d deliveries, want the suspended actor to bob", len(tasks))
	}
	var payload DeliverActivityPayload
	if err := json.Unmarshal(tasks[0].Payload(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	var activity struct {
		Type   string         `json:"type"`
		Object map[string]any `json:"object"`
	}
	if err := json.Unmarshal(payload.Activity, &activity); err != nil {
		t.Fatalf("activity: %v", err)
	}
	if payload.LocalAccountID != alice || payload.Inbox != "https://remote.example/users/bob/inbox" ||
		activity.Type != activitypub.TypeUpdate || activity.Object["suspended"] != true {
		t.Fatalf("delivery = %s of %v by %d to %s, want alice's suspended actor to bob's inbox", activity.Type, activity.Object, payload.LocalAccountID, payload.Inbox)
	}
}
