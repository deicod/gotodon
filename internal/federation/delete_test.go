package federation

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type deleteStore struct {
	accountsByURI map[string]db.Account
	accountsByID  map[int64]db.Account
	statusesByURI map[string]db.Status
	statusesByID  map[int64]db.Status
	tombstones    map[string]db.Tombstone
}

func newDeleteStore() *deleteStore {
	return &deleteStore{
		accountsByURI: make(map[string]db.Account),
		accountsByID:  make(map[int64]db.Account),
		statusesByURI: make(map[string]db.Status),
		statusesByID:  make(map[int64]db.Status),
		tombstones:    make(map[string]db.Tombstone),
	}
}

func (s *deleteStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	account, ok := s.accountsByURI[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *deleteStore) GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error) {
	if !uri.Valid || uri.String == "" {
		return db.Status{}, pgx.ErrNoRows
	}
	status, ok := s.statusesByURI[uri.String]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	return status, nil
}

func (s *deleteStore) DeleteStatus(ctx context.Context, id int64) error {
	status, ok := s.statusesByID[id]
	if !ok {
		return pgx.ErrNoRows
	}
	status.DeletedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	status.UpdatedAt = status.DeletedAt
	s.statusesByID[id] = status
	if status.Uri.Valid {
		s.statusesByURI[status.Uri.String] = status
	}
	return nil
}

func (s *deleteStore) CreateTombstone(ctx context.Context, arg db.CreateTombstoneParams) (db.Tombstone, error) {
	tombstone := db.Tombstone{
		ID:          int64(len(s.tombstones) + 1),
		AccountID:   arg.AccountID,
		Uri:         arg.Uri,
		ByModerator: arg.ByModerator,
		CreatedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	s.tombstones[arg.Uri] = tombstone
	return tombstone, nil
}

func (s *deleteStore) ExistsTombstone(ctx context.Context, uri string) (bool, error) {
	_, ok := s.tombstones[uri]
	return ok, nil
}

func (s *deleteStore) SuspendAccount(ctx context.Context, arg db.SuspendAccountParams) error {
	account, ok := s.accountsByID[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	account.SuspendedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	account.SuspensionOrigin = arg.SuspensionOrigin
	s.accountsByID[account.ID] = account
	s.accountsByURI[account.Uri] = account
	return nil
}

func TestDeleteService_DeleteNote(t *testing.T) {
	store := newDeleteStore()
	actorURI := "https://remote.example/users/alice"
	actor := db.Account{
		ID:     1,
		Uri:    actorURI,
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accountsByURI[actorURI] = actor
	store.accountsByID[actor.ID] = actor

	noteURI := "https://remote.example/statuses/1"
	status := db.Status{
		ID:        10,
		Uri:       pgtype.Text{String: noteURI, Valid: true},
		AccountID: actor.ID,
		Local:     pgtype.Bool{Bool: false, Valid: true},
	}
	store.statusesByID[status.ID] = status
	store.statusesByURI[noteURI] = status

	service := NewDeleteService(store, "local.test")
	activity := &activitypub.Activity{
		Type:  activitypub.TypeDelete,
		Actor: actorURI,
		ActivityObject: map[string]any{
			"id":   noteURI,
			"type": activitypub.TypeNote,
		},
	}

	if err := service.HandleDelete(context.Background(), activity); err != nil {
		t.Fatalf("HandleDelete error: %v", err)
	}

	updated := store.statusesByID[status.ID]
	if !updated.DeletedAt.Valid {
		t.Fatalf("deleted_at not set")
	}
	if tombstone, ok := store.tombstones[noteURI]; !ok {
		t.Fatalf("tombstone not created")
	} else if tombstone.AccountID != actor.ID {
		t.Fatalf("tombstone account_id = %d, want %d", tombstone.AccountID, actor.ID)
	}
}

func TestDeleteService_DeleteNoteActorMismatch(t *testing.T) {
	store := newDeleteStore()
	actorURI := "https://remote.example/users/alice"
	actor := db.Account{
		ID:     1,
		Uri:    actorURI,
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	otherURI := "https://remote.example/users/bob"
	other := db.Account{
		ID:     2,
		Uri:    otherURI,
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accountsByURI[actorURI] = actor
	store.accountsByURI[otherURI] = other
	store.accountsByID[actor.ID] = actor
	store.accountsByID[other.ID] = other

	noteURI := "https://remote.example/statuses/1"
	status := db.Status{
		ID:        10,
		Uri:       pgtype.Text{String: noteURI, Valid: true},
		AccountID: actor.ID,
		Local:     pgtype.Bool{Bool: false, Valid: true},
	}
	store.statusesByID[status.ID] = status
	store.statusesByURI[noteURI] = status

	service := NewDeleteService(store, "local.test")
	activity := &activitypub.Activity{
		Type:  activitypub.TypeDelete,
		Actor: otherURI,
		ActivityObject: map[string]any{
			"id":   noteURI,
			"type": activitypub.TypeNote,
		},
	}

	err := service.HandleDelete(context.Background(), activity)
	if err == nil {
		t.Fatalf("expected error for actor mismatch")
	}
	inboxErr, ok := err.(*apihandler.InboxError)
	if !ok || inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("error = %v, want InboxError %d", err, httpStatusBadRequest)
	}
	if store.statusesByID[status.ID].DeletedAt.Valid {
		t.Fatalf("status deleted on mismatch")
	}
	if _, ok := store.tombstones[noteURI]; ok {
		t.Fatalf("tombstone created on mismatch")
	}
}

func TestDeleteService_DeletePerson(t *testing.T) {
	store := newDeleteStore()
	actorURI := "https://remote.example/users/alice"
	account := db.Account{
		ID:     1,
		Uri:    actorURI,
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accountsByURI[actorURI] = account
	store.accountsByID[account.ID] = account

	service := NewDeleteService(store, "local.test")
	activity := &activitypub.Activity{
		Type:  activitypub.TypeDelete,
		Actor: actorURI,
		ActivityObject: map[string]any{
			"id":   actorURI,
			"type": activitypub.TypePerson,
		},
	}

	if err := service.HandleDelete(context.Background(), activity); err != nil {
		t.Fatalf("HandleDelete error: %v", err)
	}

	updated := store.accountsByURI[actorURI]
	if !updated.SuspendedAt.Valid {
		t.Fatalf("account not suspended")
	}
}
