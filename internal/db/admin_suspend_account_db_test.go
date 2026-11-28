package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestAdminSuspendAccountSuspendsOnce checks that AdminSuspendAccount
// suspends only an account that isn't suspended, and reports whether it
// did: an account suspended already, here or by its own server, keeps its
// suspension, its updated_at and its deletion requests as they were, so a
// repeated suspension federates nothing new and a deleted account can't be
// unsuspended.
func TestAdminSuspendAccountSuspendsOnce(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()
	exec := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	account := func(username, domain string) int64 {
		return exec(`INSERT INTO accounts (username, domain, uri, updated_at)
			VALUES ($1, NULLIF($2, ''), $3, '2000-01-01T00:00:00Z') RETURNING id`,
			username, domain, "https://"+username+".example/actor")
	}
	suspend := func(id int64) {
		t.Helper()
		if n, err := q.AdminSuspendAccount(ctx, id); err != nil || n != 1 {
			t.Fatalf("AdminSuspendAccount(%d) = %d, %v; want 1 account", id, n, err)
		}
		if _, err := conn.Exec(ctx, `UPDATE accounts SET suspended_at = '2000-02-01T00:00:00Z', updated_at = '2000-02-01T00:00:00Z' WHERE id = $1`, id); err != nil {
			t.Fatalf("backdate the suspension of %d: %v", id, err)
		}
	}

	active := account("ava", "")
	remote := account("rob", "remote.example")
	temporary := account("alice", "")
	suspend(temporary)
	deleted := account("dora", "")
	suspend(deleted)
	if err := q.DeleteAccountDeletionRequest(ctx, deleted); err != nil {
		t.Fatalf("finish the deletion of dora: %v", err)
	}
	deletedAtOrigin := account("rita", "remote.example")
	if err := q.SuspendAccount(ctx, SuspendAccountParams{ID: deletedAtOrigin}); err != nil {
		t.Fatalf("delete rita at her origin: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE accounts SET suspended_at = '2000-02-01T00:00:00Z', updated_at = '2000-02-01T00:00:00Z' WHERE id = $1`, deletedAtOrigin); err != nil {
		t.Fatalf("backdate the deletion of rita: %v", err)
	}
	requested := account("ria", "")
	exec(`INSERT INTO account_deletion_requests (account_id, created_at, updated_at) VALUES ($1, now(), now()) RETURNING id`, requested)

	type state struct {
		suspended, recent bool
		origin            pgtype.Int4
		requests          int64
	}
	stateOf := func(id int64) state {
		t.Helper()
		var s state
		if err := conn.QueryRow(ctx, `SELECT a.suspended_at IS NOT NULL,
			a.updated_at > '2000-03-01T00:00:00Z' AND a.suspended_at IS NOT DISTINCT FROM a.updated_at,
			a.suspension_origin,
			(SELECT count(*) FROM account_deletion_requests d WHERE d.account_id = a.id)
			FROM accounts a WHERE a.id = $1`, id).Scan(&s.suspended, &s.recent, &s.origin, &s.requests); err != nil {
			t.Fatalf("state of %d: %v", id, err)
		}
		return s
	}
	local := pgtype.Int4{Int32: 0, Valid: true}
	newlySuspended := state{suspended: true, recent: true, origin: local, requests: 1}

	tests := []struct {
		name    string
		id      int64
		wantN   int64
		wantNow state
	}{
		{name: "local account", id: active, wantN: 1, wantNow: newlySuspended},
		{name: "remote account", id: remote, wantN: 1, wantNow: newlySuspended},
		{name: "account that isn't suspended with a deletion request", id: requested, wantN: 1, wantNow: newlySuspended},
		{name: "account suspended already", id: temporary, wantNow: state{suspended: true, origin: local, requests: 1}},
		{name: "account whose deletion finished", id: deleted, wantNow: state{suspended: true, origin: local}},
		{name: "remote account deleted at its origin", id: deletedAtOrigin, wantNow: state{suspended: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := q.AdminSuspendAccount(ctx, tt.id)
			if err != nil {
				t.Fatalf("AdminSuspendAccount: %v", err)
			}
			if n != tt.wantN {
				t.Fatalf("suspended %d accounts, want %d", n, tt.wantN)
			}
			if got := stateOf(tt.id); got != tt.wantNow {
				t.Fatalf("state = %+v, want %+v", got, tt.wantNow)
			}
		})
	}

	t.Run("suspended again", func(t *testing.T) {
		before := stateOf(active)
		var updatedAt time.Time
		if err := conn.QueryRow(ctx, `SELECT updated_at FROM accounts WHERE id = $1`, active).Scan(&updatedAt); err != nil {
			t.Fatalf("load ava: %v", err)
		}
		n, err := q.AdminSuspendAccount(ctx, active)
		if err != nil || n != 0 {
			t.Fatalf("AdminSuspendAccount = %d, %v; want 0 accounts", n, err)
		}
		var again time.Time
		if err := conn.QueryRow(ctx, `SELECT updated_at FROM accounts WHERE id = $1`, active).Scan(&again); err != nil {
			t.Fatalf("load ava: %v", err)
		}
		if !again.Equal(updatedAt) || stateOf(active) != before {
			t.Fatalf("a repeated suspension changed updated_at from %v to %v", updatedAt, again)
		}
	})
}

// TestAdminSuspendAccountConcurrentSuspensions runs a suspension while
// another suspension of the same account is open: the second waits for the
// first, then finds the account suspended and changes nothing, so only one
// of them queues the suspension's side effects.
func TestAdminSuspendAccountConcurrentSuspensions(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
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
	second, observer := connect(), connect()

	var alice int64
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (username, uri) VALUES ('alice', 'https://local.test/users/alice') RETURNING id`).Scan(&alice); err != nil {
		t.Fatalf("insert alice: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if n, err := q.WithTx(tx).AdminSuspendAccount(ctx, alice); err != nil || n != 1 {
		t.Fatalf("first AdminSuspendAccount = %d, %v; want 1 account", n, err)
	}

	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := New(second).AdminSuspendAccount(ctx, alice)
		done <- result{n, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int64
		if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock'`, second.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if waiting == 1 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("second suspension returned (%d, %v) while the first was open", r.n, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the second suspension never waited for the first")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the first suspension: %v", err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("second AdminSuspendAccount: %v", r.err)
	}
	if r.n != 0 {
		t.Fatalf("second suspension suspended %d accounts, want 0", r.n)
	}
	var requests int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM account_deletion_requests WHERE account_id = $1`, alice).Scan(&requests); err != nil {
		t.Fatalf("count deletion requests: %v", err)
	}
	if requests != 1 {
		t.Fatalf("alice has %d deletion requests, want 1", requests)
	}
}
