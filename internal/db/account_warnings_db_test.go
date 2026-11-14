package db

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// strikeFixture is a database with accounts, reports and a warning preset
// for the account action queries.
type strikeFixture struct {
	conn *pgx.Conn
	q    *Queries
	t    *testing.T

	moderator, target, other int64
}

func newStrikeFixture(t *testing.T) *strikeFixture {
	conn := openMigratedDB(t)
	f := &strikeFixture{conn: conn, q: New(conn), t: t}
	f.moderator = f.account("mod", nil)
	f.target = f.account("alice", nil)
	remote := "remote.example"
	f.other = f.account("bob", &remote)
	return f
}

func (f *strikeFixture) account(username string, domain *string) int64 {
	f.t.Helper()
	var id int64
	if err := f.conn.QueryRow(context.Background(),
		`INSERT INTO accounts (username, domain, uri) VALUES ($1::text, $2, 'https://example.com/users/' || $1::text) RETURNING id`,
		username, domain,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert account %s: %v", username, err)
	}
	return id
}

// report inserts a report about target, resolved when resolved is set.
func (f *strikeFixture) report(target int64, statusIDs []int64, resolved bool) int64 {
	f.t.Helper()
	var id int64
	if err := f.conn.QueryRow(context.Background(),
		`INSERT INTO reports (account_id, target_account_id, status_ids, action_taken_at)
		 VALUES ($1, $2, $3, CASE WHEN $4::boolean THEN now() END) RETURNING id`,
		f.moderator, target, statusIDs, resolved,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert report: %v", err)
	}
	return id
}

func (f *strikeFixture) status(account int64, url *string, deleted bool) int64 {
	f.t.Helper()
	var id int64
	if err := f.conn.QueryRow(context.Background(),
		`INSERT INTO statuses (account_id, text, uri, url, deleted_at)
		 VALUES ($1, 'hello', 'https://example.com/statuses/' || gen_random_uuid(), $2, CASE WHEN $3::boolean THEN now() END)
		 RETURNING id`,
		account, url, deleted,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert status: %v", err)
	}
	return id
}

func TestAdminGetAccountWarningPreset(t *testing.T) {
	f := newStrikeFixture(t)
	ctx := context.Background()

	var id int64
	if err := f.conn.QueryRow(ctx,
		`INSERT INTO account_warning_presets (title, text) VALUES ('Spam', 'Stop it.') RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("insert preset: %v", err)
	}

	preset, err := f.q.AdminGetAccountWarningPreset(ctx, id)
	if err != nil {
		t.Fatalf("AdminGetAccountWarningPreset() error = %v", err)
	}
	if preset.ID != id || preset.Title != "Spam" || preset.Text != "Stop it." {
		t.Fatalf("preset = %+v", preset)
	}
	if _, err := f.q.AdminGetAccountWarningPreset(ctx, id+1); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown preset: error = %v, want pgx.ErrNoRows", err)
	}
}

func TestAdminListUnresolvedReportsByTarget(t *testing.T) {
	f := newStrikeFixture(t)
	ctx := context.Background()

	second := f.report(f.target, []int64{2}, false)
	first := f.report(f.target, []int64{1}, false)
	f.report(f.target, []int64{}, true)
	f.report(f.other, []int64{}, false)
	// first was inserted after second, so it has the higher ID.
	want := []int64{second, first}

	// The lock needs a transaction, as an account action runs it.
	tx, err := f.conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	reports, err := f.q.WithTx(tx).AdminListUnresolvedReportsByTarget(ctx, f.target)
	if err != nil {
		t.Fatalf("AdminListUnresolvedReportsByTarget() error = %v", err)
	}
	var got []int64
	for _, report := range reports {
		got = append(got, report.ID)
		if report.TargetAccountID != f.target || report.ActionTakenAt.Valid {
			t.Errorf("report %+v isn't an unresolved report about %d", report, f.target)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("reports = %v, want %v", got, want)
	}

	none, err := f.q.WithTx(tx).AdminListUnresolvedReportsByTarget(ctx, f.moderator)
	if err != nil || len(none) != 0 {
		t.Fatalf("reports about an unreported account = %v, %v, want none", none, err)
	}
}

func TestCreateAccountWarningAndRender(t *testing.T) {
	f := newStrikeFixture(t)
	ctx := context.Background()
	report := f.report(f.target, []int64{5}, false)

	tests := []struct {
		name string
		arg  CreateAccountWarningParams
	}{
		{
			name: "without a report",
			arg: CreateAccountWarningParams{
				AccountID:       pgtype.Int8{Int64: f.moderator, Valid: true},
				TargetAccountID: pgtype.Int8{Int64: f.target, Valid: true},
				Action:          3000,
				Text:            "Limited.",
			},
		},
		{
			name: "with a report",
			arg: CreateAccountWarningParams{
				AccountID:       pgtype.Int8{Int64: f.moderator, Valid: true},
				TargetAccountID: pgtype.Int8{Int64: f.target, Valid: true},
				ReportID:        pgtype.Int8{Int64: report, Valid: true},
				Action:          0,
				StatusIds:       []string{"5", "6"},
			},
		},
		{
			name: "with a report without statuses",
			arg: CreateAccountWarningParams{
				TargetAccountID: pgtype.Int8{Int64: f.target, Valid: true},
				ReportID:        pgtype.Int8{Int64: report, Valid: true},
				Action:          4000,
				StatusIds:       []string{},
			},
		},
	}

	var ids []int64
	for _, tt := range tests {
		warning, err := f.q.CreateAccountWarning(ctx, tt.arg)
		if err != nil {
			t.Fatalf("%s: CreateAccountWarning() error = %v", tt.name, err)
		}
		ids = append(ids, warning.ID)
		if warning.AccountID != tt.arg.AccountID || warning.TargetAccountID != tt.arg.TargetAccountID ||
			warning.ReportID != tt.arg.ReportID || warning.Action != tt.arg.Action || warning.Text != tt.arg.Text {
			t.Errorf("%s: warning = %+v, want %+v", tt.name, warning, tt.arg)
		}
		// NULL stays NULL, and an empty list stays empty.
		if (warning.StatusIds == nil) != (tt.arg.StatusIds == nil) || !slices.Equal(warning.StatusIds, tt.arg.StatusIds) {
			t.Errorf("%s: status IDs = %#v, want %#v", tt.name, warning.StatusIds, tt.arg.StatusIds)
		}
		if !warning.CreatedAt.Valid || warning.OverruledAt.Valid {
			t.Errorf("%s: created %v, overruled %v", tt.name, warning.CreatedAt, warning.OverruledAt)
		}
	}

	// Appeals: pending on the first strike, approved on the second.
	if _, err := f.conn.Exec(ctx,
		`INSERT INTO appeals (account_id, account_warning_id, text) VALUES ($1, $2, 'Sorry')`, f.target, ids[0],
	); err != nil {
		t.Fatalf("insert appeal: %v", err)
	}
	if _, err := f.conn.Exec(ctx,
		`INSERT INTO appeals (account_id, account_warning_id, text, approved_at) VALUES ($1, $2, 'Mistake', now())`, f.target, ids[1],
	); err != nil {
		t.Fatalf("insert appeal: %v", err)
	}

	rows, err := f.q.GetAccountWarningsByIDs(ctx, append(slices.Clone(ids), ids[2]+100))
	if err != nil {
		t.Fatalf("GetAccountWarningsByIDs() error = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want the three strikes", rows)
	}
	for i, row := range rows {
		if row.ID != ids[i] || row.TargetAccountID.Int64 != f.target || row.Action != tests[i].arg.Action {
			t.Errorf("row %d = %+v", i, row)
		}
	}
	if !rows[0].AppealID.Valid || rows[0].AppealText.String != "Sorry" || rows[0].AppealApprovedAt.Valid || rows[0].AppealRejectedAt.Valid {
		t.Errorf("first strike's appeal = %+v, want a pending one", rows[0])
	}
	if !rows[1].AppealID.Valid || rows[1].AppealText.String != "Mistake" || !rows[1].AppealApprovedAt.Valid {
		t.Errorf("second strike's appeal = %+v, want an approved one", rows[1])
	}
	if rows[2].AppealID.Valid || rows[2].AppealText.Valid {
		t.Errorf("third strike's appeal = %+v, want none", rows[2])
	}
	if rows[0].StatusIds != nil || !slices.Equal(rows[1].StatusIds, []string{"5", "6"}) || rows[2].StatusIds == nil {
		t.Errorf("status IDs = %#v, %#v, %#v", rows[0].StatusIds, rows[1].StatusIds, rows[2].StatusIds)
	}
}

func TestAdminListCitedStatuses(t *testing.T) {
	f := newStrikeFixture(t)
	ctx := context.Background()

	url := "https://remote.example/@bob/1"
	local := f.status(f.target, nil, false)
	deleted := f.status(f.target, nil, true)
	remote := f.status(f.other, &url, false)

	rows, err := f.q.AdminListCitedStatuses(ctx, []int64{remote, deleted, local, remote + 100})
	if err != nil {
		t.Fatalf("AdminListCitedStatuses() error = %v", err)
	}
	want := []AdminListCitedStatusesRow{
		{ID: local, Username: "alice"},
		{ID: deleted, Username: "alice"},
		{ID: remote, Username: "bob", Url: pgtype.Text{String: url, Valid: true}, Domain: pgtype.Text{String: "remote.example", Valid: true}},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %d", rows, len(want))
	}
	for i, row := range rows {
		row.Uri = pgtype.Text{}
		if row != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, row, want[i])
		}
	}
}
