package db

import (
	"context"
	"testing"
)

// TestRepairRegisteredAccountURIs moves local accounts that registration
// stored under https://example.com/users/<username> to the configured
// domain, and leaves every other account alone.
func TestRepairRegisteredAccountURIs(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()

	insert := func(username string, domain *string, uri string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx,
			`INSERT INTO accounts (username, domain, uri) VALUES ($1, $2, $3) RETURNING id`,
			username, domain, uri,
		).Scan(&id); err != nil {
			t.Fatalf("insert account %s: %v", username, err)
		}
		return id
	}
	remote := "example.com"
	broken := insert("alice", nil, "https://example.com/users/alice")
	brokenToo := insert("bob", nil, "https://example.com/users/bob")
	correct := insert("carol", nil, "https://social.example/users/carol")
	mismatched := insert("dave", nil, "https://example.com/users/someone-else")
	remoteAccount := insert("erin", &remote, "https://example.com/users/erin")

	uriOf := func(id int64) string {
		t.Helper()
		var uri string
		if err := conn.QueryRow(ctx, `SELECT uri FROM accounts WHERE id = $1`, id).Scan(&uri); err != nil {
			t.Fatalf("select account %d: %v", id, err)
		}
		return uri
	}

	// An instance whose domain is example.com has nothing to repair.
	if n, err := q.RepairRegisteredAccountURIs(ctx, "example.com"); err != nil || n != 0 {
		t.Fatalf("repair on example.com = %d, %v; want 0, nil", n, err)
	}
	if got := uriOf(broken); got != "https://example.com/users/alice" {
		t.Fatalf("alice changed to %q on an example.com instance", got)
	}

	n, err := q.RepairRegisteredAccountURIs(ctx, "social.example")
	if err != nil {
		t.Fatalf("RepairRegisteredAccountURIs: %v", err)
	}
	if n != 2 {
		t.Fatalf("repaired %d accounts, want 2", n)
	}
	want := map[int64]string{
		broken:        "https://social.example/users/alice",
		brokenToo:     "https://social.example/users/bob",
		correct:       "https://social.example/users/carol",
		mismatched:    "https://example.com/users/someone-else",
		remoteAccount: "https://example.com/users/erin",
	}
	for id, uri := range want {
		if got := uriOf(id); got != uri {
			t.Errorf("account %d uri = %q, want %q", id, got, uri)
		}
	}

	if n, err := q.RepairRegisteredAccountURIs(ctx, "social.example"); err != nil || n != 0 {
		t.Fatalf("second repair = %d, %v; want 0, nil", n, err)
	}
}
