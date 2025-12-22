package federation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// BlockStore describes the persistence needed for incoming Block activities.
type BlockStore interface {
	CreateStore
	CreateBlock(ctx context.Context, arg db.CreateBlockParams) (db.Block, error)
	DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) error
	DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error
}

// BlockService processes incoming Block activities.
type BlockService struct {
	store       BlockStore
	localDomain string
	fetcher     activitypub.ActorFetcher
	helper      *CreateService
}

// NewBlockService creates a service for Block activities.
func NewBlockService(store BlockStore, localDomain string, fetcher activitypub.ActorFetcher) *BlockService {
	return &BlockService{
		store:       store,
		localDomain: localDomain,
		fetcher:     fetcher,
		helper:      NewCreateService(store, localDomain, fetcher),
	}
}

// HandleBlock processes an incoming Block activity.
func (s *BlockService) HandleBlock(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid block activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing block actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if isLocalURI(actorURI, s.localDomain) {
		return nil
	}

	objectURI := extractObjectURI(activity.ActivityObject)
	if objectURI == "" {
		objectURI = extractActorURI(activity.ActivityObject)
	}
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing block object"}
	}
	if !isLocalURI(objectURI, s.localDomain) {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Block target is not local"}
	}

	actorAccount, err := s.ensureBlockingAccount(ctx, actorURI)
	if err != nil {
		return err
	}
	if actorAccount.SuspendedAt.Valid {
		return &apihandler.InboxError{Status: httpStatusForbidden, Message: "Actor is suspended"}
	}

	targetAccount, err := s.store.GetAccountByURI(ctx, objectURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Block target not found"}
		}
		return err
	}
	if targetAccount.Domain.Valid {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Block target is not local"}
	}
	if actorAccount.ID == targetAccount.ID {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Cannot block self"}
	}

	uri := pgtype.Text{Valid: false}
	if activity.ID != "" {
		uri = pgtype.Text{String: activity.ID, Valid: true}
	}

	if _, err := s.store.CreateBlock(ctx, db.CreateBlockParams{
		AccountID:       actorAccount.ID,
		TargetAccountID: targetAccount.ID,
		Uri:             uri,
	}); err != nil && !isUniqueViolation(err) {
		return err
	}

	_ = s.store.DeleteFollow(ctx, db.DeleteFollowParams{
		AccountID:       actorAccount.ID,
		TargetAccountID: targetAccount.ID,
	})
	_ = s.store.DeleteFollow(ctx, db.DeleteFollowParams{
		AccountID:       targetAccount.ID,
		TargetAccountID: actorAccount.ID,
	})
	_ = s.store.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
		AccountID:       actorAccount.ID,
		TargetAccountID: targetAccount.ID,
	})
	_ = s.store.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
		AccountID:       targetAccount.ID,
		TargetAccountID: actorAccount.ID,
	})

	return nil
}

func (s *BlockService) ensureBlockingAccount(ctx context.Context, actorURI string) (db.Account, error) {
	if s.fetcher == nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusInternalError, Message: "Actor fetcher unavailable"}
	}
	actor, err := s.fetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	return s.helper.ensureActorAccount(ctx, actorURI, actor)
}
