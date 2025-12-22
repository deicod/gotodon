package federation

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// FollowResponseStore describes the persistence needed for follow responses.
type FollowResponseStore interface {
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	GetFollowRequest(ctx context.Context, arg db.GetFollowRequestParams) (db.FollowRequest, error)
	CreateFollow(ctx context.Context, arg db.CreateFollowParams) (db.Follow, error)
	DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error
}

// FollowResponseService processes incoming Accept/Reject activities for follows.
type FollowResponseService struct {
	store       FollowResponseStore
	localDomain string
}

// NewFollowResponseService creates a follow response service.
func NewFollowResponseService(store FollowResponseStore, localDomain string) *FollowResponseService {
	return &FollowResponseService{
		store:       store,
		localDomain: localDomain,
	}
}

// HandleAccept processes an incoming Accept activity for a Follow.
func (s *FollowResponseService) HandleAccept(ctx context.Context, activity *activitypub.Activity) error {
	return s.handleResponse(ctx, activity, true)
}

// HandleReject processes an incoming Reject activity for a Follow.
func (s *FollowResponseService) HandleReject(ctx context.Context, activity *activitypub.Activity) error {
	return s.handleResponse(ctx, activity, false)
}

type followReference struct {
	actorURI  string
	objectURI string
}

func (s *FollowResponseService) handleResponse(ctx context.Context, activity *activitypub.Activity, accepted bool) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid follow response"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing response actor"}
	}

	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}

	followRef, err := parseFollowReference(activity.ActivityObject)
	if err != nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: err.Error()}
	}
	if followRef.actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing follow actor"}
	}
	if followRef.objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing follow object"}
	}

	if !isLocalURI(followRef.actorURI, s.localDomain) {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Follow actor is not local"}
	}
	if actorURI != followRef.objectURI {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Follow target does not match actor"}
	}

	followerAccount, err := s.store.GetAccountByURI(ctx, followRef.actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Follow actor not found"}
		}
		return err
	}

	targetAccount, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Follow target not found"}
		}
		return err
	}
	if followerAccount.ID == targetAccount.ID {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Cannot follow self"}
	}

	_, err = s.store.GetFollowRequest(ctx, db.GetFollowRequestParams{
		AccountID:       followerAccount.ID,
		TargetAccountID: targetAccount.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	if accepted {
		if err := s.ensureFollow(ctx, followerAccount.ID, targetAccount.ID); err != nil {
			return err
		}
	}

	_ = s.store.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
		AccountID:       followerAccount.ID,
		TargetAccountID: targetAccount.ID,
	})

	return nil
}

func (s *FollowResponseService) ensureFollow(ctx context.Context, followerID, targetID int64) error {
	_, err := s.store.CreateFollow(ctx, db.CreateFollowParams{
		AccountID:       followerID,
		TargetAccountID: targetID,
		ShowReblogs:     true,
		Notify:          false,
	})
	if err != nil && !isUniqueViolation(err) {
		return err
	}
	return nil
}

func parseFollowReference(value any) (followReference, error) {
	if value == nil {
		return followReference{}, errors.New("invalid follow object")
	}

	switch follow := value.(type) {
	case activitypub.Activity:
		return followReference{
			actorURI:  extractActorURI(follow.Actor),
			objectURI: extractObjectURI(follow.ActivityObject),
		}, nil
	case *activitypub.Activity:
		if follow == nil {
			return followReference{}, errors.New("invalid follow object")
		}
		return followReference{
			actorURI:  extractActorURI(follow.Actor),
			objectURI: extractObjectURI(follow.ActivityObject),
		}, nil
	case map[string]any:
		if typ, ok := follow["type"]; ok && !typeMatches(typ, activitypub.TypeFollow) {
			return followReference{}, errors.New("invalid follow object")
		}
		return followReference{
			actorURI:  extractActorURI(follow["actor"]),
			objectURI: extractObjectURI(follow["object"]),
		}, nil
	default:
		return followReference{}, errors.New("invalid follow object")
	}
}

func typeMatches(value any, expected string) bool {
	switch typ := value.(type) {
	case string:
		return strings.EqualFold(typ, expected)
	case []any:
		for _, item := range typ {
			if s, ok := item.(string); ok && strings.EqualFold(s, expected) {
				return true
			}
		}
	case []string:
		for _, s := range typ {
			if strings.EqualFold(s, expected) {
				return true
			}
		}
	}
	return false
}
