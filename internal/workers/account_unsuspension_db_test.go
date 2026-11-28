package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// TestAccountUnsuspensionDeliveredByWorker suspends and then unsuspends a
// local account with the suspension and unsuspension services, and runs
// every task they queue through DeliveryHandler against the database: the
// followers that were sent the suspended actor get the actor back, profile
// included, signed by the account; a repeated unsuspension sends the same
// activity again; and an account suspended again, or whose deletion has
// finished, gets nothing.
func TestAccountUnsuspensionDeliveredByWorker(t *testing.T) {
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
		return exec(`INSERT INTO accounts (username, uri, private_key, public_key, display_name, note, discoverable)
			VALUES ($1, $2, 'private-key', 'public-key', $3, 'bio', true) RETURNING id`,
			username, "https://local.test/users/"+username, "Display "+username)
	}
	remote := func(username, inbox, sharedInbox string) int64 {
		return exec(`INSERT INTO accounts (username, domain, uri, inbox_url, shared_inbox_url) VALUES ($1, 'remote.example', $2, $3, $4) RETURNING id`,
			username, "https://remote.example/users/"+username, inbox, sharedInbox)
	}
	follow := func(follower, target int64) {
		exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, follower, target)
	}

	alice := local("alice")
	dora := local("dora")
	bob := remote("bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	dave := remote("dave", "https://remote.example/users/dave/inbox", "https://remote.example/inbox")
	erin := remote("erin", "https://remote.example/users/erin/inbox", "")
	for _, follower := range []int64{bob, dave, erin} {
		follow(follower, alice)
		follow(follower, dora)
	}

	enqueuer := &taskEnqueuer{}
	outgoing := federation.NewOutgoingService(queries, "local.test", enqueuer)
	suspension := services.NewAccountSuspensionService(queries, outgoing)
	unsuspension := services.NewAccountUnsuspensionService(queries, outgoing)
	var deliveries []delivered
	handler := &DeliveryHandler{
		store: queries,
		deliver: func(_ context.Context, actor db.Account, inbox string, activity *activitypub.Activity) error {
			deliveries = append(deliveries, delivered{signer: actor.Username, suspended: actor.SuspendedAt.Valid, inbox: inbox, activity: activity})
			return nil
		},
	}
	// run runs fn and then every task it queued through the delivery
	// worker.
	run := func(t *testing.T, fn func() error) []delivered {
		t.Helper()
		enqueuer.tasks, deliveries = nil, nil
		if err := fn(); err != nil {
			t.Fatal(err)
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
	suspend := func(t *testing.T, accountID int64) []delivered {
		t.Helper()
		if err := queries.AdminSuspendAccount(ctx, accountID); err != nil {
			t.Fatalf("AdminSuspendAccount: %v", err)
		}
		return run(t, func() error { return suspension.Suspend(ctx, accountID) })
	}
	unsuspend := func(t *testing.T, accountID int64) []delivered {
		t.Helper()
		return run(t, func() error { return unsuspension.Unsuspend(ctx, accountID) })
	}
	lift := func(t *testing.T, accountID int64, want int64) {
		t.Helper()
		n, err := queries.AdminUnsuspendAccount(ctx, accountID)
		if err != nil {
			t.Fatalf("AdminUnsuspendAccount: %v", err)
		}
		if n != want {
			t.Fatalf("AdminUnsuspendAccount lifted %d suspensions, want %d", n, want)
		}
	}
	wantInboxes := "[https://remote.example/inbox https://remote.example/users/erin/inbox]"

	t.Run("local account", func(t *testing.T) {
		var suspensionIDs []string
		for _, d := range suspend(t, alice) {
			suspensionIDs = append(suspensionIDs, d.activity.ID)
		}
		lift(t, alice, 1)

		var firstIDs []string
		for attempt := 1; attempt <= 2; attempt++ {
			var inboxes, ids []string
			for _, d := range unsuspend(t, alice) {
				actor, _ := d.activity.ActivityObject.(map[string]any)
				if d.activity.Type != activitypub.TypeUpdate || d.signer != "alice" || d.suspended {
					t.Fatalf("attempt %d: delivery = %s by %s (suspended %v)", attempt, d.activity.Type, d.signer, d.suspended)
				}
				if actor["id"] != "https://local.test/users/alice" || actor["name"] != "Display alice" ||
					actor["summary"] != "bio" || actor["discoverable"] != true {
					t.Fatalf("attempt %d: actor = %v, want alice's profile", attempt, actor)
				}
				if _, ok := actor["suspended"]; ok {
					t.Fatalf("attempt %d: actor is marked suspended: %v", attempt, actor)
				}
				inboxes = append(inboxes, d.inbox)
				ids = append(ids, d.activity.ID)
			}
			if fmt.Sprint(inboxes) != wantInboxes {
				t.Fatalf("attempt %d: inboxes = %v, want %s", attempt, inboxes, wantInboxes)
			}
			if fmt.Sprint(ids) == fmt.Sprint(suspensionIDs) {
				t.Fatalf("attempt %d: unsuspension reuses the suspension's activity ids %v", attempt, ids)
			}
			if attempt == 1 {
				firstIDs = ids
			} else if fmt.Sprint(ids) != fmt.Sprint(firstIDs) {
				t.Fatalf("retry sent activities %v, want the same %v", ids, firstIDs)
			}
		}

		// Unsuspending again lifts nothing.
		lift(t, alice, 0)
	})

	t.Run("account suspended again", func(t *testing.T) {
		suspend(t, alice)
		if got := unsuspend(t, alice); len(got) != 0 {
			t.Fatalf("delivered %d activities for a suspended account", len(got))
		}
	})

	t.Run("account whose deletion finished", func(t *testing.T) {
		suspend(t, dora)
		if err := queries.DeleteAccountDeletionRequest(ctx, dora); err != nil {
			t.Fatalf("finish the deletion of dora: %v", err)
		}
		lift(t, dora, 0)
		if got := unsuspend(t, dora); len(got) != 0 {
			t.Fatalf("delivered %d activities for a deleted account", len(got))
		}
	})
}

// TestAccountUnsuspensionWaitsForItsTransaction runs the unsuspension's
// side effects, as a worker that picks up the task at once does, while the
// transaction that lifted the suspension and queued them is still open:
// they wait for it to end, deliver nothing when it rolls back, and deliver
// the actor once it commits.
func TestAccountUnsuspensionWaitsForItsTransaction(t *testing.T) {
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
	if err := queries.AdminSuspendAccount(ctx, alice); err != nil {
		t.Fatalf("suspend alice: %v", err)
	}

	enqueuer := &taskEnqueuer{}
	unsuspension := services.NewAccountUnsuspensionService(db.New(workerConn), federation.NewOutgoingService(db.New(workerConn), "local.test", enqueuer))

	// unsuspendIn lifts the suspension in a transaction, runs the side
	// effects meanwhile, ends the transaction with end, and returns the
	// tasks the side effects queued.
	unsuspendIn := func(t *testing.T, end func(pgx.Tx) error) []*asynq.Task {
		t.Helper()
		enqueuer.tasks = nil
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if n, err := queries.WithTx(tx).AdminUnsuspendAccount(ctx, alice); err != nil || n != 1 {
			t.Fatalf("AdminUnsuspendAccount: %d, %v; want 1 account", n, err)
		}

		done := make(chan error, 1)
		go func() { done <- unsuspension.Unsuspend(ctx, alice) }()
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
				t.Fatalf("side effects ended (%v) while the unsuspension's transaction was open", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("the side effects never waited for the unsuspension's transaction")
			}
			time.Sleep(10 * time.Millisecond)
		}

		if err := end(tx); err != nil {
			t.Fatalf("end the transaction: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Unsuspend: %v", err)
		}
		return enqueuer.tasks
	}

	if tasks := unsuspendIn(t, func(tx pgx.Tx) error { return tx.Rollback(ctx) }); len(tasks) != 0 {
		t.Fatalf("rolled-back unsuspension queued %d deliveries, want none", len(tasks))
	}
	tasks := unsuspendIn(t, func(tx pgx.Tx) error { return tx.Commit(ctx) })
	if len(tasks) != 1 {
		t.Fatalf("committed unsuspension queued %d deliveries, want the actor Update to bob", len(tasks))
	}
	var payload DeliverActivityPayload
	if err := json.Unmarshal(tasks[0].Payload(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	var activity activitypub.Activity
	if err := json.Unmarshal(payload.Activity, &activity); err != nil {
		t.Fatalf("activity: %v", err)
	}
	if payload.LocalAccountID != alice || payload.Inbox != "https://remote.example/users/bob/inbox" || activity.Type != activitypub.TypeUpdate {
		t.Fatalf("delivery = %s by %d to %s, want alice's Update to bob's inbox", activity.Type, payload.LocalAccountID, payload.Inbox)
	}
}
