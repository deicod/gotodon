package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/web"
)

func (s *stubAccountsStore) AdminGetReport(_ context.Context, id int64) (db.Report, error) {
	if err := s.failOn["AdminGetReport"]; err != nil {
		return db.Report{}, err
	}
	report, ok := s.reports[id]
	if !ok {
		return db.Report{}, pgx.ErrNoRows
	}
	return report, nil
}

func (s *stubAccountsStore) AdminGetAccountWarningPreset(_ context.Context, id int64) (db.AccountWarningPreset, error) {
	if err := s.failOn["AdminGetAccountWarningPreset"]; err != nil {
		return db.AccountWarningPreset{}, err
	}
	preset, ok := s.presets[id]
	if !ok {
		return db.AccountWarningPreset{}, pgx.ErrNoRows
	}
	return preset, nil
}

// AdminListUnresolvedReportsByTarget answers like the real query: the
// unresolved reports about the account, by ID.
func (s *stubAccountsStore) AdminListUnresolvedReportsByTarget(_ context.Context, targetAccountID int64) ([]db.Report, error) {
	if err := s.failOn["AdminListUnresolvedReportsByTarget"]; err != nil {
		return nil, err
	}
	var reports []db.Report
	for _, report := range s.reports {
		if report.TargetAccountID == targetAccountID && !report.ActionTakenAt.Valid {
			reports = append(reports, report)
		}
	}
	slices.SortFunc(reports, func(a, b db.Report) int { return int(a.ID - b.ID) })
	return reports, nil
}

// strikeID is the ID of the nth strike created, counting from 1.
func strikeID(n int) int64 { return 500 + int64(n) }

func (s *stubAccountsStore) CreateAccountWarning(_ context.Context, arg db.CreateAccountWarningParams) (db.AccountWarning, error) {
	if err := s.failOn["CreateAccountWarning"]; err != nil {
		return db.AccountWarning{}, err
	}
	s.strikes = append(s.strikes, arg)
	return db.AccountWarning{
		ID:              strikeID(len(s.strikes)),
		AccountID:       arg.AccountID,
		TargetAccountID: arg.TargetAccountID,
		Action:          arg.Action,
		Text:            arg.Text,
		ReportID:        arg.ReportID,
		StatusIds:       arg.StatusIds,
	}, nil
}

func (s *stubAccountsStore) AdminResolveReport(_ context.Context, arg db.AdminResolveReportParams) error {
	s.changes = append(s.changes, fmt.Sprintf("AdminResolveReport(%d)", arg.ID))
	if err := s.failOn["AdminResolveReport"]; err != nil {
		return err
	}
	s.resolutions = append(s.resolutions, arg)
	return nil
}

func (s *stubAccountsStore) CreateNotification(_ context.Context, arg db.CreateNotificationParams) (db.Notification, error) {
	if err := s.failOn["CreateNotification"]; err != nil {
		return db.Notification{}, err
	}
	s.notifications = append(s.notifications, arg)
	return db.Notification{
		ID:            900 + int64(len(s.notifications)),
		AccountID:     arg.AccountID,
		FromAccountID: arg.FromAccountID,
		ActivityType:  arg.ActivityType,
		ActivityID:    arg.ActivityID,
		Type:          arg.Type,
	}, nil
}

func (s *stubAccountsStore) AdminListCitedStatuses(_ context.Context, ids []int64) ([]db.AdminListCitedStatusesRow, error) {
	if err := s.failOn["AdminListCitedStatuses"]; err != nil {
		return nil, err
	}
	var rows []db.AdminListCitedStatusesRow
	for _, row := range s.cited {
		if slices.Contains(ids, row.ID) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// stubTransactor runs a transaction on a stubAccountsStore: rolling back
// drops whatever the transaction recorded.
type stubTransactor struct {
	store     *stubAccountsStore
	beginErr  error
	commitErr error

	commits, rollbacks int
}

func (t *stubTransactor) InTx(_ context.Context, fn func(accountActionStore) error) error {
	if t.beginErr != nil {
		return t.beginErr
	}
	s := t.store
	changes, logs, strikes, resolutions := len(s.changes), len(s.logs), len(s.strikes), len(s.resolutions)
	err := fn(s)
	if err == nil {
		err = t.commitErr
	}
	if err != nil {
		s.changes, s.logs, s.strikes, s.resolutions = s.changes[:changes], s.logs[:logs], s.strikes[:strikes], s.resolutions[:resolutions]
		t.rollbacks++
		return err
	}
	t.commits++
	return nil
}

type stubMail struct {
	err   error
	mails []mailer.Mail
}

func (m *stubMail) Deliver(_ context.Context, mail mailer.Mail) error {
	if m.err != nil {
		return m.err
	}
	m.mails = append(m.mails, mail)
	return nil
}

type stubStreamer struct {
	err      error
	streamed []db.Notification
}

func (s *stubStreamer) StreamNotification(_ context.Context, notification db.Notification) error {
	s.streamed = append(s.streamed, notification)
	return s.err
}

type stubPusher struct{ pushed []db.Notification }

func (p *stubPusher) PublishNotification(_ context.Context, notification db.Notification) {
	p.pushed = append(p.pushed, notification)
}

// Reports and the warning preset of withReports.
const (
	// reportOpen is unresolved, about targetNoRole, and cites statuses 1
	// and 2.
	reportOpen int64 = 301
	// reportOpenNoStatuses is unresolved, about targetNoRole, and cites
	// nothing.
	reportOpenNoStatuses int64 = 302
	// reportResolved is resolved and about targetNoRole.
	reportResolved int64 = 303
	// reportOther is unresolved, about targetModerator, and cites status 7.
	reportOther int64 = 304
	// reportUnknown names no report.
	reportUnknown int64 = 399

	presetSpam    int64 = 401
	presetUnknown int64 = 499
	presetText          = "Please stop posting spam."
)

// withReports adds the reports and warning preset above.
func (f *accountsFixture) withReports() *accountsFixture {
	f.store.reports = map[int64]db.Report{
		reportOpen:           {ID: reportOpen, TargetAccountID: targetNoRole, StatusIds: []int64{1, 2}},
		reportOpenNoStatuses: {ID: reportOpenNoStatuses, TargetAccountID: targetNoRole, StatusIds: []int64{}},
		reportResolved: {
			ID: reportResolved, TargetAccountID: targetNoRole, StatusIds: []int64{3},
			ActionTakenAt: pgtype.Timestamptz{Time: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Valid: true},
		},
		reportOther: {ID: reportOther, TargetAccountID: targetModerator, StatusIds: []int64{7}},
	}
	f.store.presets = map[int64]db.AccountWarningPreset{presetSpam: {ID: presetSpam, Title: "Spam", Text: presetText}}
	return f
}

func (f *accountsFixture) act(account int64, body string) *httptest.ResponseRecorder {
	return f.serve(http.MethodPost, "/api/v1/admin/accounts/"+idOf(account)+"/action", body)
}

// actForm sends an account action as a form.
func (f *accountsFixture) actForm(account int64, form string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/"+idOf(account)+"/action", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withScopes(withIdentity(req, int64Ptr(actingAccount), int64Ptr(20)), adminToken)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func assertOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "{}" {
		t.Fatalf("body = %s, want {}", body)
	}
}

// resolvedReports returns the IDs of the resolved reports, checking that
// the acting account resolved each.
func resolvedReports(t *testing.T, f *accountsFixture) []int64 {
	t.Helper()
	var ids []int64
	for _, resolution := range f.store.resolutions {
		if resolution.ActionTakenByAccountID != (pgtype.Int8{Int64: actingAccount, Valid: true}) {
			t.Fatalf("report %d resolved by %v, want account %d", resolution.ID, resolution.ActionTakenByAccountID, actingAccount)
		}
		ids = append(ids, resolution.ID)
	}
	return ids
}

// TestAccountsTakeActionStrike checks that every action creates exactly one
// strike with the expected action, text, report and status IDs.
func TestAccountsTakeActionStrike(t *testing.T) {
	tests := []struct {
		name string
		body string
		// form, when set, is sent as a form instead of body.
		form       string
		target     int64
		wantAction int32
		wantText   string
		// wantReport is the strike's report; 0 is none.
		wantReport int64
		// wantStatusIDs is the strike's status IDs; nil is NULL.
		wantStatusIDs []string
	}{
		{name: "none", body: `{"type":"none"}`, wantAction: 0},
		{name: "disable", body: `{"type":"disable"}`, wantAction: 1000},
		{name: "sensitive", body: `{"type":"sensitive"}`, wantAction: 2000},
		{name: "silence", body: `{"type":"silence"}`, wantAction: 3000},
		{name: "suspend", body: `{"type":"suspend"}`, wantAction: 4000},
		{name: "remote target", body: `{"type":"suspend"}`, target: targetRemote, wantAction: 4000},
		{name: "text", body: `{"type":"none","text":"Be nice."}`, wantText: "Be nice."},
		{name: "preset", body: `{"type":"silence","warning_preset_id":"401"}`, wantAction: 3000, wantText: presetText},
		{
			name: "preset and text", body: `{"type":"silence","warning_preset_id":"401","text":"Second time."}`,
			wantAction: 3000, wantText: presetText + "\n\nSecond time.",
		},
		{name: "preset and empty text", body: `{"type":"none","warning_preset_id":"401","text":""}`, wantText: presetText},
		{name: "preset as a JSON number", body: `{"type":"none","warning_preset_id":401}`, wantText: presetText},
		{name: "null text", body: `{"type":"none","text":null}`},
		{name: "numeric text", body: `{"type":"none","text":5}`, wantText: "5"},
		{
			name: "report", body: `{"type":"none","report_id":"301"}`,
			wantReport: reportOpen, wantStatusIDs: []string{"1", "2"},
		},
		{
			name: "report as a JSON number", body: `{"type":"sensitive","report_id":301}`,
			wantAction: 2000, wantReport: reportOpen, wantStatusIDs: []string{"1", "2"},
		},
		{
			name: "report without statuses", body: `{"type":"none","report_id":"302"}`,
			wantReport: reportOpenNoStatuses, wantStatusIDs: []string{},
		},
		{
			// Mastodon doesn't require the report to be about the target.
			name: "another account's report", body: `{"type":"suspend","report_id":"304"}`,
			wantAction: 4000, wantReport: reportOther, wantStatusIDs: []string{"7"},
		},
		{name: "blank report and preset", body: `{"type":"none","report_id":"","warning_preset_id":"  "}`},
		{name: "null report and preset", body: `{"type":"none","report_id":null,"warning_preset_id":null}`},
		{
			name: "form", form: "type=silence&report_id=301&warning_preset_id=401&text=Again.",
			wantAction: 3000, wantText: presetText + "\n\nAgain.", wantReport: reportOpen, wantStatusIDs: []string{"1", "2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(ownerActor).withReports()
			target := tt.target
			if target == 0 {
				target = targetNoRole
			}
			var rec *httptest.ResponseRecorder
			if tt.form != "" {
				rec = f.actForm(target, tt.form)
			} else {
				rec = f.act(target, tt.body)
			}
			assertOK(t, rec)

			if len(f.store.strikes) != 1 {
				t.Fatalf("strikes = %v, want exactly one", f.store.strikes)
			}
			strike := f.store.strikes[0]
			if strike.AccountID != (pgtype.Int8{Int64: actingAccount, Valid: true}) {
				t.Errorf("strike by %v, want account %d", strike.AccountID, actingAccount)
			}
			if strike.TargetAccountID != (pgtype.Int8{Int64: target, Valid: true}) {
				t.Errorf("strike against %v, want account %d", strike.TargetAccountID, target)
			}
			if strike.Action != tt.wantAction {
				t.Errorf("action = %d, want %d", strike.Action, tt.wantAction)
			}
			if strike.Text != tt.wantText {
				t.Errorf("text = %q, want %q", strike.Text, tt.wantText)
			}
			if want := (pgtype.Int8{Int64: tt.wantReport, Valid: tt.wantReport != 0}); strike.ReportID != want {
				t.Errorf("report = %v, want %v", strike.ReportID, want)
			}
			if (strike.StatusIds == nil) != (tt.wantStatusIDs == nil) || !slices.Equal(strike.StatusIds, tt.wantStatusIDs) {
				t.Errorf("status IDs = %#v, want %#v", strike.StatusIds, tt.wantStatusIDs)
			}
		})
	}
}

// TestAccountsTakeActionResolvesReports checks which reports each type
// resolves: none only the given one, every other type every unresolved
// report about the target, whatever report is given.
func TestAccountsTakeActionResolvesReports(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		target   int64
		want     []int64
		wantLogs []string
	}{
		{name: "none without a report", body: `{"type":"none"}`},
		{
			name: "none with a report", body: `{"type":"none","report_id":"301"}`,
			want: []int64{reportOpen}, wantLogs: []string{"resolve:Report:301"},
		},
		{
			// Admin::AccountAction resolves the given report again.
			name: "none with a resolved report", body: `{"type":"none","report_id":"303"}`,
			want: []int64{reportResolved}, wantLogs: []string{"resolve:Report:303"},
		},
		{
			name: "none with another account's report", body: `{"type":"none","report_id":"304"}`,
			want: []int64{reportOther}, wantLogs: []string{"resolve:Report:304"},
		},
		{
			name: "silence", body: `{"type":"silence"}`,
			want:     []int64{reportOpen, reportOpenNoStatuses},
			wantLogs: []string{"silence:Account:104", "resolve:Report:301", "resolve:Report:302"},
		},
		{
			name: "silence with a report", body: `{"type":"silence","report_id":"301"}`,
			want:     []int64{reportOpen, reportOpenNoStatuses},
			wantLogs: []string{"silence:Account:104", "resolve:Report:301", "resolve:Report:302"},
		},
		{
			name: "silence with another account's report", body: `{"type":"silence","report_id":"304"}`,
			want:     []int64{reportOpen, reportOpenNoStatuses},
			wantLogs: []string{"silence:Account:104", "resolve:Report:301", "resolve:Report:302"},
		},
		{
			name: "disable", body: `{"type":"disable"}`,
			want:     []int64{reportOpen, reportOpenNoStatuses},
			wantLogs: []string{"disable:User:204", "resolve:Report:301", "resolve:Report:302"},
		},
		{
			name: "sensitive", body: `{"type":"sensitive"}`,
			want:     []int64{reportOpen, reportOpenNoStatuses},
			wantLogs: []string{"sensitive:Account:104", "resolve:Report:301", "resolve:Report:302"},
		},
		{
			name: "suspend", body: `{"type":"suspend"}`,
			want:     []int64{reportOpen, reportOpenNoStatuses},
			wantLogs: []string{"suspend:Account:104", "resolve:Report:301", "resolve:Report:302"},
		},
		{
			name: "suspend another target", body: `{"type":"suspend"}`, target: targetModerator,
			want: []int64{reportOther}, wantLogs: []string{"suspend:Account:103", "resolve:Report:304"},
		},
		{
			name: "silence a target without reports", body: `{"type":"silence"}`, target: targetPending,
			wantLogs: []string{"silence:Account:110"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(ownerActor).withReports()
			target := tt.target
			if target == 0 {
				target = targetNoRole
			}
			assertOK(t, f.act(target, tt.body))

			if got := resolvedReports(t, f); !slices.Equal(got, tt.want) {
				t.Errorf("resolved %v, want %v", got, tt.want)
			}
			if got := f.store.loggedActions(); !slices.Equal(got, tt.wantLogs) {
				t.Errorf("logged %v, want %v", got, tt.wantLogs)
			}
			for _, entry := range f.store.logs {
				if entry.Action != "resolve" {
					continue
				}
				if want := fmt.Sprint(entry.TargetID.Int64); entry.HumanIdentifier != (pgtype.Text{String: want, Valid: true}) || entry.RouteParam.Valid {
					t.Errorf("resolve entry identifies %v with route %v, want %q and none", entry.HumanIdentifier, entry.RouteParam, want)
				}
			}
		})
	}
}

// TestAccountsTakeActionReportPermission checks that resolving reports
// needs manage_reports or the administrator bit, and that a role without
// them may still act when no report would be resolved.
func TestAccountsTakeActionReportPermission(t *testing.T) {
	usersOnly := actor{"Custom", auth.PermissionManageUsers, adminPosition}
	administrator := actor{"Custom", auth.PermissionAdministrator, adminPosition}
	reportsAndUsers := actor{"Custom", auth.PermissionManageUsers | auth.PermissionManageReports, adminPosition}

	tests := []struct {
		name    string
		actor   actor
		target  int64
		body    string
		allowed bool
		// wantStatus and wantMessage describe a refusal other than 403.
		wantStatus  int
		wantMessage string
	}{
		{name: "manage_users silences a target with reports", actor: usersOnly, target: targetNoRole, body: `{"type":"silence"}`},
		{name: "manage_users suspends a target with reports", actor: usersOnly, target: targetModerator, body: `{"type":"suspend"}`},
		{name: "manage_users warns with a report", actor: usersOnly, target: targetNoRole, body: `{"type":"none","report_id":"301"}`},
		{name: "manage_users warns with a resolved report", actor: usersOnly, target: targetNoRole, body: `{"type":"none","report_id":"303"}`},
		{name: "manage_users silences a target without reports", actor: usersOnly, target: targetPending, body: `{"type":"silence"}`, allowed: true},
		{name: "manage_users disables a target without reports", actor: usersOnly, target: targetPending, body: `{"type":"disable"}`, allowed: true},
		{name: "manage_users warns without a report", actor: usersOnly, target: targetNoRole, body: `{"type":"none","text":"Careful."}`, allowed: true},
		{
			// The report is looked up before the reports are authorized.
			name: "manage_users warns with an unknown report", actor: usersOnly, target: targetNoRole, body: `{"type":"none","report_id":"399"}`,
			wantStatus: http.StatusNotFound, wantMessage: "Record not found",
		},
		{name: "manage_reports silences a target with reports", actor: reportsAndUsers, target: targetNoRole, body: `{"type":"silence"}`, allowed: true},
		{name: "administrator silences a target with reports", actor: administrator, target: targetNoRole, body: `{"type":"silence"}`, allowed: true},
		{name: "Moderator silences a target with reports", actor: moderatorActor, target: targetNoRole, body: `{"type":"silence"}`, allowed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(tt.actor).withReports()
			rec := f.act(tt.target, tt.body)
			switch {
			case tt.wantStatus != 0:
				assertRefused(t, f, rec, tt.wantStatus, tt.wantMessage)
			case !tt.allowed:
				assertRefused(t, f, rec, http.StatusForbidden, "This action is not allowed")
			default:
				assertOK(t, rec)
				if len(f.store.strikes) != 1 {
					t.Fatalf("strikes = %v, want one", f.store.strikes)
				}
			}
		})
	}
}

// TestAccountsTakeActionRecordNotFound checks the 404s for a report or
// warning preset that names nothing, and that the policies and the type are
// checked first.
func TestAccountsTakeActionRecordNotFound(t *testing.T) {
	tests := []struct {
		name        string
		actor       actor
		target      int64
		body        string
		wantStatus  int
		wantMessage string
	}{
		{name: "unknown report", body: `{"type":"none","report_id":"399"}`},
		{name: "unknown report with another type", body: `{"type":"suspend","report_id":"399"}`},
		{name: "malformed report", body: `{"type":"none","report_id":"abc"}`},
		{name: "boolean report", body: `{"type":"none","report_id":true}`},
		{name: "out of range report", body: `{"type":"none","report_id":"99999999999999999999"}`},
		{name: "unknown preset", body: `{"type":"none","warning_preset_id":"499"}`},
		{name: "malformed preset", body: `{"type":"silence","warning_preset_id":"spam"}`},
		{name: "known report and unknown preset", body: `{"type":"silence","report_id":"301","warning_preset_id":"499"}`},
		{
			name: "unknown report against a higher role", actor: moderatorActor, target: targetOwner,
			body: `{"type":"none","report_id":"399"}`, wantStatus: http.StatusForbidden, wantMessage: "This action is not allowed",
		},
		{
			name: "unknown report with an unknown type", body: `{"type":"ban","report_id":"399"}`,
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "Invalid action type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.actor
			if a == (actor{}) {
				a = ownerActor
			}
			target := tt.target
			if target == 0 {
				target = targetNoRole
			}
			status, message := tt.wantStatus, tt.wantMessage
			if status == 0 {
				status, message = http.StatusNotFound, "Record not found"
			}
			f := newAccountsFixture(a).withReports()
			assertRefused(t, f, f.act(target, tt.body), status, message)
		})
	}
}

// TestAccountsTakeActionLogs checks the log entries of the type and strike.
func TestAccountsTakeActionLogs(t *testing.T) {
	type entry struct{ action, targetType, identifier, routeParam string }
	tests := []struct {
		name   string
		target int64
		body   string
		want   []entry
	}{
		{name: "warning with text", target: targetPending, body: `{"type":"none","text":"Be nice."}`,
			want: []entry{{"create", "AccountWarning", "user110", "110"}}},
		{name: "warning with a preset", target: targetPending, body: `{"type":"none","warning_preset_id":"401"}`,
			want: []entry{{"create", "AccountWarning", "user110", "110"}}},
		{name: "warning with blank text", target: targetPending, body: `{"type":"none","text":"  "}`},
		{name: "warning without text", target: targetPending, body: `{"type":"none"}`},
		{name: "silence with text", target: targetPending, body: `{"type":"silence","text":"Be nice."}`,
			want: []entry{{"silence", "Account", "user110", ""}}},
		{name: "disable", target: targetPending, body: `{"type":"disable"}`,
			want: []entry{{"disable", "User", "user110", "110"}}},
		{name: "suspend a remote account", target: targetRemote, body: `{"type":"suspend"}`,
			want: []entry{{"suspend", "Account", "user105@remote.example", ""}}},
		{name: "warn a remote account", target: targetRemote, body: `{"type":"none","text":"Hi."}`,
			want: []entry{{"create", "AccountWarning", "user105@remote.example", "105"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(ownerActor).withReports()
			assertOK(t, f.act(tt.target, tt.body))

			var got []entry
			for _, logged := range f.store.logs {
				if logged.AccountID != actingAccount {
					t.Errorf("%s logged by %d, want %d", logged.Action, logged.AccountID, actingAccount)
				}
				got = append(got, entry{logged.Action, logged.TargetType.String, logged.HumanIdentifier.String, logged.RouteParam.String})
				if logged.TargetType.String == "AccountWarning" && logged.TargetID.Int64 != strikeID(1) {
					t.Errorf("create logged for %d, want the strike %d", logged.TargetID.Int64, strikeID(1))
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("logged %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAccountsTakeActionRollsBack checks that a failure anywhere in the
// transaction leaves nothing changed and warns nobody.
func TestAccountsTakeActionRollsBack(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		failOn    string
		beginErr  error
		commitErr error
	}{
		{name: "report lookup", body: `{"type":"none","report_id":"301"}`, failOn: "AdminGetReport"},
		{name: "preset lookup", body: `{"type":"silence","warning_preset_id":"401"}`, failOn: "AdminGetAccountWarningPreset"},
		{name: "unresolved reports lookup", body: `{"type":"silence"}`, failOn: "AdminListUnresolvedReportsByTarget"},
		{name: "type log entry", body: `{"type":"silence"}`, failOn: "CreateAdminActionLog:silence"},
		{name: "silence", body: `{"type":"silence"}`, failOn: "AdminSilenceAccount"},
		{name: "disable", body: `{"type":"disable"}`, failOn: "AdminDisableAccount"},
		{name: "sensitive", body: `{"type":"sensitive"}`, failOn: "AdminSensitizeAccount"},
		{name: "suspend", body: `{"type":"suspend"}`, failOn: "AdminSuspendAccount"},
		{name: "strike", body: `{"type":"silence"}`, failOn: "CreateAccountWarning"},
		{name: "strike log entry", body: `{"type":"none","text":"Be nice.","report_id":"301"}`, failOn: "CreateAdminActionLog:create"},
		{name: "report log entry", body: `{"type":"silence"}`, failOn: "CreateAdminActionLog:resolve"},
		{name: "report resolution", body: `{"type":"silence"}`, failOn: "AdminResolveReport"},
		{name: "begin", body: `{"type":"silence"}`, beginErr: errDatabase},
		{name: "commit", body: `{"type":"silence"}`, commitErr: errDatabase},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(ownerActor).withReports()
			if tt.failOn != "" {
				f.store.failOn = map[string]error{tt.failOn: errDatabase}
			}
			f.tx.beginErr, f.tx.commitErr = tt.beginErr, tt.commitErr

			assertRefused(t, f, f.act(targetNoRole, tt.body), http.StatusInternalServerError, "Database error")
			if f.tx.commits != 0 {
				t.Fatalf("committed %d transactions", f.tx.commits)
			}
			if wantRollbacks := map[bool]int{true: 0, false: 1}[tt.beginErr != nil]; f.tx.rollbacks != wantRollbacks {
				t.Fatalf("rolled back %d transactions, want %d", f.tx.rollbacks, wantRollbacks)
			}
		})
	}
}

// TestAccountsTakeActionRefusalRollsBack checks that a refusal found inside
// the transaction rolls it back.
func TestAccountsTakeActionRefusalRollsBack(t *testing.T) {
	f := newAccountsFixture(actor{"Custom", auth.PermissionManageUsers, adminPosition}).withReports()
	assertRefused(t, f, f.act(targetNoRole, `{"type":"silence"}`), http.StatusForbidden, "This action is not allowed")
	if f.tx.commits != 0 || f.tx.rollbacks != 1 {
		t.Fatalf("commits = %d, rollbacks = %d, want 0 and 1", f.tx.commits, f.tx.rollbacks)
	}
}

// TestAccountsTakeActionWarningDelivery checks when the warning e-mail and
// moderation_warning notification go out: for a local target with a user,
// unless send_email_notification casts to false, and the notification only
// to a target that isn't suspended.
func TestAccountsTakeActionWarningDelivery(t *testing.T) {
	tests := []struct {
		name             string
		target           int64
		body             string
		form             string
		wantMail         bool
		wantNotification bool
	}{
		{name: "default", target: targetNoRole, body: `{"type":"none"}`, wantMail: true, wantNotification: true},
		{name: "true", target: targetNoRole, body: `{"type":"silence","send_email_notification":true}`, wantMail: true, wantNotification: true},
		{name: `"true"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"true"}`, wantMail: true, wantNotification: true},
		{name: `"1"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"1"}`, wantMail: true, wantNotification: true},
		{name: "1", target: targetNoRole, body: `{"type":"silence","send_email_notification":1}`, wantMail: true, wantNotification: true},
		{name: `"yes"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"yes"}`, wantMail: true, wantNotification: true},
		// ActiveModel::Type::Boolean only knows these spellings of false.
		{name: `"False"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"False"}`, wantMail: true, wantNotification: true},
		// params.permit drops a non-scalar value, leaving the default.
		{name: "array", target: targetNoRole, body: `{"type":"silence","send_email_notification":[false]}`, wantMail: true, wantNotification: true},
		{name: "false", target: targetNoRole, body: `{"type":"silence","send_email_notification":false}`},
		{name: `"false"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"false"}`},
		{name: `"FALSE"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"FALSE"}`},
		{name: `"0"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"0"}`},
		{name: "0", target: targetNoRole, body: `{"type":"silence","send_email_notification":0}`},
		{name: `"f"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"f"}`},
		{name: `"off"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"off"}`},
		{name: `"OFF"`, target: targetNoRole, body: `{"type":"silence","send_email_notification":"OFF"}`},
		{name: "empty", target: targetNoRole, body: `{"type":"silence","send_email_notification":""}`},
		{name: "null", target: targetNoRole, body: `{"type":"silence","send_email_notification":null}`},
		{name: "form 0", target: targetNoRole, form: "type=silence&send_email_notification=0"},
		{name: "form 1", target: targetNoRole, form: "type=silence&send_email_notification=1", wantMail: true, wantNotification: true},
		{name: "form default", target: targetNoRole, form: "type=sensitive", wantMail: true, wantNotification: true},
		{name: "pending user", target: targetPending, body: `{"type":"disable"}`, wantMail: true, wantNotification: true},
		{name: "remote account", target: targetRemote, body: `{"type":"silence"}`},
		{name: "remote account with a user", target: targetRemoteWithUser, body: `{"type":"none"}`},
		{name: "local account without a user", target: targetLocalNoUser, body: `{"type":"suspend"}`},
		{name: "instance actor", target: targetInstanceActor, body: `{"type":"none","text":"Hi."}`},
		{name: "suspend", target: targetNoRole, body: `{"type":"suspend"}`, wantMail: true},
		{name: "suspended account", target: targetSuspended, body: `{"type":"none"}`, wantMail: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(ownerActor)
			var rec *httptest.ResponseRecorder
			if tt.form != "" {
				rec = f.actForm(tt.target, tt.form)
			} else {
				rec = f.act(tt.target, tt.body)
			}
			assertOK(t, rec)
			if len(f.store.strikes) != 1 {
				t.Fatalf("strikes = %v, want one", f.store.strikes)
			}

			if got := len(f.mail.mails); got != map[bool]int{true: 1, false: 0}[tt.wantMail] {
				t.Fatalf("sent %v, want mail %v", f.mail.mails, tt.wantMail)
			}
			if tt.wantMail {
				mail := f.mail.mails[0]
				if mail.Kind != mailer.KindWarning || mail.To != fmt.Sprintf("user%d@%s", tt.target, localDomain) || mail.Locale != "de" {
					t.Errorf("mail = %+v, want the warning to user%d@%s in de", mail, tt.target, localDomain)
				}
				if want := fmt.Sprintf("@user%d@%s", tt.target, localDomain); mail.Params["Acct"] != want {
					t.Errorf("Acct = %q, want %q", mail.Params["Acct"], want)
				}
			}

			if !tt.wantNotification {
				if len(f.store.notifications) != 0 || len(f.streamer.streamed) != 0 || len(f.pusher.pushed) != 0 {
					t.Fatalf("notified %v, streamed %v, pushed %v, want nothing", f.store.notifications, f.streamer.streamed, f.pusher.pushed)
				}
				return
			}
			want := db.CreateNotificationParams{
				AccountID:     tt.target,
				FromAccountID: tt.target,
				ActivityType:  "AccountWarning",
				ActivityID:    strikeID(1),
				Type:          pgtype.Text{String: "moderation_warning", Valid: true},
			}
			if !slices.Equal(f.store.notifications, []db.CreateNotificationParams{want}) {
				t.Fatalf("notifications = %v, want %v", f.store.notifications, want)
			}
			if len(f.streamer.streamed) != 1 || f.streamer.streamed[0].ID != 901 {
				t.Errorf("streamed %v, want notification 901", f.streamer.streamed)
			}
			if len(f.pusher.pushed) != 1 || f.pusher.pushed[0].ID != 901 {
				t.Errorf("pushed %v, want notification 901", f.pusher.pushed)
			}
		})
	}
}

// TestAccountsTakeActionWarningMail checks the warning e-mail's parameters,
// with a link for every cited status that still exists.
func TestAccountsTakeActionWarningMail(t *testing.T) {
	f := newAccountsFixtureFor(ownerActor, localDomain).withReports()
	f.store.reports[reportOpen] = db.Report{ID: reportOpen, TargetAccountID: targetNoRole, StatusIds: []int64{1, 2, 3, 4}}
	f.store.cited = []db.AdminListCitedStatusesRow{
		// A local status, which has no URL.
		{ID: 1, Uri: pgtype.Text{String: "https://example.com/users/user104/statuses/1", Valid: true}, Username: "user104"},
		// A status with a URL.
		{ID: 2, Url: pgtype.Text{String: "https://example.com/@user104/2", Valid: true}, Username: "user104"},
		// A remote status without a URL.
		{ID: 3, Uri: pgtype.Text{String: "https://remote.example/statuses/3", Valid: true}, Username: "bob", Domain: pgtype.Text{String: "remote.example", Valid: true}},
		// Status 4 is gone.
	}

	assertOK(t, f.act(targetNoRole, `{"type":"sensitive","report_id":"301","warning_preset_id":"401","text":"Last warning."}`))
	if len(f.mail.mails) != 1 {
		t.Fatalf("sent %v, want one mail", f.mail.mails)
	}
	want := map[string]string{
		"Action": "sensitive",
		"Acct":   "@user104@example.com",
		"Text":   presetText + "\n\nLast warning.",
		"Statuses": "=> https://example.com/@user104/1\n" +
			"=> https://example.com/@user104/2\n" +
			"=> https://remote.example/statuses/3",
	}
	for key, value := range want {
		if got := f.mail.mails[0].Params[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}

	// The mail renders.
	subject, body, err := mailer.Render(f.mail.mails[0], mailer.InstanceInfo{Title: "Example", Domain: localDomain})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if subject != "Your posts on @user104@example.com will be marked as sensitive from now on" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, "Posts cited:\n\n=> https://example.com/@user104/1\n") {
		t.Errorf("body lacks the cited posts:\n%s", body)
	}
}

// TestAccountsTakeActionWarningDeliveryFailures checks that a failure after
// the transaction is logged rather than answered, and doesn't stop the
// other deliveries.
func TestAccountsTakeActionWarningDeliveryFailures(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(f *accountsFixture)
		body   string
		mailed bool
		// notified is whether the notification was created; streamed and
		// pushed follow it.
		notified bool
		pushed   bool
	}{
		{name: "mail delivery", setup: func(f *accountsFixture) { f.mail.err = errDatabase }, notified: true, pushed: true},
		{name: "recipient lookup", setup: func(f *accountsFixture) { f.store.failOn = map[string]error{"GetUserByAccountID": errDatabase} }, notified: true, pushed: true},
		{
			name: "cited status lookup", body: `{"type":"none","report_id":"301"}`,
			setup:    func(f *accountsFixture) { f.store.failOn = map[string]error{"AdminListCitedStatuses": errDatabase} },
			notified: true, pushed: true,
		},
		{name: "notification", setup: func(f *accountsFixture) { f.store.failOn = map[string]error{"CreateNotification": errDatabase} }, mailed: true},
		{name: "streaming", setup: func(f *accountsFixture) { f.streamer.err = errDatabase }, mailed: true, notified: true, pushed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(ownerActor).withReports()
			tt.setup(f)
			body := tt.body
			if body == "" {
				body = `{"type":"none"}`
			}
			assertOK(t, f.act(targetNoRole, body))

			if len(f.store.strikes) != 1 || f.tx.commits != 1 {
				t.Fatalf("strikes = %v, commits = %d, want the action committed", f.store.strikes, f.tx.commits)
			}
			if got := len(f.mail.mails) == 1; got != tt.mailed {
				t.Errorf("mailed = %v, want %v", got, tt.mailed)
			}
			if got := len(f.store.notifications) == 1; got != tt.notified {
				t.Errorf("notified = %v, want %v", got, tt.notified)
			}
			if got := len(f.streamer.streamed) == 1; got != tt.notified {
				t.Errorf("streamed = %v, want %v", got, tt.notified)
			}
			if got := len(f.pusher.pushed) == 1; got != tt.pushed {
				t.Errorf("pushed = %v, want %v", got, tt.pushed)
			}
		})
	}
}

// TestAccountsTakeActionWithoutPublishers checks that without a mailer,
// streaming or Web Push the notification is still created.
func TestAccountsTakeActionWithoutPublishers(t *testing.T) {
	store := &stubAccountsStore{targets: newAccountTargets(ownerPosition)}
	h := NewAccountsHandler(nil, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: localDomain}})
	h.store = store
	h.actions = &stubTransactor{store: store}
	h.roles = &stubRoles{
		usersByAccount: map[int64]db.User{actingAccount: {ID: 20, AccountID: actingAccount}},
		roles:          map[int64]db.UserRole{20: {ID: 3, Name: ownerActor.name, Permissions: ownerActor.permissions, Position: ownerActor.position}},
	}
	f := &accountsFixture{store: store, mux: http.NewServeMux()}
	h.RegisterRoutes(f.mux)

	assertOK(t, f.act(targetNoRole, `{"type":"none"}`))
	if len(store.strikes) != 1 || len(store.notifications) != 1 {
		t.Fatalf("strikes = %v, notifications = %v, want one each", store.strikes, store.notifications)
	}
}

// TestAccountsTakeActionWithoutActingAccount checks that an action fails
// closed when the token names a user but no account, since the strike and
// the resolutions need an author.
func TestAccountsTakeActionWithoutActingAccount(t *testing.T) {
	f := newAccountsFixture(ownerActor)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/"+idOf(targetNoRole)+"/action", strings.NewReader(`{"type":"silence"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withScopes(withIdentity(req, nil, int64Ptr(20)), adminToken)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	assertRefused(t, f, rec, http.StatusInternalServerError, "Admin data unavailable")
}

func TestActionParam(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantValue   string
		wantPresent bool
		wantEnabled bool
	}{
		{name: "absent", body: `{}`, wantEnabled: true},
		{name: "null", body: `{"p":null}`},
		{name: "string", body: `{"p":"301"}`, wantValue: "301", wantPresent: true, wantEnabled: true},
		{name: "blank string", body: `{"p":"  "}`, wantValue: "  ", wantEnabled: true},
		{name: "empty string", body: `{"p":""}`},
		{name: "number", body: `{"p":301}`, wantValue: "301", wantPresent: true, wantEnabled: true},
		{name: "zero", body: `{"p":0}`, wantValue: "0", wantPresent: true},
		{name: "float zero", body: `{"p":0.0}`, wantValue: "0.0", wantPresent: true, wantEnabled: true},
		{name: "true", body: `{"p":true}`, wantValue: "true", wantPresent: true, wantEnabled: true},
		{name: "false", body: `{"p":false}`, wantValue: "false", wantPresent: true},
		{name: "object", body: `{"p":{"a":1}}`, wantEnabled: true},
		{name: "array", body: `{"p":[1]}`, wantEnabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v struct {
				P actionParam `json:"p"`
			}
			if err := jsonUnmarshal(tt.body, &v); err != nil {
				t.Fatalf("decoding %s: %v", tt.body, err)
			}
			if v.P.Value != tt.wantValue || v.P.present() != tt.wantPresent || v.P.enabled() != tt.wantEnabled {
				t.Errorf("%s = %+v (present %v, enabled %v), want %q, %v, %v",
					tt.body, v.P, v.P.present(), v.P.enabled(), tt.wantValue, tt.wantPresent, tt.wantEnabled)
			}
		})
	}
}

// jsonUnmarshal decodes body as web.Bind does.
func jsonUnmarshal(body string, v any) error {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return web.Bind(req, v)
}
