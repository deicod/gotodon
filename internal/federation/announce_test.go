package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type announceStore struct {
	*createStore
}

func newAnnounceStore() *announceStore {
	return &announceStore{createStore: newCreateStore()}
}

func (s *announceStore) GetReblogByAccountAndOriginal(ctx context.Context, arg db.GetReblogByAccountAndOriginalParams) (db.Status, error) {
	for _, status := range s.statusesByID {
		if status.AccountID != arg.AccountID {
			continue
		}
		if !status.ReblogOfID.Valid || !arg.ReblogOfID.Valid {
			continue
		}
		if status.ReblogOfID.Int64 == arg.ReblogOfID.Int64 {
			return status, nil
		}
	}
	return db.Status{}, pgx.ErrNoRows
}

func (s *announceStore) IncrementStatusReblogs(ctx context.Context, statusID int64) error {
	stat, ok := s.statusStats[statusID]
	if !ok {
		stat = db.StatusStat{ID: statusID, StatusID: statusID}
	}
	stat.ReblogsCount++
	s.statusStats[statusID] = stat
	return nil
}

type mapActorFetcher struct {
	actors map[string]*activitypub.Actor
	err    error
}

func (f mapActorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	if f.err != nil {
		return nil, f.err
	}
	actor, ok := f.actors[actorURI]
	if !ok {
		return nil, errors.New("actor not found")
	}
	return actor, nil
}

type stubObjectFetcher struct {
	object any
	err    error
	calls  int
}

func (f *stubObjectFetcher) FetchObject(ctx context.Context, objectURI string) (any, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.object, nil
}

func TestAnnounceService_CreatesReblogAndNotification(t *testing.T) {
	ctx := context.Background()
	store := newAnnounceStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	original, err := store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        pgtype.Text{String: "https://local.test/statuses/1", Valid: true},
		AccountID:  local.ID,
		Visibility: httpVisibilityPublic,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateStatus error: %v", err)
	}
	if _, err := store.CreateStatusStats(ctx, original.ID); err != nil {
		t.Fatalf("CreateStatusStats error: %v", err)
	}

	announcerURI := "https://remote.example/users/alice"
	announcerActor := makeActor(announcerURI, "alice")
	service := NewAnnounceService(store, "local.test", mapActorFetcher{
		actors: map[string]*activitypub.Actor{
			announcerURI: announcerActor,
		},
	}, nil)

	activity := &activitypub.Activity{
		Type:           activitypub.TypeAnnounce,
		Actor:          announcerURI,
		ActivityObject: original.Uri.String,
		ID:             "https://remote.example/activities/boost1",
	}

	if err := service.HandleAnnounce(ctx, activity); err != nil {
		t.Fatalf("HandleAnnounce error: %v", err)
	}

	announcer, ok := store.accountsByURI[announcerURI]
	if !ok {
		t.Fatalf("announcer account not created")
	}

	reblog, ok := findReblog(store, announcer.ID, original.ID)
	if !ok {
		t.Fatalf("reblog status not created")
	}
	if !reblog.Uri.Valid || reblog.Uri.String != activity.ID {
		t.Fatalf("reblog uri = %#v, want %q", reblog.Uri, activity.ID)
	}
	if reblog.Local.Valid && reblog.Local.Bool {
		t.Fatalf("reblog local = true, want false")
	}

	stat := store.statusStats[original.ID]
	if stat.ReblogsCount != 1 {
		t.Fatalf("reblogs_count = %d, want 1", stat.ReblogsCount)
	}

	if len(store.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(store.notifications))
	}
	notif := store.notifications[0]
	if notif.AccountID != local.ID || !notif.Type.Valid || notif.Type.String != "reblog" {
		t.Fatalf("notification = %+v, want reblog for local account", notif)
	}
	if notif.ActivityID != original.ID {
		t.Fatalf("notification activity_id = %d, want %d", notif.ActivityID, original.ID)
	}
}

func TestAnnounceService_DedupesExistingReblog(t *testing.T) {
	ctx := context.Background()
	store := newAnnounceStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	announcer := db.Account{
		ID:       2,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.example", Valid: true},
		Uri:      "https://remote.example/users/alice",
	}
	store.accountsByURI[announcer.Uri] = announcer
	store.accountsByID[announcer.ID] = announcer

	original, _ := store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        pgtype.Text{String: "https://local.test/statuses/1", Valid: true},
		AccountID:  local.ID,
		Visibility: httpVisibilityPublic,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})
	store.statusStats[original.ID] = db.StatusStat{ID: original.ID, StatusID: original.ID, ReblogsCount: 1}

	_, _ = store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        pgtype.Text{String: "https://remote.example/activities/boost1", Valid: true},
		AccountID:  announcer.ID,
		ReblogOfID: pgtype.Int8{Int64: original.ID, Valid: true},
		Visibility: original.Visibility,
		Local:      pgtype.Bool{Bool: false, Valid: true},
	})

	beforeCount := len(store.statusesByID)

	service := NewAnnounceService(store, "local.test", mapActorFetcher{
		actors: map[string]*activitypub.Actor{
			announcer.Uri: makeActor(announcer.Uri, "alice"),
		},
	}, nil)

	activity := &activitypub.Activity{
		Type:           activitypub.TypeAnnounce,
		Actor:          announcer.Uri,
		ActivityObject: original.Uri.String,
		ID:             "https://remote.example/activities/boost1",
	}

	if err := service.HandleAnnounce(ctx, activity); err != nil {
		t.Fatalf("HandleAnnounce error: %v", err)
	}

	if len(store.statusesByID) != beforeCount {
		t.Fatalf("status count = %d, want %d", len(store.statusesByID), beforeCount)
	}
	if store.statusStats[original.ID].ReblogsCount != 1 {
		t.Fatalf("reblogs_count = %d, want 1", store.statusStats[original.ID].ReblogsCount)
	}
	if len(store.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0", len(store.notifications))
	}
}

func TestAnnounceService_UndoneAnnounceIsNotRestored(t *testing.T) {
	ctx := context.Background()
	store := newAnnounceStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	original, err := store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        pgtype.Text{String: "https://local.test/statuses/1", Valid: true},
		AccountID:  local.ID,
		Visibility: httpVisibilityPublic,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateStatus error: %v", err)
	}
	store.statusStats[original.ID] = db.StatusStat{ID: original.ID, StatusID: original.ID}

	// The boost was received and then undone, which deletes the reblog but
	// keeps its URI: a replayed or redelivered Announce must not restore it.
	activityID := "https://remote.example/activities/boost1"
	store.deletedURIs = map[string]bool{activityID: true}
	beforeCount := len(store.statusesByID)

	announcerURI := "https://remote.example/users/alice"
	service := NewAnnounceService(store, "local.test", mapActorFetcher{
		actors: map[string]*activitypub.Actor{
			announcerURI: makeActor(announcerURI, "alice"),
		},
	}, nil)

	activity := &activitypub.Activity{
		Type:           activitypub.TypeAnnounce,
		Actor:          announcerURI,
		ActivityObject: original.Uri.String,
		ID:             activityID,
	}

	if err := service.HandleAnnounce(ctx, activity); err != nil {
		t.Fatalf("HandleAnnounce error: %v", err)
	}
	if len(store.statusesByID) != beforeCount {
		t.Fatalf("status count = %d, want %d", len(store.statusesByID), beforeCount)
	}
	if store.statusStats[original.ID].ReblogsCount != 0 {
		t.Fatalf("reblogs_count = %d, want 0", store.statusStats[original.ID].ReblogsCount)
	}
	if len(store.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0", len(store.notifications))
	}
}

func TestAnnounceService_FetchesMissingNote(t *testing.T) {
	ctx := context.Background()
	store := newAnnounceStore()

	noteURI := "https://remote.example/statuses/99"
	authorURI := "https://remote.example/users/bob"
	announcerURI := "https://remote.example/users/alice"

	noteObject := map[string]any{
		"id":           noteURI,
		"type":         activitypub.TypeNote,
		"content":      "hello",
		"attributedTo": authorURI,
		"to":           []string{activitypub.PublicAudience},
	}

	objectFetcher := &stubObjectFetcher{object: noteObject}
	fetcher := mapActorFetcher{
		actors: map[string]*activitypub.Actor{
			authorURI:    makeActor(authorURI, "bob"),
			announcerURI: makeActor(announcerURI, "alice"),
		},
	}

	service := NewAnnounceService(store, "local.test", fetcher, objectFetcher)
	activity := &activitypub.Activity{
		Type:           activitypub.TypeAnnounce,
		Actor:          announcerURI,
		ActivityObject: noteURI,
		ID:             "https://remote.example/activities/boost2",
	}

	if err := service.HandleAnnounce(ctx, activity); err != nil {
		t.Fatalf("HandleAnnounce error: %v", err)
	}
	if objectFetcher.calls != 1 {
		t.Fatalf("object fetch calls = %d, want 1", objectFetcher.calls)
	}

	original, err := store.GetStatusByURI(ctx, pgtype.Text{String: noteURI, Valid: true})
	if err != nil {
		t.Fatalf("original status not created: %v", err)
	}

	announcer, ok := store.accountsByURI[announcerURI]
	if !ok {
		t.Fatalf("announcer account not created")
	}
	if _, ok := store.accountsByURI[authorURI]; !ok {
		t.Fatalf("author account not created")
	}

	if _, ok := findReblog(store, announcer.ID, original.ID); !ok {
		t.Fatalf("reblog status not created")
	}
	if store.statusStats[original.ID].ReblogsCount != 1 {
		t.Fatalf("reblogs_count = %d, want 1", store.statusStats[original.ID].ReblogsCount)
	}
}

func findReblog(store *announceStore, accountID, originalID int64) (db.Status, bool) {
	for _, status := range store.statusesByID {
		if status.AccountID != accountID {
			continue
		}
		if !status.ReblogOfID.Valid {
			continue
		}
		if status.ReblogOfID.Int64 == originalID {
			return status, true
		}
	}
	return db.Status{}, false
}

func makeActor(uri, username string) *activitypub.Actor {
	return &activitypub.Actor{
		Object: activitypub.Object{
			ID:   uri,
			Type: activitypub.TypePerson,
		},
		PreferredUsername: username,
		Inbox:             uri + "/inbox",
		PublicKey: &activitypub.PublicKey{
			ID:           uri + "#main-key",
			Owner:        uri,
			PublicKeyPem: "pub",
		},
	}
}
