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

// LikeStore describes the persistence needed for incoming Like activities.
type LikeStore interface {
	CreateStore
	CreateFavourite(ctx context.Context, arg db.CreateFavouriteParams) (db.Favourite, error)
	GetFavourite(ctx context.Context, arg db.GetFavouriteParams) (db.Favourite, error)
	IncrementStatusFavourites(ctx context.Context, statusID int64) error
}

// LikeService processes incoming Like activities (favourites).
type LikeService struct {
	store       LikeStore
	localDomain string
	fetcher     activitypub.ActorFetcher
	helper      *CreateService
}

// NewLikeService creates a service for Like activities.
func NewLikeService(store LikeStore, localDomain string, fetcher activitypub.ActorFetcher) *LikeService {
	return &LikeService{
		store:       store,
		localDomain: localDomain,
		fetcher:     fetcher,
		helper:      NewCreateService(store, localDomain, fetcher),
	}
}

// HandleLike processes a Like activity and records a favourite.
func (s *LikeService) HandleLike(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid like activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing like actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if isLocalURI(actorURI, s.localDomain) {
		return nil
	}

	objectValue := unwrapAnnounceObject(activity.ActivityObject)
	objectURI := extractObjectURI(objectValue)
	if objectURI == "" {
		objectURI = noteURIFromObject(objectValue)
	}
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing like object"}
	}

	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: objectURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	account, err := s.ensureLikingAccount(ctx, actorURI)
	if err != nil {
		return err
	}
	if account.SuspendedAt.Valid {
		return &apihandler.InboxError{Status: httpStatusForbidden, Message: "Actor is suspended"}
	}

	_, err = s.store.GetFavourite(ctx, db.GetFavouriteParams{
		AccountID: account.ID,
		StatusID:  status.ID,
	})
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	if _, err := s.store.CreateFavourite(ctx, db.CreateFavouriteParams{
		AccountID: account.ID,
		StatusID:  status.ID,
	}); err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return err
	}

	if _, err := s.store.CreateStatusStats(ctx, status.ID); err != nil && !isUniqueViolation(err) {
		return err
	}
	if err := s.store.IncrementStatusFavourites(ctx, status.ID); err != nil {
		return err
	}

	return s.notifyFavourite(ctx, account, status)
}

func (s *LikeService) ensureLikingAccount(ctx context.Context, actorURI string) (db.Account, error) {
	if s.fetcher == nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusInternalError, Message: "Actor fetcher unavailable"}
	}
	actor, err := s.fetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	return s.helper.ensureActorAccount(ctx, actorURI, actor)
}

func (s *LikeService) notifyFavourite(ctx context.Context, liker db.Account, status db.Status) error {
	if !status.Local.Valid || !status.Local.Bool {
		return nil
	}
	if status.AccountID == liker.ID {
		return nil
	}

	suppressed, err := s.isNotificationSuppressed(ctx, status.AccountID, liker.ID)
	if err != nil || suppressed {
		return err
	}

	_, err = s.store.CreateNotifications(ctx, db.CreateNotificationsParams{
		Column1:       []int64{status.AccountID},
		FromAccountID: liker.ID,
		ActivityType:  "Status",
		ActivityID:    status.ID,
		Type:          pgtype.Text{String: "favourite", Valid: true},
	})
	return err
}

func (s *LikeService) isNotificationSuppressed(ctx context.Context, accountID, likerID int64) (bool, error) {
	mutes, err := s.store.GetMutesFromAccounts(ctx, db.GetMutesFromAccountsParams{
		TargetAccountID: likerID,
		AccountIds:      []int64{accountID},
	})
	if err != nil {
		return false, err
	}
	if len(mutes) > 0 {
		return true, nil
	}

	blocks, err := s.store.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
		TargetAccountID: likerID,
		AccountIds:      []int64{accountID},
	})
	if err != nil {
		return false, err
	}
	return len(blocks) > 0, nil
}
