package db

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// reportFixture is a database with local accounts alice and bob and accounts
// of remote.example and third.example, for the queries behind inbound Flags.
type reportFixture struct {
	conn *pgx.Conn
	q    *Queries
	t    *testing.T

	alice, bob, remote, third int64
}

func newReportFixture(t *testing.T) *reportFixture {
	conn := openMigratedDB(t)
	f := &reportFixture{conn: conn, q: New(conn), t: t}
	f.alice = f.account("alice", "")
	f.bob = f.account("bob", "")
	// Stored as the actor URI spelled its host; domains compare without case.
	f.remote = f.account("remote.example", "Remote.Example")
	f.third = f.account("carol", "third.example")
	return f
}

func (f *reportFixture) account(username, domain string) int64 {
	f.t.Helper()
	host := domain
	if host == "" {
		host = "local.test"
	}
	var id int64
	if err := f.conn.QueryRow(context.Background(),
		`INSERT INTO accounts (username, domain, uri) VALUES ($1::text, NULLIF($2::text, ''), 'https://' || $3::text || '/users/' || $1::text) RETURNING id`,
		username, domain, host,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert account %s: %v", username, err)
	}
	return id
}

func (f *reportFixture) status(account int64, visibility int32, deleted bool) int64 {
	f.t.Helper()
	var id int64
	if err := f.conn.QueryRow(context.Background(),
		`INSERT INTO statuses (account_id, text, uri, visibility, deleted_at)
		 VALUES ($1, 'hello', 'https://local.test/statuses/' || gen_random_uuid(), $2, CASE WHEN $3::boolean THEN now() END)
		 RETURNING id`,
		account, visibility, deleted,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert status: %v", err)
	}
	return id
}

func (f *reportFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

func TestCreateRemoteReport(t *testing.T) {
	f := newReportFixture(t)
	ctx := context.Background()
	status := f.status(f.alice, 0, false)

	report, err := f.q.CreateRemoteReport(ctx, CreateRemoteReportParams{
		AccountID:       f.remote,
		TargetAccountID: f.alice,
		StatusIds:       []int64{status},
		Comment:         "Spam",
		Uri:             "https://remote.example/flag-1",
	})
	if err != nil {
		t.Fatalf("CreateRemoteReport() error = %v", err)
	}
	if report.AccountID != f.remote || report.TargetAccountID != f.alice || !slices.Equal(report.StatusIds, []int64{status}) {
		t.Fatalf("report = %+v", report)
	}
	if report.Comment != "Spam" || report.Category != 0 || report.Uri != (pgtype.Text{String: "https://remote.example/flag-1", Valid: true}) {
		t.Fatalf("report = %+v", report)
	}
	if report.Forwarded != (pgtype.Bool{Bool: false, Valid: true}) || report.ActionTakenAt.Valid || report.RuleIds != nil {
		t.Fatalf("report = %+v, want unforwarded, unresolved and without rules", report)
	}
	listed, err := f.q.AdminListReports(ctx, AdminListReportsParams{Limit: 10, TargetAccountID: f.alice})
	if err != nil {
		t.Fatalf("AdminListReports() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != report.ID || listed[0].AccountDomain.String != "Remote.Example" {
		t.Fatalf("unresolved reports of alice = %+v, want the remote report", listed)
	}

	// The same Flag again files nothing, even with other statuses.
	_, err = f.q.CreateRemoteReport(ctx, CreateRemoteReportParams{
		AccountID: f.remote, TargetAccountID: f.alice, StatusIds: []int64{}, Comment: "Spam",
		Uri: "https://remote.example/flag-1",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate CreateRemoteReport() error = %v, want pgx.ErrNoRows", err)
	}

	// Another target of the same Flag, another Flag of the same target, and
	// the same id from another reporter are reports of their own.
	for _, arg := range []CreateRemoteReportParams{
		{AccountID: f.remote, TargetAccountID: f.bob, StatusIds: []int64{}, Uri: "https://remote.example/flag-1"},
		{AccountID: f.remote, TargetAccountID: f.alice, StatusIds: []int64{}, Uri: "https://remote.example/flag-2"},
		{AccountID: f.third, TargetAccountID: f.alice, StatusIds: []int64{}, Uri: "https://remote.example/flag-1"},
	} {
		if _, err := f.q.CreateRemoteReport(ctx, arg); err != nil {
			t.Fatalf("CreateRemoteReport(%+v) error = %v", arg, err)
		}
	}

	// Reports without a uri, as POST /api/v1/reports files them, may repeat.
	for i := 0; i < 2; i++ {
		if _, err := f.q.CreateReport(ctx, CreateReportParams{
			AccountID: f.bob, TargetAccountID: f.alice, StatusIds: []int64{}, Forwarded: pgtype.Bool{Valid: true},
		}); err != nil {
			t.Fatalf("CreateReport() %d error = %v", i+1, err)
		}
	}

	var count int
	if err := f.conn.QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&count); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if count != 6 {
		t.Fatalf("reports = %d, want 6", count)
	}
}

func TestListReportableStatusIDs(t *testing.T) {
	f := newReportFixture(t)
	ctx := context.Background()

	public := f.status(f.alice, 0, false)
	unlisted := f.status(f.alice, 1, false)
	private := f.status(f.alice, 2, false)
	direct := f.status(f.alice, 3, false)
	mentioning := f.status(f.alice, 3, false)
	mentioningOther := f.status(f.alice, 3, false)
	deleted := f.status(f.alice, 0, true)
	bobs := f.status(f.bob, 0, false)
	f.exec(`INSERT INTO mentions (status_id, account_id) VALUES ($1, $2), ($3, $4)`, mentioning, f.remote, mentioningOther, f.third)
	named := []int64{bobs, deleted, mentioningOther, mentioning, direct, private, unlisted, public, 999999}

	list := func(domain string) []int64 {
		t.Helper()
		ids, err := f.q.ListReportableStatusIDs(ctx, ListReportableStatusIDsParams{
			AccountID: f.alice, StatusIds: named, Domain: domain,
		})
		if err != nil {
			t.Fatalf("ListReportableStatusIDs(%q) error = %v", domain, err)
		}
		return ids
	}

	if got, want := list("remote.example"), []int64{public, unlisted, mentioning}; !slices.Equal(got, want) {
		t.Fatalf("without followers: ids = %v, want %v", got, want)
	}
	if got, want := list("unknown.example"), []int64{public, unlisted}; !slices.Equal(got, want) {
		t.Fatalf("unrelated domain: ids = %v, want %v", got, want)
	}

	// A follower on remote.example received alice's private statuses.
	f.exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2)`, f.remote, f.alice)
	if got, want := list("REMOTE.example"), []int64{public, unlisted, private, mentioning}; !slices.Equal(got, want) {
		t.Fatalf("with a follower: ids = %v, want %v", got, want)
	}
	// Following bob doesn't let a server see alice's.
	f.exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2)`, f.third, f.bob)
	if got, want := list("third.example"), []int64{public, unlisted, mentioningOther}; !slices.Equal(got, want) {
		t.Fatalf("follower of another account: ids = %v, want %v", got, want)
	}

	ids, err := f.q.ListReportableStatusIDs(ctx, ListReportableStatusIDsParams{
		AccountID: f.alice, StatusIds: []int64{}, Domain: "remote.example",
	})
	if err != nil || len(ids) != 0 {
		t.Fatalf("no statuses: ids = %v, error = %v", ids, err)
	}
}

func TestDomainRejectsReports(t *testing.T) {
	f := newReportFixture(t)
	ctx := context.Background()

	rejects := func(domains ...string) bool {
		t.Helper()
		got, err := f.q.DomainRejectsReports(ctx, domains)
		if err != nil {
			t.Fatalf("DomainRejectsReports(%v) error = %v", domains, err)
		}
		return got
	}

	if rejects("remote.example", "example") {
		t.Fatal("no blocks: reports rejected")
	}
	if rejects() {
		t.Fatal("no domains: reports rejected")
	}

	f.exec(`INSERT INTO domain_blocks (domain, severity, reject_reports) VALUES ('Example', 0, true), ('kept.example', 1, false), ('other.test', 2, true)`)
	tests := []struct {
		domains []string
		want    bool
	}{
		{domains: []string{"example"}, want: true},
		{domains: []string{"remote.example", "example"}, want: true},
		{domains: []string{"kept.example", "example"}, want: false},
		{domains: []string{"sub.kept.example", "kept.example", "example"}, want: false},
		{domains: []string{"third.test", "test"}, want: false},
	}
	for _, tt := range tests {
		if got := rejects(tt.domains...); got != tt.want {
			t.Errorf("DomainRejectsReports(%v) = %v, want %v", tt.domains, got, tt.want)
		}
	}
}
