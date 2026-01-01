package federation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/conversations"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// DeleteStore describes the persistence needed for incoming Delete activities.
type DeleteStore interface {
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error)
	DeleteStatus(ctx context.Context, id int64) error
	CreateTombstone(ctx context.Context, arg db.CreateTombstoneParams) (db.Tombstone, error)
	ExistsTombstone(ctx context.Context, uri string) (bool, error)
	SuspendAccount(ctx context.Context, arg db.SuspendAccountParams) error
	GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	conversations.ConversationStore
}

// DeleteService processes incoming Delete activities.
type DeleteService struct {
	store       DeleteStore
	localDomain string
}

// NewDeleteService creates a Delete service for incoming deletions.
func NewDeleteService(store DeleteStore, localDomain string) *DeleteService {
	return &DeleteService{
		store:       store,
		localDomain: localDomain,
	}
}

// HandleDelete processes Delete activities for notes and person actors.
func (s *DeleteService) HandleDelete(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid delete activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing delete actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if isLocalURI(actorURI, s.localDomain) {
		return nil
	}

	objectURI := extractDeleteObjectURI(activity.ActivityObject)
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing delete object"}
	}

	objectType := extractObjectType(activity.ActivityObject)
	if isActorType(objectType) || objectURI == actorURI {
		return s.handleDeletePerson(ctx, actorURI, objectURI)
	}

	return s.handleDeleteNote(ctx, actorURI, objectURI)
}

func (s *DeleteService) handleDeleteNote(ctx context.Context, actorURI, objectURI string) error {
	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: objectURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if status.Local.Valid && status.Local.Bool {
		return nil
	}

	account, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Delete actor not found"}
		}
		return err
	}
	if account.ID != status.AccountID {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Delete actor mismatch"}
	}

	if err := s.store.DeleteStatus(ctx, status.ID); err != nil {
		return err
	}

	if status.Visibility == httpVisibilityDirect {
		activeMentions, err := s.loadActiveMentionAccounts(ctx, status.ID)
		if err != nil {
			return err
		}
		if err := conversations.RemoveStatusFromAccountConversations(ctx, s.store, status, account, activeMentions); err != nil {
			return err
		}
	}

	exists, err := s.store.ExistsTombstone(ctx, objectURI)
	if err != nil {
		return err
	}
	if !exists {
		_, err = s.store.CreateTombstone(ctx, db.CreateTombstoneParams{
			AccountID:   status.AccountID,
			Uri:         objectURI,
			ByModerator: pgtype.Bool{Bool: false, Valid: true},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *DeleteService) handleDeletePerson(ctx context.Context, actorURI, objectURI string) error {
	if objectURI != "" && objectURI != actorURI {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Delete actor mismatch"}
	}

	account, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !account.Domain.Valid {
		return nil
	}
	if account.SuspendedAt.Valid {
		return nil
	}

	return s.store.SuspendAccount(ctx, db.SuspendAccountParams{
		ID:               account.ID,
		SuspensionOrigin: pgtype.Int4{Valid: false},
	})
}

func (s *DeleteService) loadActiveMentionAccounts(ctx context.Context, statusID int64) ([]db.Account, error) {
	mentions, err := s.store.GetMentionsByStatusID(ctx, statusID)
	if err != nil {
		return nil, err
	}
	if len(mentions) == 0 {
		return nil, nil
	}

	seen := make(map[int64]struct{})
	ids := make([]int64, 0, len(mentions))
	for _, mention := range mentions {
		if mention.AccountID == 0 || mention.Silent {
			continue
		}
		if _, ok := seen[mention.AccountID]; ok {
			continue
		}
		seen[mention.AccountID] = struct{}{}
		ids = append(ids, mention.AccountID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	return s.store.GetAccountsByIDs(ctx, ids)
}

func extractDeleteObjectURI(value any) string {
	switch obj := value.(type) {
	case activitypub.Object:
		return obj.ID
	case *activitypub.Object:
		if obj != nil {
			return obj.ID
		}
	}
	return extractObjectURI(value)
}
