package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
)

// accountSuspensionBatchSize is how many follows or follow requests a
// suspension reads at a time, as find_in_batches does in Mastodon.
const accountSuspensionBatchSize = 1000

var ErrAccountSuspensionMissingDeps = errors.New("account suspension missing dependencies")

// AccountSuspensionStore describes DB access for account suspension.
type AccountSuspensionStore interface {
	GetAccountByIDForShare(ctx context.Context, id int64) (db.Account, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	ListFollowsByAccountToLocal(ctx context.Context, arg db.ListFollowsByAccountToLocalParams) ([]db.Follow, error)
	ListFollowRequestsByAccountToLocal(ctx context.Context, arg db.ListFollowRequestsByAccountToLocalParams) ([]db.FollowRequest, error)
	DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) error
	DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error
	DeleteStatusTrendsByAccount(ctx context.Context, accountID int64) error
}

// SuspensionDeliverer federates a suspension. *federation.OutgoingService
// satisfies it.
type SuspensionDeliverer interface {
	DeliverAccountSuspension(ctx context.Context, account db.Account) error
	DeliverFollowReject(ctx context.Context, rejection federation.FollowRejection) error
}

// AccountSuspensionService carries out the side effects of an account's
// suspension, as Mastodon's SuspendAccountService does, once the account's
// suspended_at is set.
//
// What a suspension hides is hidden where it is read: the home, list,
// public, hashtag and link timelines, status search and trending statuses
// leave out statuses of suspended accounts, as the directory, account
// search, follower lists and follow request lists leave out the accounts.
// Unsuspension therefore restores them without undoing anything here.
type AccountSuspensionService struct {
	store     AccountSuspensionStore
	outgoing  SuspensionDeliverer
	batchSize int32
}

// NewAccountSuspensionService constructs an account suspension service.
func NewAccountSuspensionService(store AccountSuspensionStore, outgoing SuspensionDeliverer) *AccountSuspensionService {
	return &AccountSuspensionService{
		store:     store,
		outgoing:  outgoing,
		batchSize: accountSuspensionBatchSize,
	}
}

// Suspend carries out the suspension of the account accountID. It does
// nothing for an account that doesn't exist or isn't suspended (any more).
// For every suspended account it removes the account's statuses from the
// status trends (remove_from_trends!). Then:
//
//   - for a local account, it delivers an Update of its actor, marked
//     suspended, to its remote followers (distribute_update_actor!);
//   - for a remote account, it rejects each follow and each pending follow
//     request that the account has of a local account: it delivers a Reject
//     to the account's inbox, signed by the local account, and deletes the
//     follow or request (reject_remote_follows!), so the account's server
//     stops sending it what its followers see. A follow it can't deliver the
//     Reject for, because the account has no inbox, it deletes all the
//     same.
//
// Pending follow requests that others have made of a suspended local
// account are kept, hidden from its request list, for unsuspension.
//
// Every step can be repeated, so a failed suspension can be run again: a
// follow is deleted only after its Reject is enqueued, and a retry sends the
// same activities, with the same IDs, again. A failure of one follow doesn't
// stop the others; Suspend returns all failures joined.
//
// The account is read once the transaction that changes it has ended, so a
// suspension can queue this before it commits: Suspend then acts on what it
// committed, and does nothing if it rolled back.
func (s *AccountSuspensionService) Suspend(ctx context.Context, accountID int64) error {
	if s == nil || s.store == nil || s.outgoing == nil {
		return ErrAccountSuspensionMissingDeps
	}

	account, err := s.store.GetAccountByIDForShare(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading account %d: %w", accountID, err)
	}
	if !account.SuspendedAt.Valid {
		return nil
	}

	if err := s.store.DeleteStatusTrendsByAccount(ctx, account.ID); err != nil {
		return fmt.Errorf("removing account %d from trends: %w", account.ID, err)
	}

	if !account.Domain.Valid || account.Domain.String == "" {
		if err := s.outgoing.DeliverAccountSuspension(ctx, account); err != nil {
			return fmt.Errorf("delivering suspension of account %d: %w", account.ID, err)
		}
		return nil
	}

	return s.rejectRemoteFollows(ctx, account)
}

// rejectRemoteFollows rejects and deletes each follow and each pending follow
// request that account, a remote account, has of a local account
// (reject_remote_follows!). A follow it can't deliver the Reject for,
// because the account has no inbox, it deletes all the same. A failure of
// one follow doesn't stop the others; it returns all failures joined.
func (s *AccountSuspensionService) rejectRemoteFollows(ctx context.Context, account db.Account) error {
	return errors.Join(
		s.rejectFollows(ctx, account, false, s.listFollows, s.deleteFollow),
		s.rejectFollows(ctx, account, true, s.listFollowRequests, s.deleteFollowRequest),
	)
}

// followRow is a follow or a follow request.
type followRow struct {
	id       int64
	targetID int64
	uri      pgtype.Text
}

// rejectFollows rejects and deletes, batch by batch, the follows (or, with
// request, the follow requests) that list returns for account, a remote
// account, deleting each with remove.
func (s *AccountSuspensionService) rejectFollows(
	ctx context.Context,
	account db.Account,
	request bool,
	list func(ctx context.Context, accountID, afterID int64, limit int32) ([]followRow, error),
	remove func(ctx context.Context, accountID, targetID int64) error,
) error {
	kind := "follow"
	if request {
		kind = "follow request"
	}
	limit := s.batchSize
	if limit <= 0 {
		limit = accountSuspensionBatchSize
	}

	var errs []error
	afterID := int64(0)
	for {
		rows, err := list(ctx, account.ID, afterID, limit)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing %ss of account %d: %w", kind, account.ID, err))
			break
		}
		if len(rows) == 0 {
			break
		}

		targets, err := s.accountsByID(ctx, rows)
		if err != nil {
			errs = append(errs, fmt.Errorf("loading %s targets of account %d: %w", kind, account.ID, err))
			break
		}
		for _, row := range rows {
			afterID = row.id
			target, ok := targets[row.targetID]
			if !ok {
				continue
			}
			err := s.outgoing.DeliverFollowReject(ctx, federation.FollowRejection{
				Follower: account,
				Target:   target,
				ID:       row.id,
				URI:      row.uri,
				Request:  request,
			})
			if err != nil && !errors.Is(err, federation.ErrInboxMissing) {
				errs = append(errs, fmt.Errorf("rejecting %s %d: %w", kind, row.id, err))
				continue
			}
			if err := remove(ctx, account.ID, row.targetID); err != nil {
				errs = append(errs, fmt.Errorf("deleting %s %d: %w", kind, row.id, err))
			}
		}

		if len(rows) < int(limit) {
			break
		}
	}
	return errors.Join(errs...)
}

// accountsByID loads the targets of rows by ID.
func (s *AccountSuspensionService) accountsByID(ctx context.Context, rows []followRow) (map[int64]db.Account, error) {
	ids := make([]int64, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if !seen[row.targetID] {
			seen[row.targetID] = true
			ids = append(ids, row.targetID)
		}
	}
	accounts, err := s.store.GetAccountsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]db.Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	return byID, nil
}

func (s *AccountSuspensionService) listFollows(ctx context.Context, accountID, afterID int64, limit int32) ([]followRow, error) {
	follows, err := s.store.ListFollowsByAccountToLocal(ctx, db.ListFollowsByAccountToLocalParams{
		AccountID: accountID,
		AfterID:   afterID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]followRow, 0, len(follows))
	for _, follow := range follows {
		rows = append(rows, followRow{id: follow.ID, targetID: follow.TargetAccountID, uri: follow.Uri})
	}
	return rows, nil
}

func (s *AccountSuspensionService) listFollowRequests(ctx context.Context, accountID, afterID int64, limit int32) ([]followRow, error) {
	requests, err := s.store.ListFollowRequestsByAccountToLocal(ctx, db.ListFollowRequestsByAccountToLocalParams{
		AccountID: accountID,
		AfterID:   afterID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]followRow, 0, len(requests))
	for _, request := range requests {
		rows = append(rows, followRow{id: request.ID, targetID: request.TargetAccountID, uri: request.Uri})
	}
	return rows, nil
}

func (s *AccountSuspensionService) deleteFollow(ctx context.Context, accountID, targetID int64) error {
	return s.store.DeleteFollow(ctx, db.DeleteFollowParams{AccountID: accountID, TargetAccountID: targetID})
}

func (s *AccountSuspensionService) deleteFollowRequest(ctx context.Context, accountID, targetID int64) error {
	return s.store.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{AccountID: accountID, TargetAccountID: targetID})
}
