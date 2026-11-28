package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/conversations"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/streaming"
)

var (
	_ AccountDeletionStore      = (*db.Queries)(nil)
	_ AccountDeletionBatchStore = (*db.Queries)(nil)
	_ AccountDeletionDeliverer  = (*federation.OutgoingService)(nil)
	_ AccountDeletionTransactor = DBTransactor{}
	_ streaming.Publisher       = (*recordingPublisher)(nil)
)

// deletionStore keeps an account deletion's data in memory. It logs each
// change, counts the rows that each batched delete has left, and fails a
// call named in fail.
type deletionStore struct {
	*suspensionStore
	conversations.ConversationStore

	pending  map[int64]bool
	users    map[int64]db.User
	inboxes  []string
	reported []int64
	statuses []db.Status
	media    []db.MediaAttachment
	rows     map[string]int64
	fail     map[string]error

	log []string
}

func (s *deletionStore) record(call string) error {
	s.log = append(s.log, call)
	return s.fail[call]
}

func (s *deletionStore) HasAccountDeletionRequest(ctx context.Context, accountID int64) (bool, error) {
	return s.pending[accountID], nil
}

func (s *deletionStore) GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error) {
	user, ok := s.users[accountID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *deletionStore) DisableAccountDeletionUser(ctx context.Context, accountID int64) error {
	return s.record("DisableAccountDeletionUser")
}

func (s *deletionStore) ListRemoteInboxes(ctx context.Context, arg db.ListRemoteInboxesParams) ([]string, error) {
	if err := s.record("ListRemoteInboxes " + arg.After); err != nil {
		return nil, err
	}
	var page []string
	for _, inbox := range s.inboxes {
		if inbox > arg.After && len(page) < int(arg.Limit) {
			page = append(page, inbox)
		}
	}
	return page, nil
}

func (s *deletionStore) ListLocalFollowsOfAccount(ctx context.Context, arg db.ListLocalFollowsOfAccountParams) ([]db.Follow, error) {
	var rows []db.Follow
	for _, follow := range s.follows {
		if follow.TargetAccountID == arg.AccountID && follow.ID > arg.AfterID && s.isLocal(follow.AccountID) && len(rows) < int(arg.Limit) {
			rows = append(rows, follow)
		}
	}
	return rows, nil
}

func (s *deletionStore) PurgeAccountProfile(ctx context.Context, id int64) error {
	return s.record("PurgeAccountProfile")
}

func (s *deletionStore) ListUnresolvedReportedStatusIDs(ctx context.Context, targetAccountID int64) ([]int64, error) {
	if err := s.record("ListUnresolvedReportedStatusIDs"); err != nil {
		return nil, err
	}
	return s.reported, nil
}

func (s *deletionStore) ListAccountStatusesForDeletion(ctx context.Context, arg db.ListAccountStatusesForDeletionParams) ([]db.Status, error) {
	keep := make(map[int64]bool, len(arg.KeepIds))
	for _, id := range arg.KeepIds {
		keep[id] = true
	}
	var page []db.Status
	for _, status := range s.statuses {
		if status.AccountID == arg.AccountID && status.ID > arg.AfterID && !keep[status.ID] && len(page) < int(arg.Limit) {
			page = append(page, status)
		}
	}
	return page, nil
}

func (s *deletionStore) ListAccountMediaAttachmentsForDeletion(ctx context.Context, arg db.ListAccountMediaAttachmentsForDeletionParams) ([]db.MediaAttachment, error) {
	keep := make(map[int64]bool, len(arg.KeepStatusIds))
	for _, id := range arg.KeepStatusIds {
		keep[id] = true
	}
	var page []db.MediaAttachment
	for _, attachment := range s.media {
		if attachment.ID > arg.AfterID && !(attachment.StatusID.Valid && keep[attachment.StatusID.Int64]) && len(page) < int(arg.Limit) {
			page = append(page, attachment)
		}
	}
	return page, nil
}

func (s *deletionStore) DeleteMediaAttachmentByID(ctx context.Context, id int64) error {
	if err := s.record(fmt.Sprintf("DeleteMediaAttachmentByID %d", id)); err != nil {
		return err
	}
	kept := s.media[:0]
	for _, attachment := range s.media {
		if attachment.ID != id {
			kept = append(kept, attachment)
		}
	}
	s.media = kept
	return nil
}

// batch deletes up to limit of the rows left under name.
func (s *deletionStore) batch(name string, limit int32) (int64, error) {
	if err := s.record(name); err != nil {
		return 0, err
	}
	n := s.rows[name]
	if n > int64(limit) {
		n = int64(limit)
	}
	s.rows[name] -= n
	return n, nil
}

func (s *deletionStore) DeleteAccountMentionsBatch(ctx context.Context, arg db.DeleteAccountMentionsBatchParams) (int64, error) {
	return s.batch("mentions", arg.Limit)
}

func (s *deletionStore) DeleteAccountPollsBatch(ctx context.Context, arg db.DeleteAccountPollsBatchParams) (int64, error) {
	return s.batch("polls", arg.Limit)
}

func (s *deletionStore) DeleteAccountNotificationsBatch(ctx context.Context, arg db.DeleteAccountNotificationsBatchParams) (int64, error) {
	return s.batch("notifications", arg.Limit)
}

func (s *deletionStore) DeleteAccountNotificationRequestsBatch(ctx context.Context, arg db.DeleteAccountNotificationRequestsBatchParams) (int64, error) {
	return s.batch("notification_requests", arg.Limit)
}

func (s *deletionStore) DeleteAccountFavouritesBatch(ctx context.Context, arg db.DeleteAccountFavouritesBatchParams) (int64, error) {
	return s.batch("favourites", arg.Limit)
}

func (s *deletionStore) DeleteAccountBookmarksBatch(ctx context.Context, arg db.DeleteAccountBookmarksBatchParams) (int64, error) {
	return s.batch("bookmarks", arg.Limit)
}

func (s *deletionStore) DeleteAccountFollowsBatch(ctx context.Context, arg db.DeleteAccountFollowsBatchParams) (int64, error) {
	return s.batch("follows", arg.Limit)
}

func (s *deletionStore) DeleteAccountFollowRequestsBatch(ctx context.Context, arg db.DeleteAccountFollowRequestsBatchParams) (int64, error) {
	return s.batch("follow_requests", arg.Limit)
}

func (s *deletionStore) DeleteAccountBlocksBatch(ctx context.Context, arg db.DeleteAccountBlocksBatchParams) (int64, error) {
	return s.batch("blocks", arg.Limit)
}

func (s *deletionStore) DeleteAccountMutesBatch(ctx context.Context, arg db.DeleteAccountMutesBatchParams) (int64, error) {
	return s.batch("mutes", arg.Limit)
}

func (s *deletionStore) DeleteAccountConversationsBatch(ctx context.Context, arg db.DeleteAccountConversationsBatchParams) (int64, error) {
	return s.batch("conversations", arg.Limit)
}

func (s *deletionStore) PurgeAccountAssociations(ctx context.Context, accountID int64) error {
	return s.record("PurgeAccountAssociations")
}

func (s *deletionStore) DeleteUserByAccountID(ctx context.Context, accountID int64) error {
	if err := s.record("DeleteUserByAccountID"); err != nil {
		return err
	}
	delete(s.users, accountID)
	return nil
}

func (s *deletionStore) DeleteAccount(ctx context.Context, id int64) error {
	if err := s.record("DeleteAccount"); err != nil {
		return err
	}
	delete(s.accounts, id)
	return nil
}

func (s *deletionStore) DeleteAccountDeletionRequest(ctx context.Context, accountID int64) error {
	if err := s.record("DeleteAccountDeletionRequest"); err != nil {
		return err
	}
	delete(s.pending, accountID)
	return nil
}

// GetMentionsByStatusIDs, DecrementRepliesOfDeletedStatuses and
// DeleteAccountStatusesByIDs make deletionStore its own batch store.
func (s *deletionStore) GetMentionsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Mention, error) {
	return nil, nil
}

func (s *deletionStore) DecrementRepliesOfDeletedStatuses(ctx context.Context, arg db.DecrementRepliesOfDeletedStatusesParams) error {
	return s.record(fmt.Sprintf("DecrementRepliesOfDeletedStatuses %v", arg.Ids))
}

func (s *deletionStore) DeleteAccountStatusesByIDs(ctx context.Context, arg db.DeleteAccountStatusesByIDsParams) (int64, error) {
	if err := s.record(fmt.Sprintf("DeleteAccountStatusesByIDs %v", arg.Ids)); err != nil {
		return 0, err
	}
	gone := make(map[int64]bool, len(arg.Ids))
	for _, id := range arg.Ids {
		gone[id] = true
	}
	kept := s.statuses[:0]
	for _, status := range s.statuses {
		if !gone[status.ID] {
			kept = append(kept, status)
		}
	}
	s.statuses = kept
	return int64(len(arg.Ids)), nil
}

// deletionTransactor runs a transaction on the store, logging its
// outcome. It keeps no rollback: a failing call fails before it changes
// anything.
type deletionTransactor struct {
	store *deletionStore
}

func (t deletionTransactor) InTx(ctx context.Context, fn func(AccountDeletionBatchStore) error) error {
	if err := fn(t.store); err != nil {
		t.store.log = append(t.store.log, "rollback")
		return err
	}
	t.store.log = append(t.store.log, "commit")
	return nil
}

// recordingDeletionDeliverer records a deletion's deliveries.
type recordingDeletionDeliverer struct {
	recordingSuspensionDeliverer
	deletions [][]string
	undos     []federation.FollowUndo
	err       error
}

func (d *recordingDeletionDeliverer) DeliverAccountDeletion(ctx context.Context, account db.Account, inboxes []string) error {
	if d.err != nil {
		return d.err
	}
	d.deletions = append(d.deletions, inboxes)
	return nil
}

func (d *recordingDeletionDeliverer) DeliverFollowUndo(ctx context.Context, undo federation.FollowUndo) error {
	if err := d.fail[undo.ID]; err != nil {
		return err
	}
	d.undos = append(d.undos, undo)
	return nil
}

// recordingStorage deletes nothing and records what it is asked to delete.
type recordingStorage struct {
	media.Storage
	deleted []string
	fail    map[string]error
}

func (s *recordingStorage) Delete(ctx context.Context, path string) error {
	if err := s.fail[path]; err != nil {
		return err
	}
	s.deleted = append(s.deleted, path)
	return os.ErrNotExist
}

type publishedDelete struct {
	id      string
	streams []string
}

type recordingPublisher struct {
	deletes []publishedDelete
}

func (p *recordingPublisher) PublishStatusToRedis(ctx context.Context, status any, streams []string) error {
	return nil
}

func (p *recordingPublisher) PublishDeleteToRedis(ctx context.Context, statusID string, streams []string) error {
	p.deletes = append(p.deletes, publishedDelete{id: statusID, streams: streams})
	return nil
}

func (p *recordingPublisher) PublishNotificationToRedis(ctx context.Context, accountID int64, notification any) error {
	return nil
}

type deletionFixture struct {
	store     *deletionStore
	outgoing  *recordingDeletionDeliverer
	storage   *recordingStorage
	publisher *recordingPublisher
	service   *AccountDeletionService
}

// newDeletionFixture has alice (1), a local account whose deletion has
// begun, with a confirmed user, two pages of known inboxes, three
// statuses, one of them reported, media and rows in every batched table.
func newDeletionFixture() *deletionFixture {
	accounts := suspensionAccounts()
	suspendedAt := pgtype.Timestamptz{Time: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), Valid: true}
	alice := accounts[1]
	alice.SuspendedAt = suspendedAt
	alice.AvatarFileName = pgtype.Text{String: "accounts/avatars/1/a.png", Valid: true}
	alice.HeaderFileName = pgtype.Text{String: "", Valid: true}
	accounts[1] = alice

	store := &deletionStore{
		suspensionStore: &suspensionStore{accounts: accounts},
		pending:         map[int64]bool{1: true},
		users:           map[int64]db.User{1: {ID: 10, AccountID: 1, Approved: true, ConfirmedAt: suspendedAt}},
		inboxes:         []string{"https://a.example/inbox", "https://b.example/inbox", "https://c.example/inbox"},
		reported:        []int64{102},
		statuses: []db.Status{
			{ID: 101, AccountID: 1, Visibility: statusVisibilityPublic},
			{ID: 102, AccountID: 1, Visibility: statusVisibilityPublic},
			{ID: 103, AccountID: 1, Visibility: 1},
			{ID: 104, AccountID: 1, Visibility: statusVisibilityPublic, DeletedAt: suspendedAt},
		},
		media: []db.MediaAttachment{
			{ID: 201, AccountID: pgtype.Int8{Int64: 1, Valid: true}, FileFileName: pgtype.Text{String: "media/201.png", Valid: true}},
			{ID: 202, AccountID: pgtype.Int8{Int64: 1, Valid: true}, StatusID: pgtype.Int8{Int64: 102, Valid: true}, FileFileName: pgtype.Text{String: "media/202.png", Valid: true}},
		},
		rows: map[string]int64{
			"mentions": 3, "polls": 1, "notifications": 5, "notification_requests": 1, "favourites": 2,
			"bookmarks": 2, "follows": 4, "follow_requests": 1, "blocks": 2, "mutes": 1, "conversations": 1,
		},
		fail: map[string]error{},
	}
	outgoing := &recordingDeletionDeliverer{}
	storage := &recordingStorage{}
	publisher := &recordingPublisher{}
	service := NewAccountDeletionService(store, deletionTransactor{store: store}, outgoing, storage, publisher, nil)
	service.statusBatchSize = 2
	service.rowBatchSize = 2
	return &deletionFixture{store: store, outgoing: outgoing, storage: storage, publisher: publisher, service: service}
}

// adminDeletionLog is what deleting alice with AdminAccountDeletionOptions
// does, in order, with batches of two.
var adminDeletionLog = []string{
	"DisableAccountDeletionUser",
	"ListRemoteInboxes ", "ListRemoteInboxes https://b.example/inbox",
	"PurgeAccountProfile",
	"ListUnresolvedReportedStatusIDs",
	"DecrementRepliesOfDeletedStatuses [101 103]", "DeleteAccountStatusesByIDs [101 103]", "commit",
	"DecrementRepliesOfDeletedStatuses [104]", "DeleteAccountStatusesByIDs [104]", "commit",
	"DeleteMediaAttachmentByID 201",
	"mentions", "mentions",
	"polls",
	"notifications", "notifications", "notifications",
	"notification_requests",
	"favourites", "favourites",
	"bookmarks", "bookmarks",
	"follows", "follows", "follows",
	"follow_requests",
	"blocks", "blocks",
	"mutes",
	"conversations",
	"PurgeAccountAssociations",
	"DeleteAccountDeletionRequest",
}

func TestAccountDeletionService_DeleteLocalAccount(t *testing.T) {
	f := newDeletionFixture()
	if err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got, want := strings.Join(f.store.log, "\n"), strings.Join(adminDeletionLog, "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
	if len(f.outgoing.deletions) != 1 || fmt.Sprint(f.outgoing.deletions[0]) != fmt.Sprint(f.store.inboxes) {
		t.Fatalf("actor Delete went to %v, want once to every known inbox %v", f.outgoing.deletions, f.store.inboxes)
	}
	if len(f.outgoing.rejections) != 0 || len(f.outgoing.undos) != 0 || len(f.outgoing.suspensions) != 0 {
		t.Fatalf("a local account's deletion sent rejects %v, undos %v, suspensions %v", f.outgoing.rejections, f.outgoing.undos, f.outgoing.suspensions)
	}
	// The avatar and the unreported attachment are deleted; the reported
	// status and its attachment are kept.
	if want := "[accounts/avatars/1/a.png media/201.png]"; fmt.Sprint(f.storage.deleted) != want {
		t.Fatalf("deleted files %v, want %s", f.storage.deleted, want)
	}
	if len(f.store.statuses) != 1 || f.store.statuses[0].ID != 102 {
		t.Fatalf("statuses left %v, want the reported status 102", f.store.statuses)
	}
	if len(f.store.media) != 1 || f.store.media[0].ID != 202 {
		t.Fatalf("media left %v, want the reported status's attachment 202", f.store.media)
	}
	// Only the live public status is announced as deleted, on the local
	// public streams.
	if len(f.publisher.deletes) != 1 || f.publisher.deletes[0].id != "101" ||
		fmt.Sprint(f.publisher.deletes[0].streams) != fmt.Sprint([]string{streaming.StreamPublic, streaming.StreamPublicLocal}) {
		t.Fatalf("published deletes %v, want 101 on public and public:local", f.publisher.deletes)
	}
	if _, ok := f.store.users[1]; !ok {
		t.Fatal("the user was deleted, want it kept disabled to reserve the e-mail address")
	}
	if _, ok := f.store.accounts[1]; !ok {
		t.Fatal("the account was deleted, want its record kept")
	}
	if f.store.pending[1] {
		t.Fatal("the deletion request is still there")
	}

	// A finished deletion is left alone.
	f.store.log = nil
	if err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("Delete again: %v", err)
	}
	if len(f.store.log) != 0 || len(f.outgoing.deletions) != 1 {
		t.Fatalf("a finished deletion ran again: calls %v, %d actor Deletes", f.store.log, len(f.outgoing.deletions))
	}
}

func TestAccountDeletionService_DeleteOptions(t *testing.T) {
	t.Run("without reserving the e-mail address, the user goes", func(t *testing.T) {
		f := newDeletionFixture()
		if err := f.service.Delete(context.Background(), 1, AccountDeletionOptions{ReserveUsername: true}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		tail := f.store.log[len(f.store.log)-3:]
		if want := "[PurgeAccountAssociations DeleteUserByAccountID DeleteAccountDeletionRequest]"; fmt.Sprint(tail) != want {
			t.Fatalf("last calls %v, want %s", tail, want)
		}
		if _, ok := f.store.accounts[1]; !ok {
			t.Fatal("the account was deleted, want its record kept")
		}
	})

	t.Run("without reserving the username, the record goes with everything", func(t *testing.T) {
		f := newDeletionFixture()
		if err := f.service.Delete(context.Background(), 1, AccountDeletionOptions{}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		for _, call := range f.store.log {
			if call == "PurgeAccountProfile" || call == "ListUnresolvedReportedStatusIDs" || call == "DeleteAccountDeletionRequest" {
				t.Fatalf("called %s for an account whose record goes", call)
			}
		}
		if last := f.store.log[len(f.store.log)-1]; last != "DeleteAccount" {
			t.Fatalf("last call %q, want DeleteAccount", last)
		}
		if len(f.store.statuses) != 0 {
			t.Fatalf("statuses left %v, want none: reported statuses are kept only with the record", f.store.statuses)
		}
	})

	t.Run("skipping ActivityPub sends nothing", func(t *testing.T) {
		f := newDeletionFixture()
		opts := AdminAccountDeletionOptions()
		opts.SkipActivityPub = true
		if err := f.service.Delete(context.Background(), 1, opts); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if len(f.outgoing.deletions) != 0 {
			t.Fatalf("sent %d actor Deletes", len(f.outgoing.deletions))
		}
		if f.store.pending[1] {
			t.Fatal("the deletion request is still there")
		}
	})

	for name, user := range map[string]db.User{
		"unconfirmed": {ID: 10, AccountID: 1, Approved: true},
		"pending":     {ID: 10, AccountID: 1, ConfirmedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	} {
		t.Run("an account whose user is "+name+" goes whole, quietly", func(t *testing.T) {
			f := newDeletionFixture()
			f.store.users[1] = user
			if err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions()); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if len(f.outgoing.deletions) != 0 {
				t.Fatalf("sent %d actor Deletes for an account nobody else has seen", len(f.outgoing.deletions))
			}
			if last := f.store.log[len(f.store.log)-1]; last != "DeleteAccount" {
				t.Fatalf("last call %q, want DeleteAccount", last)
			}
		})
	}
}

func TestAccountDeletionService_DeleteRemoteAccount(t *testing.T) {
	f := newDeletionFixture()
	// bob (10) is a suspended remote account; alice (1) and frank (3) are
	// local, dave (11) is remote.
	f.store.pending[10] = true
	f.store.statuses = nil
	f.store.media = nil
	f.store.follows = []db.Follow{
		{ID: 1, AccountID: 10, TargetAccountID: 1, Uri: pgtype.Text{String: "https://remote.example/follows/1", Valid: true}},
		{ID: 2, AccountID: 1, TargetAccountID: 10},
		{ID: 3, AccountID: 11, TargetAccountID: 10},
		{ID: 4, AccountID: 3, TargetAccountID: 10},
	}
	f.store.followRequests = []db.FollowRequest{{ID: 5, AccountID: 10, TargetAccountID: 1}}

	if err := f.service.Delete(context.Background(), 10, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if len(f.outgoing.deletions) != 0 {
		t.Fatalf("sent %d actor Deletes for a remote account", len(f.outgoing.deletions))
	}
	var rejected, undone []int64
	for _, rejection := range f.outgoing.rejections {
		if rejection.Follower.ID != 10 {
			t.Fatalf("rejection %+v, want one of bob's follows", rejection)
		}
		rejected = append(rejected, rejection.ID)
	}
	for _, undo := range f.outgoing.undos {
		if undo.Target.ID != 10 || undo.Follower.Domain.Valid {
			t.Fatalf("undo %+v, want a local account's follow of bob", undo)
		}
		undone = append(undone, undo.ID)
	}
	if fmt.Sprint(rejected) != "[1 5]" {
		t.Fatalf("rejected %v, want bob's follow 1 and follow request 5 of alice", rejected)
	}
	if fmt.Sprint(undone) != "[2 4]" {
		t.Fatalf("undid %v, want alice's and frank's follows 2 and 4 of bob", undone)
	}
	var left []int64
	for _, follow := range f.store.follows {
		left = append(left, follow.ID)
	}
	if fmt.Sprint(left) != "[3]" {
		t.Fatalf("follows left %v, want only dave's, which the follows purge deletes", left)
	}
	for _, call := range f.store.log {
		if call == "DisableAccountDeletionUser" || call == "DeleteUserByAccountID" {
			t.Fatalf("called %s for a remote account", call)
		}
	}
	if f.store.pending[10] {
		t.Fatal("the deletion request is still there")
	}
}

func TestAccountDeletionService_DeleteLeavesOthersAlone(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *deletionFixture)
	}{
		{"missing account", func(f *deletionFixture) { delete(f.store.accounts, 1) }},
		{"account that isn't suspended", func(f *deletionFixture) {
			alice := f.store.accounts[1]
			alice.SuspendedAt = pgtype.Timestamptz{}
			f.store.accounts[1] = alice
		}},
		{"suspended account without a deletion request", func(f *deletionFixture) { delete(f.store.pending, 1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newDeletionFixture()
			tt.setup(f)
			if err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions()); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if len(f.store.log) != 0 || len(f.outgoing.deletions) != 0 || len(f.storage.deleted) != 0 {
				t.Fatalf("changed %v, delivered %v, deleted files %v; want nothing", f.store.log, f.outgoing.deletions, f.storage.deleted)
			}
		})
	}

	t.Run("missing dependencies", func(t *testing.T) {
		f := newDeletionFixture()
		f.service.storage = nil
		if err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions()); !errors.Is(err, ErrAccountDeletionMissingDeps) {
			t.Fatalf("err = %v, want ErrAccountDeletionMissingDeps", err)
		}
		var nilService *AccountDeletionService
		if err := nilService.Delete(context.Background(), 1, AdminAccountDeletionOptions()); !errors.Is(err, ErrAccountDeletionMissingDeps) {
			t.Fatalf("nil service: err = %v, want ErrAccountDeletionMissingDeps", err)
		}
	})
}

// TestAccountDeletionService_DeleteResumesAfterFailure fails a deletion at
// each kind of step, then runs it again: the failed run stops at the
// failure and keeps the deletion request, and the second run finishes the
// work without repeating any of it.
func TestAccountDeletionService_DeleteResumesAfterFailure(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		fail func(f *deletionFixture)
		// last is the last call the failed run makes.
		last string
	}{
		{"disabling the user", func(f *deletionFixture) { f.store.fail["DisableAccountDeletionUser"] = boom }, "DisableAccountDeletionUser"},
		{"listing inboxes", func(f *deletionFixture) { f.store.fail["ListRemoteInboxes https://b.example/inbox"] = boom }, "ListRemoteInboxes https://b.example/inbox"},
		{"delivering the actor Delete", func(f *deletionFixture) { f.outgoing.err = boom }, "ListRemoteInboxes https://b.example/inbox"},
		{"purging the profile", func(f *deletionFixture) { f.store.fail["PurgeAccountProfile"] = boom }, "PurgeAccountProfile"},
		{"deleting the profile images", func(f *deletionFixture) { f.storage.fail = map[string]error{"accounts/avatars/1/a.png": boom} }, "ListRemoteInboxes https://b.example/inbox"},
		{"deleting the second batch of statuses", func(f *deletionFixture) { f.store.fail["DeleteAccountStatusesByIDs [104]"] = boom }, "rollback"},
		{"deleting a media file", func(f *deletionFixture) { f.storage.fail = map[string]error{"media/201.png": boom} }, "commit"},
		{"deleting a media attachment", func(f *deletionFixture) { f.store.fail["DeleteMediaAttachmentByID 201"] = boom }, "DeleteMediaAttachmentByID 201"},
		{"deleting a batch of follows", func(f *deletionFixture) { f.store.fail["follows"] = boom }, "follows"},
		{"deleting other associations", func(f *deletionFixture) { f.store.fail["PurgeAccountAssociations"] = boom }, "PurgeAccountAssociations"},
		{"fulfilling the deletion request", func(f *deletionFixture) { f.store.fail["DeleteAccountDeletionRequest"] = boom }, "DeleteAccountDeletionRequest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newDeletionFixture()
			tt.fail(f)
			err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions())
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want boom", err)
			}
			if last := f.store.log[len(f.store.log)-1]; last != tt.last {
				t.Fatalf("failed run ended with %q, want %q; calls %v", last, tt.last, f.store.log)
			}
			if !f.store.pending[1] {
				t.Fatal("a failed deletion dropped its deletion request")
			}
			firstRun := len(f.store.log)

			f.store.fail = map[string]error{}
			f.storage.fail = nil
			f.outgoing.err = nil
			if err := f.service.Delete(context.Background(), 1, AdminAccountDeletionOptions()); err != nil {
				t.Fatalf("second run: %v", err)
			}
			if f.store.pending[1] {
				t.Fatal("the deletion request is still there after the second run")
			}
			if len(f.store.statuses) != 1 || len(f.store.media) != 1 {
				t.Fatalf("left statuses %v and media %v, want only the reported ones", f.store.statuses, f.store.media)
			}
			for name, left := range f.store.rows {
				if left != 0 {
					t.Fatalf("%d %s left", left, name)
				}
			}
			// The second run deletes no batch of statuses that the first
			// committed.
			committed := map[string]bool{}
			for i, call := range f.store.log[:firstRun] {
				if call == "commit" {
					committed[f.store.log[i-1]] = true
				}
			}
			for _, call := range f.store.log[firstRun:] {
				if strings.HasPrefix(call, "DeleteAccountStatusesByIDs") && committed[call] {
					t.Fatalf("second run repeated %s", call)
				}
			}
		})
	}
}

// TestAccountDeletionService_DeleteRemoteAccountResumes fails a Reject and
// an Undo: the failed run keeps those follows, purges nothing and keeps the
// deletion request; the second run sends what was missing only.
func TestAccountDeletionService_DeleteRemoteAccountResumes(t *testing.T) {
	f := newDeletionFixture()
	f.store.pending[10] = true
	f.store.follows = []db.Follow{
		{ID: 1, AccountID: 10, TargetAccountID: 1},
		{ID: 2, AccountID: 10, TargetAccountID: 3},
		{ID: 3, AccountID: 1, TargetAccountID: 10},
		{ID: 4, AccountID: 3, TargetAccountID: 10},
	}
	boom := errors.New("boom")
	f.outgoing.fail = map[int64]error{1: boom, 4: boom}

	if err := f.service.Delete(context.Background(), 10, AdminAccountDeletionOptions()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if !f.store.pending[10] {
		t.Fatal("a failed deletion dropped its deletion request")
	}
	for _, call := range f.store.log {
		if call == "PurgeAccountProfile" || call == "follows" {
			t.Fatalf("a deletion whose activities failed went on to %s", call)
		}
	}
	var left []int64
	for _, follow := range f.store.follows {
		left = append(left, follow.ID)
	}
	if fmt.Sprint(left) != "[1 4]" {
		t.Fatalf("follows left %v, want the two whose activities failed", left)
	}

	f.outgoing.fail = nil
	if err := f.service.Delete(context.Background(), 10, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var rejected, undone []int64
	for _, rejection := range f.outgoing.rejections {
		rejected = append(rejected, rejection.ID)
	}
	for _, undo := range f.outgoing.undos {
		undone = append(undone, undo.ID)
	}
	if fmt.Sprint(rejected) != "[2 1]" || fmt.Sprint(undone) != "[3 4]" {
		t.Fatalf("rejected %v and undid %v, want each follow once", rejected, undone)
	}
	if f.store.pending[10] {
		t.Fatal("the deletion request is still there after the second run")
	}
}

func TestAccountDeletionService_DeleteStopsWhenCancelled(t *testing.T) {
	f := newDeletionFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.service.Delete(ctx, 1, AdminAccountDeletionOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !f.store.pending[1] {
		t.Fatal("a cancelled deletion dropped its deletion request")
	}
}
