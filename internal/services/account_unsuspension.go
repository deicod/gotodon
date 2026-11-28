package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/db"
)

var ErrAccountUnsuspensionMissingDeps = errors.New("account unsuspension missing dependencies")

// AccountUnsuspensionStore describes DB access for account unsuspension.
type AccountUnsuspensionStore interface {
	GetAccountByIDAllowSuspended(ctx context.Context, id int64) (db.Account, error)
}

// UnsuspensionDeliverer federates an unsuspension.
// *federation.OutgoingService satisfies it.
type UnsuspensionDeliverer interface {
	DeliverAccountUnsuspension(ctx context.Context, account db.Account) error
}

// AccountUnsuspensionService carries out the side effects of lifting an
// account's suspension, as Mastodon's UnsuspendAccountService does, once
// AdminUnsuspendAccount has cleared its suspended_at.
//
// What a suspension hides is hidden where it is read (see
// AccountSuspensionService), so clearing suspended_at already brings back
// the account's statuses to the home, list, public, hashtag and link
// timelines, status search and trending statuses, and the account to the
// directory, account search, follower lists and follow request lists, and
// its actor document is served again. Nothing that a suspension or a deletion
// removed comes back: the status trends, the follows and follow requests of
// a remote account rejected by its suspension, and whatever a deletion that
// was under way had already deleted, stay gone.
type AccountUnsuspensionService struct {
	store    AccountUnsuspensionStore
	outgoing UnsuspensionDeliverer
}

// NewAccountUnsuspensionService constructs an account unsuspension service.
func NewAccountUnsuspensionService(store AccountUnsuspensionStore, outgoing UnsuspensionDeliverer) *AccountUnsuspensionService {
	return &AccountUnsuspensionService{store: store, outgoing: outgoing}
}

// Unsuspend carries out the unsuspension of the account accountID. For a
// local account, it delivers an Update of its actor, profile included, to
// its remote followers (distribute_update_actor!), who were sent the
// suspended actor. A remote account has nothing to federate: its own
// server federates it.
//
// It does nothing for an account that doesn't exist or is suspended (again),
// as UnsuspendAccountService returns for one. It can be repeated: a retry
// sends the same activity, with the same ID, again.
func (s *AccountUnsuspensionService) Unsuspend(ctx context.Context, accountID int64) error {
	if s == nil || s.store == nil || s.outgoing == nil {
		return ErrAccountUnsuspensionMissingDeps
	}

	account, err := s.store.GetAccountByIDAllowSuspended(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading account %d: %w", accountID, err)
	}
	if account.SuspendedAt.Valid {
		return nil
	}
	if account.Domain.Valid && account.Domain.String != "" {
		return nil
	}

	if err := s.outgoing.DeliverAccountUnsuspension(ctx, account); err != nil {
		return fmt.Errorf("delivering unsuspension of account %d: %w", account.ID, err)
	}
	return nil
}
