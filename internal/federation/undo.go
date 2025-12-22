package federation

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// UndoStore describes the persistence needed for Undo handling.
type UndoStore interface {
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error)
	GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error)
	DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) error
	GetFollowRequest(ctx context.Context, arg db.GetFollowRequestParams) (db.FollowRequest, error)
	DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error
	GetFavourite(ctx context.Context, arg db.GetFavouriteParams) (db.Favourite, error)
	DeleteFavourite(ctx context.Context, arg db.DeleteFavouriteParams) error
	GetReblogByAccountAndOriginal(ctx context.Context, arg db.GetReblogByAccountAndOriginalParams) (db.Status, error)
	DeleteStatus(ctx context.Context, id int64) error
	DecrementStatusReblogs(ctx context.Context, statusID int64) error
	DecrementStatusFavourites(ctx context.Context, statusID int64) error
	GetBlock(ctx context.Context, arg db.GetBlockParams) (db.Block, error)
	DeleteBlock(ctx context.Context, arg db.DeleteBlockParams) error
}

// UndoService processes incoming Undo activities.
type UndoService struct {
	store       UndoStore
	localDomain string
}

// NewUndoService creates a new undo service.
func NewUndoService(store UndoStore, localDomain string) *UndoService {
	return &UndoService{
		store:       store,
		localDomain: localDomain,
	}
}

// HandleUndo processes an Undo activity and removes its effects.
func (s *UndoService) HandleUndo(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid undo activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing undo actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}

	target, ok, err := parseUndoObject(activity.ActivityObject)
	if err != nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: err.Error()}
	}
	if !ok || target.activityType == "" {
		return nil
	}

	if target.actorURI != "" && target.actorURI != actorURI {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Undo actor mismatch"}
	}

	switch target.activityType {
	case activitypub.TypeFollow:
		return s.undoFollow(ctx, actorURI, target.objectURI)
	case activitypub.TypeLike:
		return s.undoLike(ctx, actorURI, target.objectURI)
	case activitypub.TypeAnnounce:
		return s.undoAnnounce(ctx, actorURI, target.objectURI, target.id)
	case activitypub.TypeBlock:
		return s.undoBlock(ctx, actorURI, target.objectURI)
	default:
		return nil
	}
}

func (s *UndoService) undoFollow(ctx context.Context, actorURI, objectURI string) error {
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing follow object"}
	}
	if !isLocalURI(objectURI, s.localDomain) {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Follow target is not local"}
	}

	follower, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Undo actor not found"}
		}
		return err
	}

	target, err := s.store.GetAccountByURI(ctx, objectURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Undo target not found"}
		}
		return err
	}
	if follower.ID == target.ID {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Cannot unfollow self"}
	}

	_ = s.store.DeleteFollow(ctx, db.DeleteFollowParams{
		AccountID:       follower.ID,
		TargetAccountID: target.ID,
	})
	_ = s.store.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
		AccountID:       follower.ID,
		TargetAccountID: target.ID,
	})

	return nil
}

func (s *UndoService) undoLike(ctx context.Context, actorURI, objectURI string) error {
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing like object"}
	}

	actor, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Undo actor not found"}
		}
		return err
	}

	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: objectURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	_, err = s.store.GetFavourite(ctx, db.GetFavouriteParams{
		AccountID: actor.ID,
		StatusID:  status.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	if err := s.store.DeleteFavourite(ctx, db.DeleteFavouriteParams{
		AccountID: actor.ID,
		StatusID:  status.ID,
	}); err != nil {
		return err
	}

	return s.store.DecrementStatusFavourites(ctx, status.ID)
}

func (s *UndoService) undoAnnounce(ctx context.Context, actorURI, objectURI, activityID string) error {
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing announce object"}
	}

	actor, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Undo actor not found"}
		}
		return err
	}

	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: objectURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	reblog, err := s.findReblog(ctx, actor.ID, status.ID, activityID)
	if err != nil {
		return err
	}
	if reblog.ID == 0 {
		return nil
	}

	if err := s.store.DeleteStatus(ctx, reblog.ID); err != nil {
		return err
	}
	return s.store.DecrementStatusReblogs(ctx, status.ID)
}

func (s *UndoService) findReblog(ctx context.Context, actorID, originalID int64, activityID string) (db.Status, error) {
	if activityID != "" {
		candidate, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: activityID, Valid: true})
		if err == nil {
			if candidate.AccountID == actorID && candidate.ReblogOfID.Valid && candidate.ReblogOfID.Int64 == originalID {
				return candidate, nil
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return db.Status{}, err
		}
	}

	reblog, err := s.store.GetReblogByAccountAndOriginal(ctx, db.GetReblogByAccountAndOriginalParams{
		AccountID:  actorID,
		ReblogOfID: pgtype.Int8{Int64: originalID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Status{}, nil
		}
		return db.Status{}, err
	}

	return reblog, nil
}

func (s *UndoService) undoBlock(ctx context.Context, actorURI, objectURI string) error {
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing block object"}
	}
	if !isLocalURI(objectURI, s.localDomain) {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Block target is not local"}
	}

	actor, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Undo actor not found"}
		}
		return err
	}

	target, err := s.store.GetAccountByURI(ctx, objectURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Undo target not found"}
		}
		return err
	}
	if actor.ID == target.ID {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Cannot unblock self"}
	}

	_, err = s.store.GetBlock(ctx, db.GetBlockParams{
		AccountID:       actor.ID,
		TargetAccountID: target.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	return s.store.DeleteBlock(ctx, db.DeleteBlockParams{
		AccountID:       actor.ID,
		TargetAccountID: target.ID,
	})
}

type undoObject struct {
	activityType string
	actorURI     string
	objectURI    string
	id           string
}

func parseUndoObject(value any) (undoObject, bool, error) {
	if value == nil {
		return undoObject{}, false, errors.New("missing undo object")
	}

	switch obj := value.(type) {
	case activitypub.Activity:
		if obj.Type == "" {
			return undoObject{}, false, errors.New("missing undo object type")
		}
		return undoObject{
			activityType: obj.Type,
			actorURI:     extractActorURI(obj.Actor),
			objectURI:    extractObjectURI(obj.ActivityObject),
			id:           obj.ID,
		}, true, nil
	case *activitypub.Activity:
		if obj == nil {
			return undoObject{}, false, errors.New("missing undo object")
		}
		if obj.Type == "" {
			return undoObject{}, false, errors.New("missing undo object type")
		}
		return undoObject{
			activityType: obj.Type,
			actorURI:     extractActorURI(obj.Actor),
			objectURI:    extractObjectURI(obj.ActivityObject),
			id:           obj.ID,
		}, true, nil
	case map[string]any:
		activityType := coerceActivityType(obj["type"])
		if activityType == "" {
			return undoObject{}, false, errors.New("missing undo object type")
		}
		id, _ := obj["id"].(string)
		return undoObject{
			activityType: activityType,
			actorURI:     extractActorURI(obj["actor"]),
			objectURI:    extractObjectURI(obj["object"]),
			id:           id,
		}, true, nil
	default:
		return undoObject{}, false, nil
	}
}

func coerceActivityType(value any) string {
	switch typ := value.(type) {
	case string:
		return typ
	case []any:
		var fallback string
		for _, item := range typ {
			s, ok := item.(string)
			if !ok || s == "" {
				continue
			}
			if fallback == "" {
				fallback = s
			}
			if !strings.EqualFold(s, "Activity") {
				return s
			}
		}
		return fallback
	case []string:
		var fallback string
		for _, s := range typ {
			if s == "" {
				continue
			}
			if fallback == "" {
				fallback = s
			}
			if !strings.EqualFold(s, "Activity") {
				return s
			}
		}
		return fallback
	default:
		return ""
	}
}
