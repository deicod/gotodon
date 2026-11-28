package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// stubAccountsStore is an accountsStore backed by a map of action targets.
// It records every state change, so tests can check that a refusal changed
// nothing.
type stubAccountsStore struct {
	targets map[int64]db.AdminGetAccountActionTargetRow
	// targetErr, when set, is returned by the target lookup.
	targetErr error
	// writeErr, when set, is returned by every state change.
	writeErr error

	// list and listErr answer AdminListAccounts, which records its params
	// in listParams.
	list       []db.AdminListAccountsRow
	listErr    error
	listParams []db.AdminListAccountsParams
	// rows are AdminGetAccount's rows. An account in targets without a row
	// gets one made from its target.
	rows map[int64]db.AdminGetAccountRow
	// getErr, when set, is returned by AdminGetAccount.
	getErr error
	// accountsErr, when set, is returned by GetAccountsByIDs, which leaves
	// out the accounts in missingAccounts.
	accountsErr     error
	missingAccounts []int64
	// ips and ipsErr answer AdminListUserIPs.
	ips    []db.AdminListUserIPsRow
	ipsErr error

	// reports and presets answer an account action's lookups, and cited
	// AdminListCitedStatuses.
	reports map[int64]db.Report
	presets map[int64]db.AccountWarningPreset
	cited   []db.AdminListCitedStatusesRow
	// failOn makes a call fail with its error: a method name, or
	// CreateAdminActionLog:<action> for the log entry of one action.
	failOn map[string]error

	changes       []string
	logs          []db.CreateAdminActionLogParams
	strikes       []db.CreateAccountWarningParams
	resolutions   []db.AdminResolveReportParams
	notifications []db.CreateNotificationParams
}

func (s *stubAccountsStore) change(name string, id int64) error {
	s.changes = append(s.changes, fmt.Sprintf("%s(%d)", name, id))
	if err := s.failOn[name]; err != nil {
		return err
	}
	return s.writeErr
}

func (s *stubAccountsStore) AdminListAccounts(_ context.Context, arg db.AdminListAccountsParams) ([]db.AdminListAccountsRow, error) {
	s.listParams = append(s.listParams, arg)
	return s.list, s.listErr
}

// AdminGetAccount answers like the real query, which left-joins users: it
// finds every account, with the user columns NULL for one without a user.
func (s *stubAccountsStore) AdminGetAccount(_ context.Context, id int64) (db.AdminGetAccountRow, error) {
	if s.getErr != nil {
		return db.AdminGetAccountRow{}, s.getErr
	}
	if row, ok := s.rows[id]; ok {
		return row, nil
	}
	target, ok := s.targets[id]
	if !ok {
		return db.AdminGetAccountRow{}, pgx.ErrNoRows
	}
	row := db.AdminGetAccountRow{
		ID:          id,
		Username:    fmt.Sprintf("user%d", id),
		Domain:      target.Domain,
		SuspendedAt: target.SuspendedAt,
		UserID:      target.UserID,
	}
	if target.UserID.Valid {
		row.Email = pgtype.Text{String: fmt.Sprintf("user%d@%s", id, localDomain), Valid: true}
		row.Approved = target.UserApproved
		row.Disabled = pgtype.Bool{Bool: false, Valid: true}
	}
	return row, nil
}

// accountOf returns account id as its row in rows or list has it, or as
// user<id> with the domain of its target.
func (s *stubAccountsStore) accountOf(id int64) db.Account {
	if row, ok := s.rows[id]; ok {
		return db.Account{ID: id, Username: row.Username, Domain: row.Domain}
	}
	for _, row := range s.list {
		if row.ID == id {
			return db.Account{ID: id, Username: row.Username, Domain: row.Domain}
		}
	}
	return db.Account{ID: id, Username: fmt.Sprintf("user%d", id), Domain: s.targets[id].Domain}
}

func (s *stubAccountsStore) GetAccountsByIDs(_ context.Context, ids []int64) ([]db.Account, error) {
	if s.accountsErr != nil {
		return nil, s.accountsErr
	}
	accounts := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(s.missingAccounts, id) {
			accounts = append(accounts, s.accountOf(id))
		}
	}
	return accounts, nil
}

func (s *stubAccountsStore) AdminListUserIPs(_ context.Context, userIDs []int64) ([]db.AdminListUserIPsRow, error) {
	if s.ipsErr != nil {
		return nil, s.ipsErr
	}
	var rows []db.AdminListUserIPsRow
	for _, row := range s.ips {
		if slices.Contains(userIDs, row.UserID) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *stubAccountsStore) AdminGetAccountActionTarget(_ context.Context, id int64) (db.AdminGetAccountActionTargetRow, error) {
	if s.targetErr != nil {
		return db.AdminGetAccountActionTargetRow{}, s.targetErr
	}
	target, ok := s.targets[id]
	if !ok {
		return db.AdminGetAccountActionTargetRow{}, pgx.ErrNoRows
	}
	return target, nil
}

func (s *stubAccountsStore) AdminApproveAccount(_ context.Context, id int64) error {
	return s.change("AdminApproveAccount", id)
}

// AdminRejectAccount deletes the user, as the real query does.
func (s *stubAccountsStore) AdminRejectAccount(_ context.Context, id int64) error {
	if err := s.change("AdminRejectAccount", id); err != nil {
		return err
	}
	target := s.targets[id]
	target.UserID, target.UserApproved, target.RolePosition = pgtype.Int8{}, pgtype.Bool{}, pgtype.Int4{}
	s.targets[id] = target
	return nil
}

// stubDeletionScheduler records the deletions it is asked to start as
// changes of the store, with their options.
type stubDeletionScheduler struct {
	store   *stubAccountsStore
	options []services.AccountDeletionOptions
}

func (d *stubDeletionScheduler) ScheduleAccountDeletion(_ context.Context, accountID int64, opts services.AccountDeletionOptions) error {
	d.options = append(d.options, opts)
	return d.store.change("ScheduleAccountDeletion", accountID)
}

func (s *stubAccountsStore) AdminDisableAccount(_ context.Context, id int64) error {
	return s.change("AdminDisableAccount", id)
}

func (s *stubAccountsStore) AdminEnableAccount(_ context.Context, id int64) error {
	return s.change("AdminEnableAccount", id)
}

func (s *stubAccountsStore) AdminSilenceAccount(_ context.Context, id int64) error {
	return s.change("AdminSilenceAccount", id)
}

func (s *stubAccountsStore) AdminUnsilenceAccount(_ context.Context, id int64) error {
	return s.change("AdminUnsilenceAccount", id)
}

func (s *stubAccountsStore) AdminSuspendAccount(_ context.Context, id int64) error {
	return s.change("AdminSuspendAccount", id)
}

func (s *stubAccountsStore) AdminUnsuspendAccount(_ context.Context, id int64) error {
	return s.change("AdminUnsuspendAccount", id)
}

func (s *stubAccountsStore) AdminSensitizeAccount(_ context.Context, id int64) error {
	return s.change("AdminSensitizeAccount", id)
}

func (s *stubAccountsStore) AdminUnsensitizeAccount(_ context.Context, id int64) error {
	return s.change("AdminUnsensitizeAccount", id)
}

func (s *stubAccountsStore) GetAccountByIDAllowSuspended(_ context.Context, id int64) (db.Account, error) {
	target, ok := s.targets[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return db.Account{ID: id, Username: fmt.Sprintf("user%d", id), Domain: target.Domain}, nil
}

// GetUserByAccountID finds the user of an account with one, whose e-mail is
// user<account>@localDomain and whose locale is de.
func (s *stubAccountsStore) GetUserByAccountID(_ context.Context, accountID int64) (db.User, error) {
	if err := s.failOn["GetUserByAccountID"]; err != nil {
		return db.User{}, err
	}
	target, ok := s.targets[accountID]
	if !ok || !target.UserID.Valid {
		return db.User{}, pgx.ErrNoRows
	}
	return db.User{
		ID:        target.UserID.Int64,
		AccountID: accountID,
		Email:     fmt.Sprintf("user%d@%s", accountID, localDomain),
		Locale:    pgtype.Text{String: "de", Valid: true},
	}, nil
}

func (s *stubAccountsStore) CreateAdminActionLog(_ context.Context, arg db.CreateAdminActionLogParams) error {
	if err := s.failOn["CreateAdminActionLog:"+arg.Action]; err != nil {
		return err
	}
	s.logs = append(s.logs, arg)
	return nil
}

// loggedActions returns the logged actions as action:target type:target ID.
func (s *stubAccountsStore) loggedActions() []string {
	actions := make([]string, 0, len(s.logs))
	for _, entry := range s.logs {
		actions = append(actions, fmt.Sprintf("%s:%s:%d", entry.Action, entry.TargetType.String, entry.TargetID.Int64))
	}
	return actions
}

const localDomain = "example.com"

// The acting user is account 10 with user 20, as in rolesFor.
const actingAccount int64 = 10

// Target accounts. Users are numbered as their account plus 100.
const (
	targetOwner     int64 = 101
	targetAdmin     int64 = 102
	targetModerator int64 = 103
	// targetNoRole is an approved local user without a role, so at the
	// everyone role's position.
	targetNoRole int64 = 104
	// targetRemote is a remote account, which has no user.
	targetRemote int64 = 105
	// targetInstanceActor is the local instance actor, which has no user.
	targetInstanceActor int64 = 106
	// targetLocalNoUser is a local account without a user that isn't the
	// instance actor.
	targetLocalNoUser int64 = 107
	// targetRemoteActorURI is a remote account whose URI is the local
	// instance actor's.
	targetRemoteActorURI int64 = 108
	targetPending        int64 = 110
	// targetSuspended is suspended temporarily: locally, with a deletion
	// request.
	targetSuspended int64 = 111
	// targetSuspendedNoRequest is suspended locally without a deletion
	// request.
	targetSuspendedNoRequest int64 = 112
	// targetSuspendedRemotely is a remote account that its own server
	// suspended, with a deletion request.
	targetSuspendedRemotely int64 = 113
	// targetPendingOwner is a pending user with the Owner role.
	targetPendingOwner int64 = 114
	// targetSuspendedOwner is an Owner suspended temporarily.
	targetSuspendedOwner int64 = 115
	// targetDeletionRequested isn't suspended but has a deletion request.
	targetDeletionRequested int64 = 116
	// targetRemoteWithUser is a remote account with a user row, which
	// require_local_account! refuses as remote.
	targetRemoteWithUser int64 = 117
	// targetRemoteSuspendedLocally is a remote account that this server
	// suspended.
	targetRemoteSuspendedLocally int64 = 118
	// targetUnknown names no account.
	targetUnknown int64 = 999
)

func localTarget(id int64, rolePosition *int32, approved bool) db.AdminGetAccountActionTargetRow {
	target := db.AdminGetAccountActionTargetRow{
		ID:           id,
		Uri:          fmt.Sprintf("https://%s/users/user%d", localDomain, id),
		UserID:       pgtype.Int8{Int64: id + 100, Valid: true},
		UserApproved: pgtype.Bool{Bool: approved, Valid: true},
	}
	if rolePosition != nil {
		target.RolePosition = pgtype.Int4{Int32: *rolePosition, Valid: true}
	}
	return target
}

func remoteTarget(id int64, uri string) db.AdminGetAccountActionTargetRow {
	return db.AdminGetAccountActionTargetRow{
		ID:     id,
		Domain: pgtype.Text{String: "remote.example", Valid: true},
		Uri:    uri,
	}
}

func suspended(target db.AdminGetAccountActionTargetRow, origin int32, deletionRequested bool) db.AdminGetAccountActionTargetRow {
	target.SuspendedAt = pgtype.Timestamptz{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	target.SuspensionOrigin = pgtype.Int4{Int32: origin, Valid: true}
	target.DeletionRequested = deletionRequested
	return target
}

// newAccountTargets returns the target accounts, with the acting account
// at actingPosition.
func newAccountTargets(actingPosition int32) map[int64]db.AdminGetAccountActionTargetRow {
	at := func(position int32) *int32 { return &position }
	self := localTarget(actingAccount, at(actingPosition), true)
	self.UserID = pgtype.Int8{Int64: 20, Valid: true}

	return map[int64]db.AdminGetAccountActionTargetRow{
		actingAccount:     self,
		targetOwner:       localTarget(targetOwner, at(ownerPosition), true),
		targetAdmin:       localTarget(targetAdmin, at(adminPosition), true),
		targetModerator:   localTarget(targetModerator, at(moderatorPosition), true),
		targetNoRole:      localTarget(targetNoRole, nil, true),
		targetRemote:      remoteTarget(targetRemote, "https://remote.example/users/bob"),
		targetPending:     localTarget(targetPending, nil, false),
		targetLocalNoUser: {ID: targetLocalNoUser, Uri: fmt.Sprintf("https://%s/users/ghost", localDomain)},
		targetInstanceActor: {
			ID:  targetInstanceActor,
			Uri: activitypub.InstanceActorURI(localDomain),
		},
		targetRemoteActorURI:     remoteTarget(targetRemoteActorURI, activitypub.InstanceActorURI(localDomain)),
		targetSuspended:          suspended(localTarget(targetSuspended, nil, true), suspensionOriginLocal, true),
		targetSuspendedNoRequest: suspended(localTarget(targetSuspendedNoRequest, nil, true), suspensionOriginLocal, false),
		targetSuspendedRemotely:  suspended(remoteTarget(targetSuspendedRemotely, "https://remote.example/users/frank"), 1, true),
		targetPendingOwner:       localTarget(targetPendingOwner, at(ownerPosition), false),
		targetSuspendedOwner:     suspended(localTarget(targetSuspendedOwner, at(ownerPosition), true), suspensionOriginLocal, true),
		targetDeletionRequested:  deletionRequested(localTarget(targetDeletionRequested, nil, true)),
		targetRemoteWithUser:     remoteWithUser(localTarget(targetRemoteWithUser, nil, false)),
		targetRemoteSuspendedLocally: suspended(
			remoteTarget(targetRemoteSuspendedLocally, "https://remote.example/users/rose"), suspensionOriginLocal, true),
	}
}

func deletionRequested(target db.AdminGetAccountActionTargetRow) db.AdminGetAccountActionTargetRow {
	target.DeletionRequested = true
	return target
}

func remoteWithUser(target db.AdminGetAccountActionTargetRow) db.AdminGetAccountActionTargetRow {
	target.Domain = pgtype.Text{String: "remote.example", Valid: true}
	target.Uri = fmt.Sprintf("https://remote.example/users/user%d", target.ID)
	return target
}

// actor is an acting role with the seeded permissions and position.
type actor struct {
	name        string
	permissions int64
	position    int32
}

var (
	ownerActor     = actor{"Owner", ownerPermissions, ownerPosition}
	adminActor     = actor{"Admin", adminPermissions, adminPosition}
	moderatorActor = actor{"Moderator", moderatorPermissions, moderatorPosition}
)

// accountsFixture serves the admin account routes for an acting role, with
// WithAccess in front as in production. Account actions run in tx, and their
// warnings go to mail, streamer and pusher.
type accountsFixture struct {
	store     *stubAccountsStore
	mux       *http.ServeMux
	tx        *stubTransactor
	deletions *stubDeletionScheduler
	mail      *stubMail
	streamer  *stubStreamer
	pusher    *stubPusher
}

func newAccountsFixture(a actor) *accountsFixture {
	return newAccountsFixtureFor(a, localDomain)
}

// newAccountsFixtureFor serves the routes for a server at domain.
func newAccountsFixtureFor(a actor, domain string) *accountsFixture {
	store := &stubAccountsStore{targets: newAccountTargets(a.position)}
	h := NewAccountsHandler(nil, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: domain}})
	h.store = store
	h.roles = &stubRoles{
		usersByAccount: map[int64]db.User{actingAccount: {ID: 20, AccountID: actingAccount}},
		roles:          map[int64]db.UserRole{20: {ID: 3, Name: a.name, Permissions: a.permissions, Position: a.position}},
	}
	f := &accountsFixture{
		store:     store,
		mux:       http.NewServeMux(),
		tx:        &stubTransactor{store: store},
		deletions: &stubDeletionScheduler{store: store},
		mail:      &stubMail{},
		streamer:  &stubStreamer{},
		pusher:    &stubPusher{},
	}
	h.actions = f.tx
	h.SetAccountDeletionScheduler(f.deletions)
	h.SetMailDeliverer(f.mail)
	h.SetNotificationPublishers(f.streamer, f.pusher)
	h.RegisterRoutes(f.mux)
	return f
}

// serve sends an authenticated request with both admin scopes. A body is
// sent as JSON.
func (f *accountsFixture) serve(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req = withScopes(withIdentity(req, int64Ptr(actingAccount), int64Ptr(20)), adminToken)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *accountsFixture) takeAction(id, actionType string) *httptest.ResponseRecorder {
	return f.serve(http.MethodPost, "/api/v1/admin/accounts/"+id+"/action", fmt.Sprintf(`{"type":%q}`, actionType))
}

func idOf(account int64) string { return fmt.Sprint(account) }

// actionChange is the state change and log entry of each action type,
// "" for none.
var actionChange = map[string]struct{ change, log string }{
	"disable":   {"AdminDisableAccount", "disable:User"},
	"sensitive": {"AdminSensitizeAccount", "sensitive:Account"},
	"silence":   {"AdminSilenceAccount", "silence:Account"},
	"suspend":   {"AdminSuspendAccount", "suspend:Account"},
	"none":      {},
}

// assertAllowed checks a 200 response and that exactly the given change and
// log entries happened.
func assertAllowed(t *testing.T, f *accountsFixture, rec *httptest.ResponseRecorder, changes, logs []string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
	}
	if !slices.Equal(f.store.changes, changes) {
		t.Fatalf("changes = %v, want %v", f.store.changes, changes)
	}
	if got := f.store.loggedActions(); !slices.Equal(got, logs) {
		t.Fatalf("logged = %v, want %v", got, logs)
	}
}

// assertRefused checks the status and error, and that nothing changed,
// nothing was logged and nobody was warned.
func assertRefused(t *testing.T, f *accountsFixture, rec *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), status)
	}
	if got := errorMessage(t, rec); got != message {
		t.Fatalf("error = %q, want %q", got, message)
	}
	assertNothingDone(t, f)
}

// assertNothingDone checks that nothing changed, nothing was logged and
// nobody was warned.
func assertNothingDone(t *testing.T, f *accountsFixture) {
	t.Helper()
	if len(f.store.changes) != 0 {
		t.Fatalf("refused request changed %v", f.store.changes)
	}
	if len(f.store.logs) != 0 {
		t.Fatalf("refused request logged %v", f.store.loggedActions())
	}
	if len(f.store.strikes) != 0 || len(f.store.resolutions) != 0 {
		t.Fatalf("refused request created strikes %v and resolved reports %v", f.store.strikes, f.store.resolutions)
	}
	if len(f.store.notifications) != 0 {
		t.Fatalf("refused request notified %v", f.store.notifications)
	}
	if f.mail != nil && len(f.mail.mails) != 0 {
		t.Fatalf("refused request sent %v", f.mail.mails)
	}
}

// TestAccountsTakeActionRoleHierarchy checks every action type of every
// seeded role against every kind of target: a role overrides only lower
// positions, an account without a user is always overridden, and disable
// needs a user.
func TestAccountsTakeActionRoleHierarchy(t *testing.T) {
	targets := []struct {
		name    string
		account int64
	}{
		{"Owner", targetOwner},
		{"Admin", targetAdmin},
		{"Moderator", targetModerator},
		{"user without a role", targetNoRole},
		{"remote account", targetRemote},
		{"themselves", actingAccount},
	}
	overrides := map[string][]int64{
		ownerActor.name:     {targetAdmin, targetModerator, targetNoRole, targetRemote},
		adminActor.name:     {targetModerator, targetNoRole, targetRemote},
		moderatorActor.name: {targetNoRole, targetRemote},
	}

	for _, a := range []actor{ownerActor, adminActor, moderatorActor} {
		for _, target := range targets {
			for _, actionType := range []string{"disable", "sensitive", "silence", "suspend", "none"} {
				t.Run(fmt.Sprintf("%s %s %s", a.name, actionType, target.name), func(t *testing.T) {
					f := newAccountsFixture(a)
					rec := f.takeAction(idOf(target.account), actionType)

					allowed := slices.Contains(overrides[a.name], target.account)
					if actionType == "disable" && target.account == targetRemote {
						allowed = false
					}
					if !allowed {
						assertRefused(t, f, rec, http.StatusForbidden, "This action is not allowed")
						return
					}

					want := actionChange[actionType]
					var changes, logs []string
					if want.change != "" {
						changes = []string{fmt.Sprintf("%s(%d)", want.change, target.account)}
						logged := target.account
						if strings.HasSuffix(want.log, ":User") {
							logged = f.store.targets[target.account].UserID.Int64
						}
						logs = []string{fmt.Sprintf("%s:%d", want.log, logged)}
					}
					assertAllowed(t, f, rec, changes, logs)
				})
			}
		}
	}
}

func TestAccountsTakeActionTargets(t *testing.T) {
	// Roles at and just above the everyone role's position, which a user
	// without a role holds.
	everyoneLevel := actor{"Custom", moderatorPermissions, everyonePosition}
	aboveEveryone := actor{"Custom", moderatorPermissions, everyonePosition + 1}

	tests := []struct {
		name string
		// actor is the acting role; the zero value is the Owner.
		actor      actor
		target     int64
		actionType string
		// domain is the handler's local domain; "" uses localDomain.
		domain  string
		allowed bool
	}{
		{"role at the everyone position against a user without a role", everyoneLevel, targetNoRole, "silence", "", false},
		{"role at the everyone position against a remote account", everyoneLevel, targetRemote, "silence", "", true},
		{"role above the everyone position against a user without a role", aboveEveryone, targetNoRole, "silence", "", true},
		{"role above the everyone position against a Moderator", aboveEveryone, targetModerator, "silence", "", false},
		{"suspend the instance actor", actor{}, targetInstanceActor, "suspend", "", false},
		{"disable the instance actor", actor{}, targetInstanceActor, "disable", "", false},
		{"sensitize the instance actor", actor{}, targetInstanceActor, "sensitive", "", true},
		{"silence the instance actor", actor{}, targetInstanceActor, "silence", "", true},
		{"none on the instance actor", actor{}, targetInstanceActor, "none", "", true},
		{"suspend a local account without a user", actor{}, targetLocalNoUser, "suspend", "", true},
		{"disable a local account without a user", actor{}, targetLocalNoUser, "disable", "", false},
		{"suspend a remote account with the instance actor's URI", actor{}, targetRemoteActorURI, "suspend", "", true},
		{"suspend another server's instance actor", actor{}, targetInstanceActor, "suspend", "other.example", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domain := tt.domain
			if domain == "" {
				domain = localDomain
			}
			a := tt.actor
			if a == (actor{}) {
				a = ownerActor
			}
			f := newAccountsFixtureFor(a, domain)
			rec := f.takeAction(idOf(tt.target), tt.actionType)

			if !tt.allowed {
				assertRefused(t, f, rec, http.StatusForbidden, "This action is not allowed")
				return
			}
			want := actionChange[tt.actionType]
			var changes, logs []string
			if want.change != "" {
				changes = []string{fmt.Sprintf("%s(%d)", want.change, tt.target)}
				logs = []string{fmt.Sprintf("%s:%d", want.log, tt.target)}
			}
			assertAllowed(t, f, rec, changes, logs)
		})
	}
}

// TestAccountsInstanceActorWithoutLocalDomain checks that without a local
// domain no account counts as the instance actor, not even a local one
// whose URI is as blank as the missing instance actor URI.
func TestAccountsInstanceActorWithoutLocalDomain(t *testing.T) {
	f := newAccountsFixtureFor(ownerActor, "")
	f.store.targets[targetLocalNoUser] = db.AdminGetAccountActionTargetRow{ID: targetLocalNoUser}

	rec := f.takeAction(idOf(targetLocalNoUser), "suspend")
	assertAllowed(t, f, rec,
		[]string{fmt.Sprintf("AdminSuspendAccount(%d)", targetLocalNoUser)},
		[]string{fmt.Sprintf("suspend:Account:%d", targetLocalNoUser)})
}

func TestAccountsTakeActionRequests(t *testing.T) {
	tests := []struct {
		name        string
		actor       actor
		path        string
		body        string
		targetErr   error
		writeErr    error
		wantStatus  int
		wantMessage string
	}{
		{
			name: "malformed ID", actor: ownerActor, path: "abc", body: `{"type":"suspend"}`,
			wantStatus: http.StatusNotFound, wantMessage: "Record not found",
		},
		{
			name: "unknown account", actor: ownerActor, path: idOf(targetUnknown), body: `{"type":"suspend"}`,
			wantStatus: http.StatusNotFound, wantMessage: "Record not found",
		},
		{
			name: "unknown account with an unknown type", actor: ownerActor, path: idOf(targetUnknown), body: `{"type":"ban"}`,
			wantStatus: http.StatusNotFound, wantMessage: "Record not found",
		},
		{
			name: "lookup fails", actor: ownerActor, path: idOf(targetNoRole), body: `{"type":"suspend"}`, targetErr: errDatabase,
			wantStatus: http.StatusInternalServerError, wantMessage: "Database error",
		},
		{
			name: "malformed body", actor: ownerActor, path: idOf(targetNoRole), body: `{"type":`,
			wantStatus: http.StatusBadRequest, wantMessage: "Invalid request body",
		},
		{
			name: "unknown type", actor: ownerActor, path: idOf(targetNoRole), body: `{"type":"ban"}`,
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "Invalid action type",
		},
		{
			name: "missing type", actor: ownerActor, path: idOf(targetNoRole), body: `{}`,
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "Invalid action type",
		},
		{
			// Admin::AccountAction validates the type before it authorizes.
			name: "unknown type against a higher role", actor: moderatorActor, path: idOf(targetOwner), body: `{"type":"ban"}`,
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "Invalid action type",
		},
		{
			// The transaction rolls the failed change back.
			name: "change fails", actor: ownerActor, path: idOf(targetNoRole), body: `{"type":"silence"}`, writeErr: errDatabase,
			wantStatus: http.StatusInternalServerError, wantMessage: "Database error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(tt.actor)
			f.store.targetErr = tt.targetErr
			f.store.writeErr = tt.writeErr
			rec := f.serve(http.MethodPost, "/api/v1/admin/accounts/"+tt.path+"/action", tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), tt.wantStatus)
			}
			if got := errorMessage(t, rec); got != tt.wantMessage {
				t.Fatalf("error = %q, want %q", got, tt.wantMessage)
			}
			assertNothingDone(t, f)
		})
	}
}

// TestAccountsTakeActionWithoutActingRole checks that the handler fails
// closed when it runs without WithAccess in front.
func TestAccountsTakeActionWithoutActingRole(t *testing.T) {
	store := &stubAccountsStore{targets: newAccountTargets(ownerPosition)}
	h := NewAccountsHandler(nil, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: localDomain}})
	h.store = store

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/104/action", strings.NewReader(`{"type":"suspend"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", idOf(targetNoRole))
	req = withIdentity(req, int64Ptr(actingAccount), int64Ptr(20))
	rec := httptest.NewRecorder()
	h.TakeAction(rec, req)

	f := &accountsFixture{store: store}
	assertRefused(t, f, rec, http.StatusInternalServerError, "Admin data unavailable")
}

// TestAccountsTakeActionWithoutTransactions checks that without a connection
// pool an account action fails closed.
func TestAccountsTakeActionWithoutTransactions(t *testing.T) {
	f := newAccountsFixture(ownerActor)
	h := NewAccountsHandler(nil, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: localDomain}})
	h.store = f.store
	h.roles = &stubRoles{
		usersByAccount: map[int64]db.User{actingAccount: {ID: 20, AccountID: actingAccount}},
		roles:          map[int64]db.UserRole{20: {ID: 3, Name: ownerActor.name, Permissions: ownerActor.permissions, Position: ownerActor.position}},
	}
	f.mux = http.NewServeMux()
	h.RegisterRoutes(f.mux)

	rec := f.takeAction(idOf(targetNoRole), "silence")
	assertRefused(t, f, rec, http.StatusInternalServerError, "Admin data unavailable")
}

// TestAccountsStateConditions checks the routes whose conditions depend on
// the target's state, not on roles.
func TestAccountsStateConditions(t *testing.T) {
	const (
		notAllowed = "This action is not allowed"
		forbidden  = "Forbidden"
	)
	user := func(account int64) int64 { return account + 100 }

	tests := []struct {
		name    string
		actor   actor
		method  string
		route   string
		account int64

		// wantStatus and wantMessage describe a refusal; zero means the
		// change is made.
		wantStatus  int
		wantMessage string
		wantChange  string
		wantLog     string
		// wantRendered is whether the response is the admin account.
		wantRendered bool
	}{
		// AccountPolicy#destroy?: suspended temporarily.
		{name: "delete an account that isn't suspended", actor: adminActor, method: http.MethodDelete, account: targetNoRole, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "delete a suspended account without a deletion request", actor: adminActor, method: http.MethodDelete, account: targetSuspendedNoRequest, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "delete an account with a deletion request that isn't suspended", actor: adminActor, method: http.MethodDelete, account: targetDeletionRequested, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "delete a temporarily suspended account", actor: adminActor, method: http.MethodDelete, account: targetSuspended, wantChange: "ScheduleAccountDeletion"},
		{name: "delete a temporarily suspended remote account", actor: ownerActor, method: http.MethodDelete, account: targetSuspendedRemotely, wantChange: "ScheduleAccountDeletion"},

		// AccountPolicy#unsuspend?: a local-origin suspension.
		{name: "unsuspend a remote-origin suspension", actor: ownerActor, method: http.MethodPost, route: "unsuspend", account: targetSuspendedRemotely, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "unsuspend an account that isn't suspended", actor: ownerActor, method: http.MethodPost, route: "unsuspend", account: targetNoRole, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "unsuspend a remote account that isn't suspended", actor: ownerActor, method: http.MethodPost, route: "unsuspend", account: targetRemote, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "unsuspend a temporary suspension", actor: moderatorActor, method: http.MethodPost, route: "unsuspend", account: targetSuspended, wantChange: "AdminUnsuspendAccount", wantLog: "unsuspend:Account", wantRendered: true},
		{name: "unsuspend a local suspension without a deletion request", actor: moderatorActor, method: http.MethodPost, route: "unsuspend", account: targetSuspendedNoRequest, wantChange: "AdminUnsuspendAccount", wantLog: "unsuspend:Account", wantRendered: true},

		// UserPolicy#approve? and #reject?: not approved yet, and
		// require_local_account!.
		{name: "approve an approved user", actor: ownerActor, method: http.MethodPost, route: "approve", account: targetNoRole, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "approve a pending user", actor: moderatorActor, method: http.MethodPost, route: "approve", account: targetPending, wantChange: "AdminApproveAccount", wantLog: "approve:User", wantRendered: true},
		{name: "approve a remote account", actor: ownerActor, method: http.MethodPost, route: "approve", account: targetRemote, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "approve a local account without a user", actor: ownerActor, method: http.MethodPost, route: "approve", account: targetLocalNoUser, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "approve the instance actor", actor: ownerActor, method: http.MethodPost, route: "approve", account: targetInstanceActor, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "approve a remote account with a user", actor: ownerActor, method: http.MethodPost, route: "approve", account: targetRemoteWithUser, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "reject an approved user", actor: ownerActor, method: http.MethodPost, route: "reject", account: targetNoRole, wantStatus: http.StatusForbidden, wantMessage: notAllowed},
		{name: "reject a pending user", actor: moderatorActor, method: http.MethodPost, route: "reject", account: targetPending, wantChange: "AdminRejectAccount", wantLog: "reject:User"},
		{name: "reject a remote account", actor: ownerActor, method: http.MethodPost, route: "reject", account: targetRemote, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "reject a local account without a user", actor: ownerActor, method: http.MethodPost, route: "reject", account: targetLocalNoUser, wantStatus: http.StatusForbidden, wantMessage: forbidden},

		// require_local_account! is enable's only condition.
		{name: "enable a local user", actor: moderatorActor, method: http.MethodPost, route: "enable", account: targetNoRole, wantChange: "AdminEnableAccount", wantLog: "enable:User", wantRendered: true},
		{name: "enable a pending user", actor: moderatorActor, method: http.MethodPost, route: "enable", account: targetPending, wantChange: "AdminEnableAccount", wantLog: "enable:User", wantRendered: true},
		{name: "enable a remote account", actor: ownerActor, method: http.MethodPost, route: "enable", account: targetRemote, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "enable a remote account with a user", actor: ownerActor, method: http.MethodPost, route: "enable", account: targetRemoteWithUser, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "reject a remote account with a user", actor: ownerActor, method: http.MethodPost, route: "reject", account: targetRemoteWithUser, wantStatus: http.StatusForbidden, wantMessage: forbidden},
		{name: "enable a local account without a user", actor: ownerActor, method: http.MethodPost, route: "enable", account: targetLocalNoUser, wantStatus: http.StatusForbidden, wantMessage: forbidden},

		// These routes have no role hierarchy, as in Mastodon.
		{name: "Moderator approves a pending Owner", actor: moderatorActor, method: http.MethodPost, route: "approve", account: targetPendingOwner, wantChange: "AdminApproveAccount", wantLog: "approve:User", wantRendered: true},
		{name: "Moderator enables the Owner", actor: moderatorActor, method: http.MethodPost, route: "enable", account: targetOwner, wantChange: "AdminEnableAccount", wantLog: "enable:User", wantRendered: true},
		{name: "Moderator unsilences the Owner", actor: moderatorActor, method: http.MethodPost, route: "unsilence", account: targetOwner, wantChange: "AdminUnsilenceAccount", wantLog: "unsilence:Account", wantRendered: true},
		{name: "Moderator unsensitizes the Owner", actor: moderatorActor, method: http.MethodPost, route: "unsensitive", account: targetOwner, wantChange: "AdminUnsensitizeAccount", wantLog: "unsensitive:Account", wantRendered: true},
		{name: "Admin deletes a temporarily suspended Owner", actor: adminActor, method: http.MethodDelete, account: targetSuspendedOwner, wantChange: "ScheduleAccountDeletion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(tt.actor)
			path := "/api/v1/admin/accounts/" + idOf(tt.account)
			if tt.route != "" {
				path += "/" + tt.route
			}
			rec := f.serve(tt.method, path, "")

			if tt.wantStatus != 0 {
				assertRefused(t, f, rec, tt.wantStatus, tt.wantMessage)
				return
			}

			var logs []string
			if tt.wantLog != "" {
				logged := tt.account
				if strings.HasSuffix(tt.wantLog, ":User") {
					logged = user(tt.account)
				}
				logs = []string{fmt.Sprintf("%s:%d", tt.wantLog, logged)}
			}
			assertAllowed(t, f, rec, []string{fmt.Sprintf("%s(%d)", tt.wantChange, tt.account)}, logs)

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
			}
			if tt.wantRendered {
				if body["id"] != idOf(tt.account) {
					t.Fatalf("body = %v, want the account %d", body, tt.account)
				}
			} else if len(body) != 0 {
				t.Fatalf("body = %v, want {}", body)
			}
		})
	}
}

// TestAccountsWriteRoutesNotFound checks that every write route answers 404
// for an ID that isn't a number or names no account, and changes nothing.
func TestAccountsWriteRoutesNotFound(t *testing.T) {
	routes := []struct{ method, route, body string }{
		{http.MethodDelete, "", ""},
		{http.MethodPost, "action", `{"type":"none"}`},
		{http.MethodPost, "approve", ""},
		{http.MethodPost, "reject", ""},
		{http.MethodPost, "enable", ""},
		{http.MethodPost, "unsilence", ""},
		{http.MethodPost, "unsuspend", ""},
		{http.MethodPost, "unsensitive", ""},
	}
	ids := []struct{ name, id string }{
		{"malformed", "abc"},
		{"out of range", "99999999999999999999"},
		{"unknown", idOf(targetUnknown)},
	}

	for _, route := range routes {
		for _, tt := range ids {
			t.Run(fmt.Sprintf("%s %s %s", route.method, route.route, tt.name), func(t *testing.T) {
				f := newAccountsFixture(ownerActor)
				path := "/api/v1/admin/accounts/" + tt.id
				if route.route != "" {
					path += "/" + route.route
				}
				rec := f.serve(route.method, path, route.body)
				assertRefused(t, f, rec, http.StatusNotFound, "Record not found")
			})
		}
	}
}

// TestAccountsWriteRoutesLookupFails checks that a failed target lookup is a
// server error, not a missing account.
func TestAccountsWriteRoutesLookupFails(t *testing.T) {
	routes := []struct{ method, route string }{
		{http.MethodDelete, ""},
		{http.MethodPost, "approve"},
		{http.MethodPost, "reject"},
		{http.MethodPost, "enable"},
		{http.MethodPost, "unsilence"},
		{http.MethodPost, "unsuspend"},
		{http.MethodPost, "unsensitive"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.route, func(t *testing.T) {
			f := newAccountsFixture(ownerActor)
			f.store.targetErr = errDatabase
			path := "/api/v1/admin/accounts/" + idOf(targetSuspended)
			if route.route != "" {
				path += "/" + route.route
			}
			rec := f.serve(route.method, path, "")
			assertRefused(t, f, rec, http.StatusInternalServerError, "Database error")
		})
	}
}

// TestAccountsDeleteSchedulesDeletion checks that deleting a temporarily
// suspended account starts its deletion in the background with the options
// of Admin::AccountDeletionWorker, and answers 500 when it can't.
func TestAccountsDeleteSchedulesDeletion(t *testing.T) {
	t.Run("keeps the username and the e-mail address", func(t *testing.T) {
		f := newAccountsFixture(adminActor)
		rec := f.serve(http.MethodDelete, "/api/v1/admin/accounts/"+idOf(targetSuspended), "")
		assertAllowed(t, f, rec, []string{fmt.Sprintf("ScheduleAccountDeletion(%d)", targetSuspended)}, nil)
		want := services.AccountDeletionOptions{ReserveUsername: true, ReserveEmail: true}
		if len(f.deletions.options) != 1 || f.deletions.options[0] != want {
			t.Fatalf("options = %+v, want %+v", f.deletions.options, want)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "{}" {
			t.Fatalf("body = %s, want {}", body)
		}
	})

	t.Run("scheduling fails", func(t *testing.T) {
		f := newAccountsFixture(adminActor)
		f.store.failOn = map[string]error{"ScheduleAccountDeletion": errors.New("queue down")}
		rec := f.serve(http.MethodDelete, "/api/v1/admin/accounts/"+idOf(targetSuspended), "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := errorMessage(t, rec); got != "Failed to schedule account deletion" {
			t.Fatalf("error = %q", got)
		}
	})

	t.Run("no scheduler", func(t *testing.T) {
		f := newAccountsFixture(adminActor)
		h := NewAccountsHandler(nil, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: localDomain}})
		h.store = f.store
		h.roles = &stubRoles{
			usersByAccount: map[int64]db.User{actingAccount: {ID: 20, AccountID: actingAccount}},
			roles:          map[int64]db.UserRole{20: {ID: 3, Name: adminActor.name, Permissions: adminActor.permissions, Position: adminActor.position}},
		}
		f.mux = http.NewServeMux()
		h.RegisterRoutes(f.mux)
		rec := f.serve(http.MethodDelete, "/api/v1/admin/accounts/"+idOf(targetSuspended), "")
		assertRefused(t, f, rec, http.StatusInternalServerError, "Account deletion unavailable")
	})
}
