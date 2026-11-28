package federation

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	flagLocalDomain = "local.test"
	flagActorURI    = "https://remote.example/actor"
	flagID          = "https://remote.example/0c9a6f0e-flag"
)

type reportKey struct {
	accountID int64
	targetID  int64
	uri       string
}

// flagStore extends createStore with the report queries, enforcing the
// unique (account, target, uri) index of reports as the database does.
type flagStore struct {
	*createStore
	staff        []db.User
	domainBlocks map[string]bool
	reports      []db.Report
	reportKeys   map[reportKey]bool
	// reportableDomains holds the domain of each ListReportableStatusIDs call.
	reportableDomains []string
}

func newFlagStore() *flagStore {
	return &flagStore{
		createStore:  newCreateStore(),
		domainBlocks: make(map[string]bool),
		reportKeys:   make(map[reportKey]bool),
	}
}

func (s *flagStore) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	account, ok := s.accountsByID[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *flagStore) ListUsersWithPermission(ctx context.Context, permission int64) ([]db.User, error) {
	if permission != auth.PermissionManageReports {
		return nil, nil
	}
	return s.staff, nil
}

// DomainRejectsReports decides by the first of domains, most specific first,
// that a block covers.
func (s *flagStore) DomainRejectsReports(ctx context.Context, domains []string) (bool, error) {
	for _, domain := range domains {
		if rejects, ok := s.domainBlocks[domain]; ok {
			return rejects, nil
		}
	}
	return false, nil
}

// ListReportableStatusIDs keeps the public and unlisted statuses of the
// account; the follower and mention rules are covered by the query tests.
func (s *flagStore) ListReportableStatusIDs(ctx context.Context, arg db.ListReportableStatusIDsParams) ([]int64, error) {
	s.reportableDomains = append(s.reportableDomains, arg.Domain)
	ids := []int64{}
	for _, id := range arg.StatusIds {
		status, ok := s.statusesByID[id]
		if !ok || status.AccountID != arg.AccountID || status.DeletedAt.Valid {
			continue
		}
		if status.Visibility <= httpVisibilityUnlisted {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (s *flagStore) CreateRemoteReport(ctx context.Context, arg db.CreateRemoteReportParams) (db.Report, error) {
	key := reportKey{accountID: arg.AccountID, targetID: arg.TargetAccountID, uri: arg.Uri}
	if s.reportKeys[key] {
		return db.Report{}, pgx.ErrNoRows
	}
	s.reportKeys[key] = true
	report := db.Report{
		ID:              int64(len(s.reports) + 1),
		AccountID:       arg.AccountID,
		TargetAccountID: arg.TargetAccountID,
		StatusIds:       arg.StatusIds,
		Comment:         arg.Comment,
		Forwarded:       pgtype.Bool{Bool: false, Valid: true},
		Uri:             pgtype.Text{String: arg.Uri, Valid: true},
	}
	s.reports = append(s.reports, report)
	return report, nil
}

func (s *flagStore) addAccount(account db.Account) db.Account {
	s.accountsByURI[account.Uri] = account
	s.accountsByID[account.ID] = account
	return account
}

func (s *flagStore) addStatus(status db.Status) db.Status {
	s.statusesByURI[status.Uri.String] = status
	s.statusesByID[status.ID] = status
	return status
}

type recordingMailer struct {
	mails []mailer.Mail
}

func (m *recordingMailer) Deliver(ctx context.Context, mail mailer.Mail) error {
	m.mails = append(m.mails, mail)
	return nil
}

// flagFixture is a server with local accounts alice and bob, carol of
// third.example, and the cached instance actor of remote.example that sends
// the Flags.
type flagFixture struct {
	store   *flagStore
	mail    *recordingMailer
	service *FlagService

	reporter, alice, bob, carol db.Account

	alicePublic, aliceUnlisted, aliceDirect, bobPublic db.Status
	carolReply, carolPost                              db.Status
}

func newFlagFixture(t *testing.T) *flagFixture {
	t.Helper()
	store := newFlagStore()
	store.nextAccountID = 100
	f := &flagFixture{store: store, mail: &recordingMailer{}}
	store.staff = []db.User{{ID: 1, AccountID: 1, Email: "mod@local.test", Locale: pgtype.Text{String: "de", Valid: true}}}

	f.reporter = store.addAccount(db.Account{
		ID: 10, Username: "remote.example", Uri: flagActorURI,
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	})
	f.alice = store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://local.test/users/alice"})
	f.bob = store.addAccount(db.Account{ID: 2, Username: "bob", Uri: "https://local.test/users/bob"})
	f.carol = store.addAccount(db.Account{
		ID: 3, Username: "carol", Uri: "https://third.example/users/carol",
		Domain: pgtype.Text{String: "third.example", Valid: true},
	})

	f.alicePublic = store.addStatus(localFlagStatus(11, f.alice, httpVisibilityPublic))
	f.aliceUnlisted = store.addStatus(localFlagStatus(12, f.alice, httpVisibilityUnlisted))
	f.aliceDirect = store.addStatus(localFlagStatus(13, f.alice, httpVisibilityDirect))
	f.bobPublic = store.addStatus(localFlagStatus(21, f.bob, httpVisibilityPublic))
	f.carolReply = store.addStatus(db.Status{
		ID: 31, AccountID: f.carol.ID, Visibility: httpVisibilityPublic,
		Uri:                pgtype.Text{String: "https://third.example/users/carol/statuses/31", Valid: true},
		InReplyToAccountID: pgtype.Int8{Int64: f.alice.ID, Valid: true},
	})
	f.carolPost = store.addStatus(db.Status{
		ID: 32, AccountID: f.carol.ID, Visibility: httpVisibilityPublic,
		Uri: pgtype.Text{String: "https://third.example/users/carol/statuses/32", Valid: true},
	})

	f.service = NewFlagService(store, flagLocalDomain, stubActorFetcher{actor: makeActor(flagActorURI, "remote.example")})
	f.service.SetMailDeliverer(f.mail)
	return f
}

func localFlagStatus(id int64, author db.Account, visibility int32) db.Status {
	return db.Status{
		ID: id, AccountID: author.ID, Visibility: visibility,
		Uri:   pgtype.Text{String: author.Uri + "/statuses/" + strconv.FormatInt(id, 10), Valid: true},
		Local: pgtype.Bool{Bool: true, Valid: true},
	}
}

func flagActivity(object any, content any) *activitypub.Activity {
	return &activitypub.Activity{
		ID:             flagID,
		Type:           activitypub.TypeFlag,
		Actor:          flagActorURI,
		ActivityObject: object,
		Content:        content,
	}
}

func uris(items ...any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case db.Account:
			out = append(out, value.Uri)
		case db.Status:
			out = append(out, value.Uri.String)
		default:
			out = append(out, value)
		}
	}
	return out
}

func TestFlagService_FilesReportOfLocalAccount(t *testing.T) {
	f := newFlagFixture(t)

	activity := flagActivity(uris(
		f.alice, f.alicePublic, f.aliceDirect, f.aliceUnlisted,
		f.bobPublic, // bob is not reported
		"https://local.test/users/alice/statuses/9", // unknown status
	), "Spam, again")
	if err := f.service.HandleFlag(context.Background(), activity); err != nil {
		t.Fatalf("HandleFlag() error = %v", err)
	}

	if len(f.store.reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(f.store.reports))
	}
	report := f.store.reports[0]
	if report.AccountID != f.reporter.ID || report.TargetAccountID != f.alice.ID {
		t.Fatalf("report account/target = %d/%d, want %d/%d", report.AccountID, report.TargetAccountID, f.reporter.ID, f.alice.ID)
	}
	if want := []int64{f.alicePublic.ID, f.aliceUnlisted.ID}; !slices.Equal(report.StatusIds, want) {
		t.Fatalf("status ids = %v, want %v", report.StatusIds, want)
	}
	if report.Comment != "Spam, again" {
		t.Fatalf("comment = %q", report.Comment)
	}
	if report.Uri.String != flagID {
		t.Fatalf("uri = %q, want %q", report.Uri.String, flagID)
	}
	if want := []string{"remote.example"}; !slices.Equal(f.store.reportableDomains, want) {
		t.Fatalf("reportable status domains = %v, want %v", f.store.reportableDomains, want)
	}

	if len(f.mail.mails) != 1 {
		t.Fatalf("mails = %d, want 1", len(f.mail.mails))
	}
	mail := f.mail.mails[0]
	if mail.Kind != mailer.KindAdminNewReport || mail.To != "mod@local.test" || mail.Locale != "de" {
		t.Fatalf("mail = %+v", mail)
	}
	if mail.Params["ReportID"] != "1" || mail.Params["Reporter"] != "@remote.example@remote.example" || mail.Params["Target"] != "@alice" {
		t.Fatalf("mail params = %v", mail.Params)
	}
}

func TestFlagService_FilesReportPerNamedAccount(t *testing.T) {
	f := newFlagFixture(t)

	activity := flagActivity(uris(f.alice, f.bobPublic, f.bob, f.alicePublic, f.alice), "")
	if err := f.service.HandleFlag(context.Background(), activity); err != nil {
		t.Fatalf("HandleFlag() error = %v", err)
	}

	if len(f.store.reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(f.store.reports))
	}
	want := map[int64][]int64{f.alice.ID: {f.alicePublic.ID}, f.bob.ID: {f.bobPublic.ID}}
	for _, report := range f.store.reports {
		if !slices.Equal(report.StatusIds, want[report.TargetAccountID]) {
			t.Fatalf("report of %d has statuses %v, want %v", report.TargetAccountID, report.StatusIds, want[report.TargetAccountID])
		}
		if report.Uri.String != flagID || report.Comment != "" {
			t.Fatalf("report = %+v", report)
		}
	}
	if len(f.mail.mails) != 2 {
		t.Fatalf("mails = %d, want 2", len(f.mail.mails))
	}
}

func TestFlagService_IgnoresDuplicateFlag(t *testing.T) {
	f := newFlagFixture(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := f.service.HandleFlag(ctx, flagActivity(uris(f.alice, f.alicePublic), "Spam")); err != nil {
			t.Fatalf("delivery %d: HandleFlag() error = %v", i+1, err)
		}
	}
	if len(f.store.reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(f.store.reports))
	}
	if len(f.mail.mails) != 1 {
		t.Fatalf("mails = %d, want 1", len(f.mail.mails))
	}

	// Another Flag of the same account is another report.
	other := flagActivity(uris(f.alice), "More spam")
	other.ID = "https://remote.example/1d2e-flag"
	if err := f.service.HandleFlag(ctx, other); err != nil {
		t.Fatalf("HandleFlag() error = %v", err)
	}
	if len(f.store.reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(f.store.reports))
	}
}

func TestFlagService_RemoteTargets(t *testing.T) {
	tests := []struct {
		name       string
		object     func(f *flagFixture) []any
		wantStatus []int64
		wantReport bool
	}{
		{
			name:       "status replying to a local account",
			object:     func(f *flagFixture) []any { return uris(f.carol, f.carolPost, f.carolReply) },
			wantReport: true,
			wantStatus: []int64{31, 32},
		},
		{
			name:   "statuses replying to no local account",
			object: func(f *flagFixture) []any { return uris(f.carol, f.carolPost) },
		},
		{
			name:   "account without statuses",
			object: func(f *flagFixture) []any { return uris(f.carol) },
		},
		{
			name:   "reply named without its author",
			object: func(f *flagFixture) []any { return uris(f.carolReply) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlagFixture(t)
			if err := f.service.HandleFlag(context.Background(), flagActivity(tt.object(f), "Harassment")); err != nil {
				t.Fatalf("HandleFlag() error = %v", err)
			}
			if !tt.wantReport {
				if len(f.store.reports) != 0 {
					t.Fatalf("reports = %+v, want none", f.store.reports)
				}
				return
			}
			if len(f.store.reports) != 1 {
				t.Fatalf("reports = %d, want 1", len(f.store.reports))
			}
			report := f.store.reports[0]
			if report.TargetAccountID != f.carol.ID || !slices.Equal(report.StatusIds, tt.wantStatus) {
				t.Fatalf("report = %+v, want target %d with statuses %v", report, f.carol.ID, tt.wantStatus)
			}
		})
	}
}

func TestFlagService_FilesNothingForUnreportableTargets(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(f *flagFixture)
		object func(f *flagFixture) any
	}{
		{
			name: "suspended account",
			setup: func(f *flagFixture) {
				f.alice.SuspendedAt = pgtype.Timestamptz{Valid: true}
				f.store.addAccount(f.alice)
			},
			object: func(f *flagFixture) any { return uris(f.alice, f.alicePublic) },
		},
		{
			name: "unknown accounts and statuses",
			object: func(f *flagFixture) any {
				return uris("https://local.test/users/nobody", "https://remote.example/statuses/1")
			},
		},
		{
			name:   "statuses without their author",
			object: func(f *flagFixture) any { return uris(f.alicePublic, f.bobPublic) },
		},
		{
			name:   "single unknown URI",
			object: func(f *flagFixture) any { return "https://local.test/users/nobody" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlagFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			if err := f.service.HandleFlag(context.Background(), flagActivity(tt.object(f), "Spam")); err != nil {
				t.Fatalf("HandleFlag() error = %v", err)
			}
			if len(f.store.reports) != 0 || len(f.mail.mails) != 0 {
				t.Fatalf("reports = %+v, mails = %d, want none", f.store.reports, len(f.mail.mails))
			}
		})
	}
}

func TestFlagService_SingleObjectForms(t *testing.T) {
	tests := []struct {
		name   string
		object any
	}{
		{name: "URI", object: "https://local.test/users/alice"},
		{name: "object", object: map[string]any{"id": "https://local.test/users/alice", "type": "Person"}},
		{name: "objects", object: []any{map[string]any{"id": "https://local.test/users/alice"}, 7, nil, ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlagFixture(t)
			if err := f.service.HandleFlag(context.Background(), flagActivity(tt.object, nil)); err != nil {
				t.Fatalf("HandleFlag() error = %v", err)
			}
			if len(f.store.reports) != 1 || f.store.reports[0].TargetAccountID != f.alice.ID {
				t.Fatalf("reports = %+v, want one of alice", f.store.reports)
			}
			if got := f.store.reports[0].StatusIds; len(got) != 0 {
				t.Fatalf("status ids = %v, want none", got)
			}
		})
	}
}

func TestFlagService_RejectsInvalidFlags(t *testing.T) {
	tests := []struct {
		name       string
		activity   func(f *flagFixture) *activitypub.Activity
		fetcher    activitypub.ActorFetcher
		setup      func(f *flagFixture)
		wantStatus int
	}{
		{
			name:       "no activity",
			activity:   func(f *flagFixture) *activitypub.Activity { return nil },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "no actor",
			activity: func(f *flagFixture) *activitypub.Activity {
				a := flagActivity(uris(f.alice), "")
				a.Actor = nil
				return a
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "no id",
			activity: func(f *flagFixture) *activitypub.Activity {
				a := flagActivity(uris(f.alice), "")
				a.ID = ""
				return a
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "id hosted by another server",
			activity: func(f *flagFixture) *activitypub.Activity {
				a := flagActivity(uris(f.alice), "")
				a.ID = "https://third.example/flags/1"
				return a
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "id hosted by the local server",
			activity: func(f *flagFixture) *activitypub.Activity {
				a := flagActivity(uris(f.alice), "")
				a.ID = "https://local.test/reports/1"
				return a
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "id that is not a URL",
			activity: func(f *flagFixture) *activitypub.Activity {
				a := flagActivity(uris(f.alice), "")
				a.ID = "urn:uuid:0c9a6f0e"
				return a
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "no object",
			activity:   func(f *flagFixture) *activitypub.Activity { return flagActivity(nil, "") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty object",
			activity:   func(f *flagFixture) *activitypub.Activity { return flagActivity([]any{}, "") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "object without URIs",
			activity: func(f *flagFixture) *activitypub.Activity {
				return flagActivity([]any{42, map[string]any{"type": "Note"}, " "}, "")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "object that is a number",
			activity:   func(f *flagFixture) *activitypub.Activity { return flagActivity(42, "") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "actor that cannot be fetched",
			activity:   func(f *flagFixture) *activitypub.Activity { return flagActivity(uris(f.alice), "") },
			fetcher:    stubActorFetcher{err: errors.New("gone")},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:     "suspended actor",
			activity: func(f *flagFixture) *activitypub.Activity { return flagActivity(uris(f.alice), "") },
			setup: func(f *flagFixture) {
				f.reporter.SuspendedAt = pgtype.Timestamptz{Valid: true}
				f.store.addAccount(f.reporter)
			},
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlagFixture(t)
			if tt.fetcher != nil {
				f.service = NewFlagService(f.store, flagLocalDomain, tt.fetcher)
			}
			if tt.setup != nil {
				tt.setup(f)
			}
			err := f.service.HandleFlag(context.Background(), tt.activity(f))
			var inboxErr *apihandler.InboxError
			if !errors.As(err, &inboxErr) || inboxErr.Status != tt.wantStatus {
				t.Fatalf("HandleFlag() error = %v, want inbox error %d", err, tt.wantStatus)
			}
			if len(f.store.reports) != 0 {
				t.Fatalf("reports = %+v, want none", f.store.reports)
			}
		})
	}
}

func TestFlagService_IgnoresLocalActor(t *testing.T) {
	f := newFlagFixture(t)
	activity := flagActivity(uris(f.bob), "")
	activity.Actor = f.alice.Uri
	activity.ID = "https://local.test/reports/1"
	if err := f.service.HandleFlag(context.Background(), activity); err != nil {
		t.Fatalf("HandleFlag() error = %v", err)
	}
	if len(f.store.reports) != 0 {
		t.Fatalf("reports = %+v, want none", f.store.reports)
	}
}

func TestFlagService_CreatesReporterAccount(t *testing.T) {
	f := newFlagFixture(t)
	delete(f.store.accountsByURI, flagActorURI)
	delete(f.store.accountsByID, f.reporter.ID)

	if err := f.service.HandleFlag(context.Background(), flagActivity(uris(f.alice), "")); err != nil {
		t.Fatalf("HandleFlag() error = %v", err)
	}
	reporter, ok := f.store.accountsByURI[flagActorURI]
	if !ok || reporter.Domain.String != "remote.example" {
		t.Fatalf("reporter account = %+v, %v", reporter, ok)
	}
	if len(f.store.reports) != 1 || f.store.reports[0].AccountID != reporter.ID {
		t.Fatalf("reports = %+v, want one by %d", f.store.reports, reporter.ID)
	}
}

func TestFlagService_DomainBlocks(t *testing.T) {
	tests := []struct {
		name       string
		blocks     map[string]bool
		wantReport bool
	}{
		{name: "no block", wantReport: true},
		{name: "block of another domain", blocks: map[string]bool{"third.example": true}, wantReport: true},
		{name: "block keeping reports", blocks: map[string]bool{"remote.example": false}, wantReport: true},
		{name: "block rejecting reports", blocks: map[string]bool{"remote.example": true}},
		{name: "parent domain rejecting reports", blocks: map[string]bool{"example": true}},
		{name: "more specific block keeping reports", blocks: map[string]bool{"example": true, "remote.example": false}, wantReport: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlagFixture(t)
			for domain, rejects := range tt.blocks {
				f.store.domainBlocks[domain] = rejects
			}
			if err := f.service.HandleFlag(context.Background(), flagActivity(uris(f.alice), "")); err != nil {
				t.Fatalf("HandleFlag() error = %v", err)
			}
			if got := len(f.store.reports) == 1; got != tt.wantReport {
				t.Fatalf("report filed = %v, want %v", got, tt.wantReport)
			}
		})
	}
}

func TestFlagComment(t *testing.T) {
	long := strings.Repeat("ä", maxFlagCommentLength+10)
	tests := []struct {
		name    string
		content any
		want    string
	}{
		{name: "text", content: "Spam", want: "Spam"},
		{name: "no content", content: nil, want: ""},
		{name: "not text", content: map[string]any{"en": "Spam"}, want: ""},
		{name: "too long", content: long, want: strings.Repeat("ä", maxFlagCommentLength)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flagComment(tt.content); got != tt.want {
				t.Fatalf("flagComment() = %q (%d runes), want %d runes", got[:min(len(got), 20)], len([]rune(got)), len([]rune(tt.want)))
			}
		})
	}
}

func TestDomainBlockVariants(t *testing.T) {
	tests := []struct {
		domain string
		want   []string
	}{
		{domain: "Social.Remote.Example", want: []string{"social.remote.example", "remote.example", "example"}},
		{domain: "remote.example:8443", want: []string{"remote.example", "example"}},
		{domain: "localhost", want: []string{"localhost"}},
		{domain: "", want: []string{}},
	}
	for _, tt := range tests {
		if got := domainBlockVariants(tt.domain); !slices.Equal(got, tt.want) {
			t.Errorf("domainBlockVariants(%q) = %v, want %v", tt.domain, got, tt.want)
		}
	}
}
