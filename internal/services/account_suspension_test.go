package services

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
)

var (
	_ AccountSuspensionStore = (*db.Queries)(nil)
	_ SuspensionDeliverer    = (*federation.OutgoingService)(nil)
)

// suspensionStore keeps accounts, follows and follow requests in memory and
// records what a suspension changes.
type suspensionStore struct {
	accounts       map[int64]db.Account
	accountErr     error
	follows        []db.Follow
	followRequests []db.FollowRequest
	listErr        error

	followListCalls []db.ListFollowsByAccountToLocalParams
	trendDeletes    []int64
}

func (s *suspensionStore) GetAccountByIDAllowSuspended(ctx context.Context, id int64) (db.Account, error) {
	if s.accountErr != nil {
		return db.Account{}, s.accountErr
	}
	account, ok := s.accounts[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *suspensionStore) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if account, ok := s.accounts[id]; ok {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (s *suspensionStore) isLocal(id int64) bool {
	account, ok := s.accounts[id]
	return ok && !account.Domain.Valid
}

func (s *suspensionStore) ListFollowsByAccountToLocal(ctx context.Context, arg db.ListFollowsByAccountToLocalParams) ([]db.Follow, error) {
	s.followListCalls = append(s.followListCalls, arg)
	if s.listErr != nil {
		return nil, s.listErr
	}
	var rows []db.Follow
	for _, follow := range s.follows {
		if follow.AccountID == arg.AccountID && follow.ID > arg.AfterID && s.isLocal(follow.TargetAccountID) && len(rows) < int(arg.Limit) {
			rows = append(rows, follow)
		}
	}
	return rows, nil
}

func (s *suspensionStore) ListFollowRequestsByAccountToLocal(ctx context.Context, arg db.ListFollowRequestsByAccountToLocalParams) ([]db.FollowRequest, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var rows []db.FollowRequest
	for _, request := range s.followRequests {
		if request.AccountID == arg.AccountID && request.ID > arg.AfterID && s.isLocal(request.TargetAccountID) && len(rows) < int(arg.Limit) {
			rows = append(rows, request)
		}
	}
	return rows, nil
}

func (s *suspensionStore) DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) error {
	kept := s.follows[:0]
	for _, follow := range s.follows {
		if follow.AccountID != arg.AccountID || follow.TargetAccountID != arg.TargetAccountID {
			kept = append(kept, follow)
		}
	}
	s.follows = kept
	return nil
}

func (s *suspensionStore) DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error {
	kept := s.followRequests[:0]
	for _, request := range s.followRequests {
		if request.AccountID != arg.AccountID || request.TargetAccountID != arg.TargetAccountID {
			kept = append(kept, request)
		}
	}
	s.followRequests = kept
	return nil
}

func (s *suspensionStore) DeleteStatusTrendsByAccount(ctx context.Context, accountID int64) error {
	s.trendDeletes = append(s.trendDeletes, accountID)
	return nil
}

// recordingSuspensionDeliverer records deliveries; it fails a rejection of
// a follow or request whose ID is in fail.
type recordingSuspensionDeliverer struct {
	suspensions []db.Account
	rejections  []federation.FollowRejection
	fail        map[int64]error
}

func (d *recordingSuspensionDeliverer) DeliverAccountSuspension(ctx context.Context, account db.Account) error {
	d.suspensions = append(d.suspensions, account)
	return nil
}

func (d *recordingSuspensionDeliverer) DeliverFollowReject(ctx context.Context, rejection federation.FollowRejection) error {
	if err := d.fail[rejection.ID]; err != nil {
		return err
	}
	d.rejections = append(d.rejections, rejection)
	return nil
}

func suspensionAccounts() map[int64]db.Account {
	suspendedAt := pgtype.Timestamptz{Time: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), Valid: true}
	remote := pgtype.Text{String: "remote.example", Valid: true}
	return map[int64]db.Account{
		1:  {ID: 1, Username: "alice", Uri: "https://local.test/users/alice"},
		2:  {ID: 2, Username: "carol", Uri: "https://local.test/users/carol"},
		3:  {ID: 3, Username: "frank", Uri: "https://local.test/users/frank"},
		10: {ID: 10, Username: "bob", Domain: remote, Uri: "https://remote.example/users/bob", InboxUrl: "https://remote.example/users/bob/inbox", SuspendedAt: suspendedAt},
		11: {ID: 11, Username: "dave", Domain: remote, Uri: "https://remote.example/users/dave"},
		20: {ID: 20, Username: "erin", Uri: "https://local.test/users/erin", SuspendedAt: suspendedAt},
	}
}

func rejectionKeys(rejections []federation.FollowRejection) []string {
	keys := make([]string, 0, len(rejections))
	for _, r := range rejections {
		kind := "follow"
		if r.Request {
			kind = "request"
		}
		keys = append(keys, kind+":"+r.Follower.Username+"->"+r.Target.Username+":"+r.URI.String)
	}
	sort.Strings(keys)
	return keys
}

func TestAccountSuspensionRemoteAccountRejectsFollowsOfLocalAccounts(t *testing.T) {
	store := &suspensionStore{
		accounts: suspensionAccounts(),
		follows: []db.Follow{
			{ID: 1, AccountID: 10, TargetAccountID: 1, Uri: pgtype.Text{String: "https://remote.example/follows/1", Valid: true}},
			{ID: 2, AccountID: 10, TargetAccountID: 11}, // a remote target: left alone
			{ID: 3, AccountID: 11, TargetAccountID: 1},  // another follower of alice
			{ID: 4, AccountID: 10, TargetAccountID: 2, Uri: pgtype.Text{String: "https://remote.example/follows/4", Valid: true}},
		},
		followRequests: []db.FollowRequest{
			{ID: 1, AccountID: 10, TargetAccountID: 3, Uri: pgtype.Text{String: "https://remote.example/follows/9", Valid: true}},
			{ID: 2, AccountID: 11, TargetAccountID: 3},
		},
	}
	deliverer := &recordingSuspensionDeliverer{}
	service := NewAccountSuspensionService(store, deliverer)

	if err := service.Suspend(context.Background(), 10); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	want := []string{
		"follow:bob->alice:https://remote.example/follows/1",
		"follow:bob->carol:https://remote.example/follows/4",
		"request:bob->frank:https://remote.example/follows/9",
	}
	if got := rejectionKeys(deliverer.rejections); !equalStrings(got, want) {
		t.Fatalf("rejections = %v, want %v", got, want)
	}
	for _, rejection := range deliverer.rejections {
		if rejection.Follower.ID != 10 || !rejection.Follower.SuspendedAt.Valid {
			t.Fatalf("rejection follower = %+v, want the suspended account", rejection.Follower)
		}
	}
	if len(deliverer.suspensions) != 0 {
		t.Fatalf("delivered an actor suspension for a remote account")
	}
	if got := followIDs(store.follows); !equalInts(got, []int64{2, 3}) {
		t.Fatalf("follows left = %v, want [2 3]", got)
	}
	if len(store.followRequests) != 1 || store.followRequests[0].ID != 2 {
		t.Fatalf("follow requests left = %+v, want only request 2", store.followRequests)
	}
	if !equalInts(store.trendDeletes, []int64{10}) {
		t.Fatalf("trend deletes = %v, want [10]", store.trendDeletes)
	}

	// Suspending again finds nothing more to reject.
	deliverer.rejections = nil
	if err := service.Suspend(context.Background(), 10); err != nil {
		t.Fatalf("Suspend again: %v", err)
	}
	if len(deliverer.rejections) != 0 {
		t.Fatalf("repeated suspension rejected %v", rejectionKeys(deliverer.rejections))
	}
	if got := followIDs(store.follows); !equalInts(got, []int64{2, 3}) {
		t.Fatalf("follows left after repeat = %v, want [2 3]", got)
	}
}

func TestAccountSuspensionLocalAccountDeliversSuspendedActor(t *testing.T) {
	store := &suspensionStore{
		accounts: suspensionAccounts(),
		follows: []db.Follow{
			{ID: 1, AccountID: 20, TargetAccountID: 1},
			{ID: 2, AccountID: 11, TargetAccountID: 20},
		},
		followRequests: []db.FollowRequest{
			{ID: 1, AccountID: 11, TargetAccountID: 20},
			{ID: 2, AccountID: 20, TargetAccountID: 2},
		},
	}
	deliverer := &recordingSuspensionDeliverer{}
	service := NewAccountSuspensionService(store, deliverer)

	for run := 1; run <= 2; run++ {
		if err := service.Suspend(context.Background(), 20); err != nil {
			t.Fatalf("Suspend run %d: %v", run, err)
		}
		if len(deliverer.suspensions) != run || deliverer.suspensions[run-1].ID != 20 {
			t.Fatalf("run %d: suspensions = %+v, want account 20 once per run", run, deliverer.suspensions)
		}
	}
	if len(deliverer.rejections) != 0 {
		t.Fatalf("rejected %v for a local account", rejectionKeys(deliverer.rejections))
	}
	if len(store.follows) != 2 || len(store.followRequests) != 2 {
		t.Fatalf("follows %d and follow requests %d changed, want both 2", len(store.follows), len(store.followRequests))
	}
	if !equalInts(store.trendDeletes, []int64{20, 20}) {
		t.Fatalf("trend deletes = %v, want [20 20]", store.trendDeletes)
	}
}

func TestAccountSuspensionSkipsAccountsThatAreNotSuspended(t *testing.T) {
	store := &suspensionStore{
		accounts: suspensionAccounts(),
		follows:  []db.Follow{{ID: 1, AccountID: 11, TargetAccountID: 1}},
	}
	deliverer := &recordingSuspensionDeliverer{}
	service := NewAccountSuspensionService(store, deliverer)

	for _, id := range []int64{1, 11, 404} {
		if err := service.Suspend(context.Background(), id); err != nil {
			t.Fatalf("Suspend(%d): %v", id, err)
		}
	}
	if len(deliverer.suspensions) != 0 || len(deliverer.rejections) != 0 || len(store.trendDeletes) != 0 || len(store.follows) != 1 {
		t.Fatalf("an unsuspended or missing account changed something: suspensions %d, rejections %d, trend deletes %v, follows %d",
			len(deliverer.suspensions), len(deliverer.rejections), store.trendDeletes, len(store.follows))
	}
}

func TestAccountSuspensionRetryAfterDeliveryFailure(t *testing.T) {
	accounts := suspensionAccounts()
	noInbox := accounts[10]
	noInbox.ID, noInbox.Username, noInbox.InboxUrl = 12, "gina", ""
	accounts[12] = noInbox
	store := &suspensionStore{
		accounts: accounts,
		follows: []db.Follow{
			{ID: 1, AccountID: 10, TargetAccountID: 1},
			{ID: 2, AccountID: 10, TargetAccountID: 2},
			{ID: 3, AccountID: 12, TargetAccountID: 1},
		},
	}
	queueFull := errors.New("queue full")
	deliverer := &recordingSuspensionDeliverer{fail: map[int64]error{1: queueFull}}
	service := NewAccountSuspensionService(store, deliverer)

	err := service.Suspend(context.Background(), 10)
	if !errors.Is(err, queueFull) {
		t.Fatalf("Suspend: err = %v, want the delivery failure", err)
	}
	if got := followIDs(store.follows); !equalInts(got, []int64{1, 3}) {
		t.Fatalf("follows left = %v, want the failed follow 1 kept and 2 deleted", got)
	}

	delete(deliverer.fail, 1)
	if err := service.Suspend(context.Background(), 10); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := followIDs(store.follows); !equalInts(got, []int64{3}) {
		t.Fatalf("follows left after retry = %v, want [3]", got)
	}

	// A follower without an inbox can't be sent the Reject; its follow
	// goes all the same.
	deliverer.fail = map[int64]error{3: federation.ErrInboxMissing}
	if err := service.Suspend(context.Background(), 12); err != nil {
		t.Fatalf("Suspend without inbox: %v", err)
	}
	if len(store.follows) != 0 {
		t.Fatalf("follows left = %v, want none", followIDs(store.follows))
	}
}

func TestAccountSuspensionPagesThroughFollows(t *testing.T) {
	store := &suspensionStore{accounts: suspensionAccounts()}
	for id := int64(1); id <= 5; id++ {
		target := 100 + id
		store.accounts[target] = db.Account{ID: target, Username: "local"}
		store.follows = append(store.follows, db.Follow{ID: id, AccountID: 10, TargetAccountID: target})
	}
	deliverer := &recordingSuspensionDeliverer{fail: map[int64]error{2: errors.New("unavailable")}}
	service := NewAccountSuspensionService(store, deliverer)
	service.batchSize = 2

	if err := service.Suspend(context.Background(), 10); err == nil {
		t.Fatal("Suspend: want the failure of follow 2")
	}
	if len(deliverer.rejections) != 4 {
		t.Fatalf("rejected %d follows, want the 4 that didn't fail", len(deliverer.rejections))
	}
	var afterIDs []int64
	for _, call := range store.followListCalls {
		if call.Limit != 2 {
			t.Fatalf("limit = %d, want 2", call.Limit)
		}
		afterIDs = append(afterIDs, call.AfterID)
	}
	if !equalInts(afterIDs, []int64{0, 2, 4}) {
		t.Fatalf("pages after %v, want [0 2 4]", afterIDs)
	}
}

func TestAccountSuspensionErrors(t *testing.T) {
	if err := (*AccountSuspensionService)(nil).Suspend(context.Background(), 1); !errors.Is(err, ErrAccountSuspensionMissingDeps) {
		t.Fatalf("nil service: err = %v", err)
	}
	if err := NewAccountSuspensionService(&suspensionStore{}, nil).Suspend(context.Background(), 1); !errors.Is(err, ErrAccountSuspensionMissingDeps) {
		t.Fatalf("no deliverer: err = %v", err)
	}

	dbDown := errors.New("db down")
	store := &suspensionStore{accountErr: dbDown}
	if err := NewAccountSuspensionService(store, &recordingSuspensionDeliverer{}).Suspend(context.Background(), 10); !errors.Is(err, dbDown) {
		t.Fatalf("account lookup failure: err = %v", err)
	}

	store = &suspensionStore{accounts: suspensionAccounts(), listErr: dbDown}
	if err := NewAccountSuspensionService(store, &recordingSuspensionDeliverer{}).Suspend(context.Background(), 10); !errors.Is(err, dbDown) {
		t.Fatalf("follow listing failure: err = %v", err)
	}
}

func followIDs(follows []db.Follow) []int64 {
	ids := make([]int64, 0, len(follows))
	for _, follow := range follows {
		ids = append(ids, follow.ID)
	}
	return ids
}

func equalInts(a, b []int64) bool {
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
