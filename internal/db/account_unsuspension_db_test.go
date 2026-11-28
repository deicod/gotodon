package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestAdminUnsuspendAccountLiftsTemporarySuspensions checks that
// AdminUnsuspendAccount lifts only a suspension that this server made of an
// account whose deletion hasn't finished, and reports what it lifted: a
// deleted account, one that its own server suspended or deleted, and one
// that isn't suspended stay as they are.
func TestAdminUnsuspendAccountLiftsTemporarySuspensions(t *testing.T) {
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
		return exec(`INSERT INTO accounts (username, domain, uri) VALUES ($1, NULLIF($2, ''), $3) RETURNING id`,
			username, domain, "https://"+username+".example/actor")
	}
	suspend := func(id int64) {
		t.Helper()
		if err := q.AdminSuspendAccount(ctx, id); err != nil {
			t.Fatalf("AdminSuspendAccount(%d): %v", id, err)
		}
	}

	temporary := account("alice", "")
	suspend(temporary)
	remote := account("rob", "remote.example")
	suspend(remote)
	deleted := account("dora", "")
	suspend(deleted)
	if err := q.DeleteAccountDeletionRequest(ctx, deleted); err != nil {
		t.Fatalf("finish the deletion of dora: %v", err)
	}
	deletedAtOrigin := account("rita", "remote.example")
	suspend(deletedAtOrigin)
	if err := q.SuspendAccount(ctx, SuspendAccountParams{ID: deletedAtOrigin}); err != nil {
		t.Fatalf("delete rita at her origin: %v", err)
	}
	active := account("ava", "")

	type state struct {
		suspended bool
		origin    pgtype.Int4
		requests  int64
	}
	stateOf := func(id int64) state {
		t.Helper()
		var s state
		if err := conn.QueryRow(ctx, `SELECT a.suspended_at IS NOT NULL, a.suspension_origin,
			(SELECT count(*) FROM account_deletion_requests d WHERE d.account_id = a.id)
			FROM accounts a WHERE a.id = $1`, id).Scan(&s.suspended, &s.origin, &s.requests); err != nil {
			t.Fatalf("state of %d: %v", id, err)
		}
		return s
	}
	local := pgtype.Int4{Int32: 0, Valid: true}

	tests := []struct {
		name    string
		id      int64
		wantN   int64
		wantNow state
	}{
		{name: "local account suspended temporarily", id: temporary, wantN: 1, wantNow: state{}},
		{name: "remote account suspended temporarily", id: remote, wantN: 1, wantNow: state{}},
		{name: "account whose deletion finished", id: deleted, wantNow: state{suspended: true, origin: local}},
		{name: "remote account deleted at its origin", id: deletedAtOrigin, wantNow: state{suspended: true, requests: 1}},
		{name: "account that isn't suspended", id: active, wantNow: state{}},
		{name: "account unsuspended already", id: temporary, wantNow: state{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := q.AdminUnsuspendAccount(ctx, tt.id)
			if err != nil {
				t.Fatalf("AdminUnsuspendAccount: %v", err)
			}
			if n != tt.wantN {
				t.Fatalf("unsuspended %d accounts, want %d", n, tt.wantN)
			}
			if got := stateOf(tt.id); got != tt.wantNow {
				t.Fatalf("state = %+v, want %+v", got, tt.wantNow)
			}
		})
	}
}

// TestAdminUnsuspendAccountLosesToAFinishingDeletion runs an unsuspension
// while the last step of a deletion, which deletes the deletion request,
// holds its claim on the account: the unsuspension waits for the step, then
// finds the deletion finished and changes nothing. The deleted account
// stays deleted.
func TestAdminUnsuspendAccountLosesToAFinishingDeletion(t *testing.T) {
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
	moderator, observer := connect(), connect()

	var alice int64
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (username, uri) VALUES ('alice', 'https://local.test/users/alice') RETURNING id`).Scan(&alice); err != nil {
		t.Fatalf("insert alice: %v", err)
	}
	if err := q.AdminSuspendAccount(ctx, alice); err != nil {
		t.Fatalf("suspend alice: %v", err)
	}

	// The deletion's last step: claim, then delete the request.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	step := q.WithTx(tx)
	if _, err := step.ClaimAccountDeletion(ctx, alice); err != nil {
		t.Fatalf("ClaimAccountDeletion: %v", err)
	}
	if err := step.DeleteAccountDeletionRequest(ctx, alice); err != nil {
		t.Fatalf("DeleteAccountDeletionRequest: %v", err)
	}

	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := New(moderator).AdminUnsuspendAccount(ctx, alice)
		done <- result{n, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int64
		if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock'`, moderator.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if waiting == 1 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("unsuspension returned (%d, %v) while the deletion's step held its claim", r.n, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the unsuspension never waited for the deletion's step")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the deletion's step: %v", err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("AdminUnsuspendAccount: %v", r.err)
	}
	if r.n != 0 {
		t.Fatalf("unsuspended %d accounts after the deletion finished, want 0", r.n)
	}
	var suspended bool
	if err := conn.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM accounts WHERE id = $1`, alice).Scan(&suspended); err != nil {
		t.Fatalf("load alice: %v", err)
	}
	if !suspended {
		t.Fatal("the deleted account was unsuspended")
	}
}

// TestUnsuspensionRestoresDirectoryAndLookups checks that lifting a
// suspension brings the account back to the profile directory, account
// search and the lookups of its actor and WebFinger, from which the
// suspension took it, and that a deleted account doesn't come back.
func TestUnsuspensionRestoresDirectoryAndLookups(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()
	insert := func(username string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx, `INSERT INTO accounts (username, uri, display_name, discoverable)
			VALUES ($1, $2, 'Wanderer', true) RETURNING id`, username, "https://local.test/users/"+username).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", username, err)
		}
		return id
	}
	alice, dora := insert("alice"), insert("dora")

	// visible reports where each account is found.
	visible := func() map[int64][]string {
		t.Helper()
		found := map[int64][]string{}
		directory, err := q.ListDeepDirectory(ctx, ListDeepDirectoryParams{Limit: 10, Local: true, OrderNew: true})
		if err != nil {
			t.Fatalf("ListDeepDirectory: %v", err)
		}
		for _, account := range directory {
			found[account.ID] = append(found[account.ID], "directory")
		}
		search, err := q.SearchAccounts(ctx, SearchAccountsParams{Limit: 10, Query: "wander"})
		if err != nil {
			t.Fatalf("SearchAccounts: %v", err)
		}
		for _, account := range search {
			found[account.ID] = append(found[account.ID], "search")
		}
		for _, username := range []string{"alice", "dora"} {
			account, err := q.GetAccountByUsername(ctx, username)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				t.Fatalf("GetAccountByUsername(%s): %v", username, err)
			}
			found[account.ID] = append(found[account.ID], "actor")
		}
		return found
	}
	everywhere := []string{"directory", "search", "actor"}
	check := func(stage string, want map[int64][]string) {
		t.Helper()
		got := visible()
		for _, id := range []int64{alice, dora} {
			if !equalStrings(got[id], want[id]) {
				t.Errorf("%s: account %d found in %v, want %v", stage, id, got[id], want[id])
			}
		}
	}

	check("before suspension", map[int64][]string{alice: everywhere, dora: everywhere})
	for _, id := range []int64{alice, dora} {
		if err := q.AdminSuspendAccount(ctx, id); err != nil {
			t.Fatalf("AdminSuspendAccount(%d): %v", id, err)
		}
	}
	if err := q.DeleteAccountDeletionRequest(ctx, dora); err != nil {
		t.Fatalf("finish the deletion of dora: %v", err)
	}
	check("after suspension", map[int64][]string{})

	for _, id := range []int64{alice, dora} {
		if _, err := q.AdminUnsuspendAccount(ctx, id); err != nil {
			t.Fatalf("AdminUnsuspendAccount(%d): %v", id, err)
		}
	}
	check("after unsuspension", map[int64][]string{alice: everywhere})
}

// TestSuspendAccountKeepsSuspensionTime checks that suspending an account
// that is suspended already changes its origin and keeps the time of its
// suspension.
func TestSuspendAccountKeepsSuspensionTime(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()
	suspendedAt := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var rob int64
	if err := conn.QueryRow(ctx, `INSERT INTO accounts (username, domain, uri, suspended_at, suspension_origin)
		VALUES ('rob', 'remote.example', 'https://remote.example/users/rob', $1, 0) RETURNING id`, suspendedAt).Scan(&rob); err != nil {
		t.Fatalf("insert rob: %v", err)
	}
	if err := q.SuspendAccount(ctx, SuspendAccountParams{ID: rob}); err != nil {
		t.Fatalf("SuspendAccount: %v", err)
	}
	account, err := q.GetAccountByIDAllowSuspended(ctx, rob)
	if err != nil {
		t.Fatalf("load rob: %v", err)
	}
	if !account.SuspendedAt.Valid || !account.SuspendedAt.Time.Equal(suspendedAt) {
		t.Fatalf("suspended_at = %+v, want %v", account.SuspendedAt, suspendedAt)
	}
	if account.SuspensionOrigin.Valid {
		t.Fatalf("suspension_origin = %+v, want NULL", account.SuspensionOrigin)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
