package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/conversations"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/streaming"
)

const (
	// accountDeletionStatusBatchSize is how many statuses one transaction
	// deletes.
	accountDeletionStatusBatchSize = 100
	// accountDeletionRowBatchSize is how many rows one statement deletes,
	// or one listing reads, as in_batches does in Mastodon.
	accountDeletionRowBatchSize = 1000

	statusVisibilityPublic = 0
	statusVisibilityDirect = 3
)

var ErrAccountDeletionMissingDeps = errors.New("account deletion missing dependencies")

// AccountDeletionOptions are the options of Mastodon's DeleteAccountService.
type AccountDeletionOptions struct {
	// ReserveUsername keeps the account's record, suspended and emptied, so
	// that its username stays taken and its actor answers 410 Gone
	// (reserve_username). Without it the record is deleted, and with it
	// everything that is left of the account.
	ReserveUsername bool `json:"reserve_username"`
	// ReserveEmail keeps a local account's user, disabled, so that its
	// e-mail address stays taken (reserve_email). Without it the user is
	// deleted.
	ReserveEmail bool `json:"reserve_email"`
	// SkipActivityPub sends no activities (skip_activitypub).
	SkipActivityPub bool `json:"skip_activitypub"`
}

// AdminAccountDeletionOptions are the options of a deletion that a
// moderator asks for (Admin::AccountDeletionWorker): the username and the
// e-mail address stay taken.
func AdminAccountDeletionOptions() AccountDeletionOptions {
	return AccountDeletionOptions{ReserveUsername: true, ReserveEmail: true}
}

// AccountDeletionStore describes DB access for account deletion.
type AccountDeletionStore interface {
	AccountSuspensionStore
	HasAccountDeletionRequest(ctx context.Context, accountID int64) (bool, error)
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	DisableAccountDeletionUser(ctx context.Context, accountID int64) error
	ListRemoteInboxes(ctx context.Context, arg db.ListRemoteInboxesParams) ([]string, error)
	ListLocalFollowsOfAccount(ctx context.Context, arg db.ListLocalFollowsOfAccountParams) ([]db.Follow, error)
	PurgeAccountProfile(ctx context.Context, id int64) error
	ListUnresolvedReportedStatusIDs(ctx context.Context, targetAccountID int64) ([]int64, error)
	ListAccountStatusesForDeletion(ctx context.Context, arg db.ListAccountStatusesForDeletionParams) ([]db.Status, error)
	DeleteAccountMentionsBatch(ctx context.Context, arg db.DeleteAccountMentionsBatchParams) (int64, error)
	ListAccountMediaAttachmentsForDeletion(ctx context.Context, arg db.ListAccountMediaAttachmentsForDeletionParams) ([]db.MediaAttachment, error)
	DeleteMediaAttachmentByID(ctx context.Context, id int64) error
	DeleteAccountPollsBatch(ctx context.Context, arg db.DeleteAccountPollsBatchParams) (int64, error)
	DeleteAccountNotificationsBatch(ctx context.Context, arg db.DeleteAccountNotificationsBatchParams) (int64, error)
	DeleteAccountNotificationRequestsBatch(ctx context.Context, arg db.DeleteAccountNotificationRequestsBatchParams) (int64, error)
	DeleteAccountFavouritesBatch(ctx context.Context, arg db.DeleteAccountFavouritesBatchParams) (int64, error)
	DeleteAccountBookmarksBatch(ctx context.Context, arg db.DeleteAccountBookmarksBatchParams) (int64, error)
	DeleteAccountFollowsBatch(ctx context.Context, arg db.DeleteAccountFollowsBatchParams) (int64, error)
	DeleteAccountFollowRequestsBatch(ctx context.Context, arg db.DeleteAccountFollowRequestsBatchParams) (int64, error)
	DeleteAccountBlocksBatch(ctx context.Context, arg db.DeleteAccountBlocksBatchParams) (int64, error)
	DeleteAccountMutesBatch(ctx context.Context, arg db.DeleteAccountMutesBatchParams) (int64, error)
	DeleteAccountConversationsBatch(ctx context.Context, arg db.DeleteAccountConversationsBatchParams) (int64, error)
	PurgeAccountAssociations(ctx context.Context, accountID int64) error
	DeleteUserByAccountID(ctx context.Context, accountID int64) error
	DeleteAccount(ctx context.Context, id int64) error
	DeleteAccountDeletionRequest(ctx context.Context, accountID int64) error
}

// AccountDeletionBatchStore is what deleting one batch of statuses uses,
// inside its transaction.
type AccountDeletionBatchStore interface {
	GetMentionsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Mention, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	DecrementRepliesOfDeletedStatuses(ctx context.Context, arg db.DecrementRepliesOfDeletedStatusesParams) error
	DeleteAccountStatusesByIDs(ctx context.Context, arg db.DeleteAccountStatusesByIDsParams) (int64, error)
	conversations.ConversationStore
}

// AccountDeletionTransactor runs fn in one database transaction, which it
// commits when fn returns nil and rolls back otherwise.
type AccountDeletionTransactor interface {
	InTx(ctx context.Context, fn func(AccountDeletionBatchStore) error) error
}

// TxBeginner begins database transactions. *pgxpool.Pool and *pgx.Conn
// satisfy it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// DBTransactor runs account deletion transactions on a database.
type DBTransactor struct {
	DB      TxBeginner
	Queries *db.Queries
}

// InTx runs fn in a transaction on the database.
func (t DBTransactor) InTx(ctx context.Context, fn func(AccountDeletionBatchStore) error) error {
	if t.DB == nil || t.Queries == nil {
		return ErrAccountDeletionMissingDeps
	}
	tx, err := t.DB.Begin(ctx)
	if err != nil {
		return err
	}
	// Rolling back a committed transaction does nothing.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(t.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AccountDeletionDeliverer federates a deletion. *federation.OutgoingService
// satisfies it.
type AccountDeletionDeliverer interface {
	SuspensionDeliverer
	DeliverAccountDeletion(ctx context.Context, account db.Account, inboxes []string) error
	DeliverFollowUndo(ctx context.Context, undo federation.FollowUndo) error
}

// AccountDeletionService deletes an account's data, as Mastodon's
// DeleteAccountService does, for an account whose deletion has begun: one
// that is suspended and has a deletion request, as suspending it by a
// moderator, or its owner's deleting it, leaves it.
type AccountDeletionService struct {
	store     AccountDeletionStore
	tx        AccountDeletionTransactor
	outgoing  AccountDeletionDeliverer
	storage   media.Storage
	publisher streaming.Publisher
	logger    *slog.Logger

	statusBatchSize int32
	rowBatchSize    int32
}

// NewAccountDeletionService constructs an account deletion service. The
// publisher may be nil.
func NewAccountDeletionService(store AccountDeletionStore, tx AccountDeletionTransactor, outgoing AccountDeletionDeliverer, storage media.Storage, publisher streaming.Publisher, logger *slog.Logger) *AccountDeletionService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AccountDeletionService{
		store:           store,
		tx:              tx,
		outgoing:        outgoing,
		storage:         storage,
		publisher:       publisher,
		logger:          logger,
		statusBatchSize: accountDeletionStatusBatchSize,
		rowBatchSize:    accountDeletionRowBatchSize,
	}
}

// Delete deletes the data of the account accountID with opts. It does
// nothing for an account that doesn't exist, isn't suspended or has no
// deletion request, so a deletion that has finished, or whose suspension
// was lifted, is left alone.
//
// A local account whose user hasn't confirmed its e-mail address or hasn't
// been approved is deleted whole, with no activities: nobody else has seen
// it (user_unconfirmed_or_pending?).
//
// It goes step by step:
//
//  1. A local account's user is disabled and signed out.
//  2. Unless opts.SkipActivityPub: a local account's actor Delete goes to
//     its followers and to every other known inbox. For a remote account,
//     each follow and pending follow request that it has of a local
//     account is rejected (Reject to its inbox) and each follow that a
//     local account has of it is undone (Undo to its inbox), and each is
//     deleted once its activity is queued.
//  3. With opts.ReserveUsername, the profile is emptied and its images
//     deleted.
//  4. The account's statuses are deleted, a batch per transaction, and with
//     them the reblogs of them. Then its mentions, media attachments and
//     their files, polls, notifications (its own and those it caused),
//     notification requests, favourites (taken out of the favourites
//     counts), bookmarks, follows and follow requests either way, blocks
//     and mutes either way, conversations, and what else it owns: notes,
//     endorsements, aliases, filters, domain blocks, featured tags, list
//     memberships, lists, migrations, report notes, scheduled statuses,
//     pins, followed tags, notification permissions and trends. With
//     opts.ReserveUsername, the statuses that unresolved reports name are
//     kept, with their mentions, media and polls, for the moderators.
//  5. Without opts.ReserveEmail, a local account's user is deleted.
//  6. Without opts.ReserveUsername the account's record is deleted, and by
//     foreign key everything left of it. With it, the deletion request is
//     deleted last: what is left is the record, suspended for good, which
//     keeps the username and answers 410 Gone, the moderation history about
//     it, and the reported statuses (fulfill_deletion_request!).
//
// Each delete is a short statement, and each batch of statuses a
// transaction, so nothing holds locks for long; a step that fails leaves
// earlier batches done and the rest untouched. Delete stops at the first
// failure and returns it, keeping the deletion request, so the deletion can
// be run again and goes on where it stopped: every step reads what is left
// and repeats nothing that is done, but for the actor Delete, which it
// sends again with the same ID. A follow is deleted only after its
// activity is queued, so no Reject or Undo is lost.
func (s *AccountDeletionService) Delete(ctx context.Context, accountID int64, opts AccountDeletionOptions) error {
	if s == nil || s.store == nil || s.tx == nil || s.outgoing == nil || s.storage == nil {
		return ErrAccountDeletionMissingDeps
	}

	account, err := s.store.GetAccountByIDAllowSuspended(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading account %d: %w", accountID, err)
	}
	if !account.SuspendedAt.Valid {
		return nil
	}
	pending, err := s.store.HasAccountDeletionRequest(ctx, account.ID)
	if err != nil {
		return fmt.Errorf("checking deletion request of account %d: %w", account.ID, err)
	}
	if !pending {
		return nil
	}

	local := !account.Domain.Valid || account.Domain.String == ""
	hasUser := false
	if local {
		user, err := s.store.GetUserByAccountID(ctx, account.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("loading user of account %d: %w", account.ID, err)
		default:
			hasUser = true
			if !user.ConfirmedAt.Valid || !user.Approved {
				opts = AccountDeletionOptions{SkipActivityPub: true}
			}
		}
	}

	if hasUser {
		if err := s.store.DisableAccountDeletionUser(ctx, account.ID); err != nil {
			return fmt.Errorf("disabling user of account %d: %w", account.ID, err)
		}
	}
	if !opts.SkipActivityPub {
		if err := s.distributeActivities(ctx, account, local); err != nil {
			return fmt.Errorf("federating deletion of account %d: %w", account.ID, err)
		}
	}
	if opts.ReserveUsername {
		if err := s.purgeProfile(ctx, account); err != nil {
			return fmt.Errorf("purging profile of account %d: %w", account.ID, err)
		}
	}
	if err := s.purgeContent(ctx, account, opts.ReserveUsername); err != nil {
		return fmt.Errorf("purging content of account %d: %w", account.ID, err)
	}

	if !opts.ReserveUsername {
		if err := s.store.DeleteAccount(ctx, account.ID); err != nil {
			return fmt.Errorf("deleting account %d: %w", account.ID, err)
		}
		return nil
	}
	if hasUser && !opts.ReserveEmail {
		if err := s.store.DeleteUserByAccountID(ctx, account.ID); err != nil {
			return fmt.Errorf("deleting user of account %d: %w", account.ID, err)
		}
	}
	if err := s.store.DeleteAccountDeletionRequest(ctx, account.ID); err != nil {
		return fmt.Errorf("fulfilling deletion request of account %d: %w", account.ID, err)
	}
	return nil
}

// distributeActivities federates the deletion of account
// (distribute_activities!).
func (s *AccountDeletionService) distributeActivities(ctx context.Context, account db.Account, local bool) error {
	if local {
		inboxes, err := s.remoteInboxes(ctx)
		if err != nil {
			return fmt.Errorf("listing inboxes: %w", err)
		}
		return s.outgoing.DeliverAccountDeletion(ctx, account, inboxes)
	}

	suspension := &AccountSuspensionService{store: s.store, outgoing: s.outgoing, batchSize: s.rowBatchSize}
	return errors.Join(
		suspension.rejectRemoteFollows(ctx, account),
		s.undoLocalFollows(ctx, account),
	)
}

// remoteInboxes lists every inbox of a remote account that isn't
// suspended.
func (s *AccountDeletionService) remoteInboxes(ctx context.Context) ([]string, error) {
	limit := s.rowLimit()
	var inboxes []string
	after := ""
	for {
		page, err := s.store.ListRemoteInboxes(ctx, db.ListRemoteInboxesParams{After: after, Limit: limit})
		if err != nil {
			return nil, err
		}
		inboxes = append(inboxes, page...)
		if len(page) < int(limit) {
			return inboxes, nil
		}
		after = page[len(page)-1]
	}
}

// undoLocalFollows undoes and deletes, batch by batch, each follow that a
// local account has of account, a remote account (undo_follows!). A follow
// it can't deliver the Undo for, because the account has no inbox, it
// deletes all the same. A failure of one follow doesn't stop the others; it
// returns all failures joined.
func (s *AccountDeletionService) undoLocalFollows(ctx context.Context, account db.Account) error {
	limit := s.rowLimit()
	var errs []error
	afterID := int64(0)
	for {
		follows, err := s.store.ListLocalFollowsOfAccount(ctx, db.ListLocalFollowsOfAccountParams{
			AccountID: account.ID,
			AfterID:   afterID,
			Limit:     limit,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("listing local follows of account %d: %w", account.ID, err))
			break
		}
		if len(follows) == 0 {
			break
		}

		ids := make([]int64, 0, len(follows))
		for _, follow := range follows {
			ids = append(ids, follow.AccountID)
		}
		followers, err := s.store.GetAccountsByIDs(ctx, ids)
		if err != nil {
			errs = append(errs, fmt.Errorf("loading local followers of account %d: %w", account.ID, err))
			break
		}
		byID := make(map[int64]db.Account, len(followers))
		for _, follower := range followers {
			byID[follower.ID] = follower
		}

		for _, follow := range follows {
			afterID = follow.ID
			follower, ok := byID[follow.AccountID]
			if !ok {
				continue
			}
			err := s.outgoing.DeliverFollowUndo(ctx, federation.FollowUndo{
				Follower: follower,
				Target:   account,
				ID:       follow.ID,
				URI:      follow.Uri,
			})
			if err != nil && !errors.Is(err, federation.ErrInboxMissing) {
				errs = append(errs, fmt.Errorf("undoing follow %d: %w", follow.ID, err))
				continue
			}
			if err := s.store.DeleteFollow(ctx, db.DeleteFollowParams{AccountID: follow.AccountID, TargetAccountID: account.ID}); err != nil {
				errs = append(errs, fmt.Errorf("deleting follow %d: %w", follow.ID, err))
			}
		}

		if len(follows) < int(limit) {
			break
		}
	}
	return errors.Join(errs...)
}

// purgeProfile deletes the account's avatar and header files and empties
// its profile (purge_profile!).
func (s *AccountDeletionService) purgeProfile(ctx context.Context, account db.Account) error {
	for _, image := range []string{account.AvatarFileName.String, account.HeaderFileName.String} {
		path := strings.TrimSpace(image)
		if path == "" {
			continue
		}
		if err := s.storage.Delete(ctx, path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("deleting profile image: %w", err)
		}
	}
	return s.store.PurgeAccountProfile(ctx, account.ID)
}

// purgeContent deletes what the account owns (purge_content!). With
// keepReported, the statuses that unresolved reports name are kept.
func (s *AccountDeletionService) purgeContent(ctx context.Context, account db.Account, keepReported bool) error {
	keep := []int64{}
	if keepReported {
		reported, err := s.store.ListUnresolvedReportedStatusIDs(ctx, account.ID)
		if err != nil {
			return fmt.Errorf("listing reported statuses: %w", err)
		}
		keep = append(keep, reported...)
	}

	if err := s.purgeStatuses(ctx, account, keep); err != nil {
		return fmt.Errorf("deleting statuses: %w", err)
	}
	if err := s.purgeMediaAttachments(ctx, account, keep); err != nil {
		return fmt.Errorf("deleting media attachments: %w", err)
	}

	id := account.ID
	batches := []struct {
		what   string
		delete func(limit int32) (int64, error)
	}{
		{"mentions", func(limit int32) (int64, error) {
			return s.store.DeleteAccountMentionsBatch(ctx, db.DeleteAccountMentionsBatchParams{AccountID: id, KeepStatusIds: keep, Limit: limit})
		}},
		{"polls", func(limit int32) (int64, error) {
			return s.store.DeleteAccountPollsBatch(ctx, db.DeleteAccountPollsBatchParams{AccountID: id, KeepStatusIds: keep, Limit: limit})
		}},
		{"notifications", func(limit int32) (int64, error) {
			return s.store.DeleteAccountNotificationsBatch(ctx, db.DeleteAccountNotificationsBatchParams{AccountID: id, Limit: limit})
		}},
		{"notification requests", func(limit int32) (int64, error) {
			return s.store.DeleteAccountNotificationRequestsBatch(ctx, db.DeleteAccountNotificationRequestsBatchParams{AccountID: id, Limit: limit})
		}},
		{"favourites", func(limit int32) (int64, error) {
			return s.store.DeleteAccountFavouritesBatch(ctx, db.DeleteAccountFavouritesBatchParams{AccountID: id, Limit: limit})
		}},
		{"bookmarks", func(limit int32) (int64, error) {
			return s.store.DeleteAccountBookmarksBatch(ctx, db.DeleteAccountBookmarksBatchParams{AccountID: id, Limit: limit})
		}},
		{"follows", func(limit int32) (int64, error) {
			return s.store.DeleteAccountFollowsBatch(ctx, db.DeleteAccountFollowsBatchParams{AccountID: id, Limit: limit})
		}},
		{"follow requests", func(limit int32) (int64, error) {
			return s.store.DeleteAccountFollowRequestsBatch(ctx, db.DeleteAccountFollowRequestsBatchParams{AccountID: id, Limit: limit})
		}},
		{"blocks", func(limit int32) (int64, error) {
			return s.store.DeleteAccountBlocksBatch(ctx, db.DeleteAccountBlocksBatchParams{AccountID: id, Limit: limit})
		}},
		{"mutes", func(limit int32) (int64, error) {
			return s.store.DeleteAccountMutesBatch(ctx, db.DeleteAccountMutesBatchParams{AccountID: id, Limit: limit})
		}},
		{"conversations", func(limit int32) (int64, error) {
			return s.store.DeleteAccountConversationsBatch(ctx, db.DeleteAccountConversationsBatchParams{AccountID: id, Limit: limit})
		}},
	}
	for _, batch := range batches {
		if err := s.deleteInBatches(ctx, batch.delete); err != nil {
			return fmt.Errorf("deleting %s: %w", batch.what, err)
		}
	}

	if err := s.store.PurgeAccountAssociations(ctx, id); err != nil {
		return fmt.Errorf("deleting other associations: %w", err)
	}
	return nil
}

// deleteInBatches calls del with the batch size until it deletes fewer rows.
func (s *AccountDeletionService) deleteInBatches(ctx context.Context, del func(limit int32) (int64, error)) error {
	limit := s.rowLimit()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := del(limit)
		if err != nil {
			return err
		}
		if deleted < int64(limit) {
			return nil
		}
	}
}

// purgeStatuses deletes the account's statuses but keep, a batch per
// transaction (purge_statuses!, BatchedRemoveStatusService).
func (s *AccountDeletionService) purgeStatuses(ctx context.Context, account db.Account, keep []int64) error {
	limit := s.statusBatchSize
	if limit <= 0 {
		limit = accountDeletionStatusBatchSize
	}
	afterID := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		statuses, err := s.store.ListAccountStatusesForDeletion(ctx, db.ListAccountStatusesForDeletionParams{
			AccountID: account.ID,
			AfterID:   afterID,
			KeepIds:   keep,
			Limit:     limit,
		})
		if err != nil {
			return err
		}
		if len(statuses) == 0 {
			return nil
		}
		if err := s.tx.InTx(ctx, func(store AccountDeletionBatchStore) error {
			return deleteStatusBatch(ctx, store, account, statuses)
		}); err != nil {
			return err
		}
		s.publishDeletes(ctx, account, statuses)

		afterID = statuses[len(statuses)-1].ID
		if len(statuses) < int(limit) {
			return nil
		}
	}
}

// deleteStatusBatch deletes statuses, all of account, in one transaction:
// it unlinks the direct ones from the conversations of their local
// recipients (unlink_from_conversations!), takes the live replies out of
// the reply counts of other accounts' statuses, and deletes them.
func deleteStatusBatch(ctx context.Context, store AccountDeletionBatchStore, account db.Account, statuses []db.Status) error {
	ids := make([]int64, 0, len(statuses))
	var direct []db.Status
	for _, status := range statuses {
		ids = append(ids, status.ID)
		if status.Visibility == statusVisibilityDirect && !status.DeletedAt.Valid {
			direct = append(direct, status)
		}
	}

	if len(direct) > 0 {
		directIDs := make([]int64, 0, len(direct))
		for _, status := range direct {
			directIDs = append(directIDs, status.ID)
		}
		mentions, err := store.GetMentionsByStatusIDs(ctx, directIDs)
		if err != nil {
			return fmt.Errorf("loading mentions: %w", err)
		}
		mentioned := make(map[int64][]int64, len(direct))
		accountIDs := make([]int64, 0, len(mentions))
		seen := make(map[int64]bool, len(mentions))
		for _, mention := range mentions {
			if mention.AccountID == 0 || mention.Silent {
				continue
			}
			mentioned[mention.StatusID] = append(mentioned[mention.StatusID], mention.AccountID)
			if !seen[mention.AccountID] {
				seen[mention.AccountID] = true
				accountIDs = append(accountIDs, mention.AccountID)
			}
		}
		byID := make(map[int64]db.Account, len(accountIDs))
		if len(accountIDs) > 0 {
			accounts, err := store.GetAccountsByIDs(ctx, accountIDs)
			if err != nil {
				return fmt.Errorf("loading mentioned accounts: %w", err)
			}
			for _, acc := range accounts {
				byID[acc.ID] = acc
			}
		}
		for _, status := range direct {
			var recipients []db.Account
			for _, id := range mentioned[status.ID] {
				if acc, ok := byID[id]; ok {
					recipients = append(recipients, acc)
				}
			}
			if err := conversations.RemoveStatusFromAccountConversations(ctx, store, status, account, recipients); err != nil {
				return fmt.Errorf("unlinking status %d from conversations: %w", status.ID, err)
			}
		}
	}

	if err := store.DecrementRepliesOfDeletedStatuses(ctx, db.DecrementRepliesOfDeletedStatusesParams{AccountID: account.ID, Ids: ids}); err != nil {
		return fmt.Errorf("updating reply counts: %w", err)
	}
	if _, err := store.DeleteAccountStatusesByIDs(ctx, db.DeleteAccountStatusesByIDsParams{AccountID: account.ID, Ids: ids}); err != nil {
		return fmt.Errorf("deleting statuses: %w", err)
	}
	return nil
}

// publishDeletes tells streaming clients of the public timelines that the
// public statuses among statuses are gone. Statuses discarded before were
// announced then. A failure is logged: the statuses are gone either way.
func (s *AccountDeletionService) publishDeletes(ctx context.Context, account db.Account, statuses []db.Status) {
	if s.publisher == nil {
		return
	}
	scoped := streaming.StreamPublicRemote
	if !account.Domain.Valid || account.Domain.String == "" {
		scoped = streaming.StreamPublicLocal
	}
	for _, status := range statuses {
		if status.DeletedAt.Valid || status.Visibility != statusVisibilityPublic {
			continue
		}
		streams := []string{streaming.StreamPublic, scoped}
		if err := s.publisher.PublishDeleteToRedis(ctx, strconv.FormatInt(status.ID, 10), streams); err != nil {
			s.logger.Error("Failed to publish deleted status", "account_id", account.ID, "status_id", status.ID, "error", err)
		}
	}
}

// purgeMediaAttachments deletes the account's media attachments but those
// of keep, files first, so a retry finds any attachment whose files it
// couldn't delete (purge_media_attachments!).
func (s *AccountDeletionService) purgeMediaAttachments(ctx context.Context, account db.Account, keep []int64) error {
	limit := s.rowLimit()
	afterID := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		attachments, err := s.store.ListAccountMediaAttachmentsForDeletion(ctx, db.ListAccountMediaAttachmentsForDeletionParams{
			AccountID:     account.ID,
			AfterID:       afterID,
			KeepStatusIds: keep,
			Limit:         limit,
		})
		if err != nil {
			return err
		}
		for _, attachment := range attachments {
			if err := deleteMediaAttachmentFiles(ctx, s.storage, attachment); err != nil {
				return fmt.Errorf("deleting files of media attachment %d: %w", attachment.ID, err)
			}
			if err := s.store.DeleteMediaAttachmentByID(ctx, attachment.ID); err != nil {
				return fmt.Errorf("deleting media attachment %d: %w", attachment.ID, err)
			}
		}
		if len(attachments) < int(limit) {
			return nil
		}
		afterID = attachments[len(attachments)-1].ID
	}
}

func (s *AccountDeletionService) rowLimit() int32 {
	if s.rowBatchSize <= 0 {
		return accountDeletionRowBatchSize
	}
	return s.rowBatchSize
}
