package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type statusStat struct {
	reblogs    int64
	favourites int64
}

type favouriteKey struct {
	accountID int64
	statusID  int64
}

type undoStore struct {
	accounts       map[string]db.Account
	statuses       map[string]db.Status
	statusesByID   map[int64]db.Status
	follows        map[followKey]db.Follow
	followRequests map[followKey]db.FollowRequest
	favourites     map[favouriteKey]db.Favourite
	blocks         map[followKey]db.Block
	stats          map[int64]*statusStat
}

func newUndoStore() *undoStore {
	return &undoStore{
		accounts:       make(map[string]db.Account),
		statuses:       make(map[string]db.Status),
		statusesByID:   make(map[int64]db.Status),
		follows:        make(map[followKey]db.Follow),
		followRequests: make(map[followKey]db.FollowRequest),
		favourites:     make(map[favouriteKey]db.Favourite),
		blocks:         make(map[followKey]db.Block),
		stats:          make(map[int64]*statusStat),
	}
}

func (s *undoStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	account, ok := s.accounts[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *undoStore) GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error) {
	if !uri.Valid || uri.String == "" {
		return db.Status{}, pgx.ErrNoRows
	}
	status, ok := s.statuses[uri.String]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	return status, nil
}

func (s *undoStore) GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	follow, ok := s.follows[key]
	if !ok {
		return db.Follow{}, pgx.ErrNoRows
	}
	return follow, nil
}

func (s *undoStore) DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) error {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	delete(s.follows, key)
	return nil
}

func (s *undoStore) GetFollowRequest(ctx context.Context, arg db.GetFollowRequestParams) (db.FollowRequest, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	req, ok := s.followRequests[key]
	if !ok {
		return db.FollowRequest{}, pgx.ErrNoRows
	}
	return req, nil
}

func (s *undoStore) DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	delete(s.followRequests, key)
	return nil
}

func (s *undoStore) GetFavourite(ctx context.Context, arg db.GetFavouriteParams) (db.Favourite, error) {
	key := favouriteKey{accountID: arg.AccountID, statusID: arg.StatusID}
	fav, ok := s.favourites[key]
	if !ok {
		return db.Favourite{}, pgx.ErrNoRows
	}
	return fav, nil
}

func (s *undoStore) DeleteFavourite(ctx context.Context, arg db.DeleteFavouriteParams) error {
	key := favouriteKey{accountID: arg.AccountID, statusID: arg.StatusID}
	delete(s.favourites, key)
	return nil
}

func (s *undoStore) GetReblogByAccountAndOriginal(ctx context.Context, arg db.GetReblogByAccountAndOriginalParams) (db.Status, error) {
	if !arg.ReblogOfID.Valid {
		return db.Status{}, pgx.ErrNoRows
	}

	var found db.Status
	for _, status := range s.statusesByID {
		if status.AccountID != arg.AccountID {
			continue
		}
		if !status.ReblogOfID.Valid || status.ReblogOfID.Int64 != arg.ReblogOfID.Int64 {
			continue
		}
		if found.ID == 0 || status.ID > found.ID {
			found = status
		}
	}

	if found.ID == 0 {
		return db.Status{}, pgx.ErrNoRows
	}
	return found, nil
}

func (s *undoStore) DeleteStatus(ctx context.Context, id int64) error {
	status, ok := s.statusesByID[id]
	if !ok {
		return nil
	}
	delete(s.statusesByID, id)
	if status.Uri.Valid && status.Uri.String != "" {
		delete(s.statuses, status.Uri.String)
	}
	return nil
}

func (s *undoStore) DecrementStatusReblogs(ctx context.Context, statusID int64) error {
	stat := s.stats[statusID]
	if stat == nil {
		stat = &statusStat{}
		s.stats[statusID] = stat
	}
	if stat.reblogs > 0 {
		stat.reblogs--
	}
	return nil
}

func (s *undoStore) DecrementStatusFavourites(ctx context.Context, statusID int64) error {
	stat := s.stats[statusID]
	if stat == nil {
		stat = &statusStat{}
		s.stats[statusID] = stat
	}
	if stat.favourites > 0 {
		stat.favourites--
	}
	return nil
}

func (s *undoStore) GetBlock(ctx context.Context, arg db.GetBlockParams) (db.Block, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	block, ok := s.blocks[key]
	if !ok {
		return db.Block{}, pgx.ErrNoRows
	}
	return block, nil
}

func (s *undoStore) DeleteBlock(ctx context.Context, arg db.DeleteBlockParams) error {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	delete(s.blocks, key)
	return nil
}

func TestUndoService_UndoFollowRemovesRecords(t *testing.T) {
	store := newUndoStore()
	local := db.Account{ID: 1, Uri: "https://local.test/users/bob"}
	remote := db.Account{ID: 2, Uri: "https://remote.example/users/alice", Domain: pgtype.Text{String: "remote.example", Valid: true}}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote

	key := followKey{accountID: remote.ID, targetID: local.ID}
	store.follows[key] = db.Follow{AccountID: remote.ID, TargetAccountID: local.ID}
	store.followRequests[key] = db.FollowRequest{AccountID: remote.ID, TargetAccountID: local.ID}

	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          remote.Uri,
			ActivityObject: local.Uri,
		},
	}

	if err := service.HandleUndo(context.Background(), undo); err != nil {
		t.Fatalf("HandleUndo() error = %v", err)
	}
	if _, ok := store.follows[key]; ok {
		t.Fatal("expected follow to be removed")
	}
	if _, ok := store.followRequests[key]; ok {
		t.Fatal("expected follow request to be removed")
	}
}

func TestUndoService_UndoLikeRemovesFavourite(t *testing.T) {
	store := newUndoStore()
	remote := db.Account{ID: 2, Uri: "https://remote.example/users/alice", Domain: pgtype.Text{String: "remote.example", Valid: true}}
	store.accounts[remote.Uri] = remote
	status := db.Status{ID: 10, Uri: pgtype.Text{String: "https://local.test/statuses/1", Valid: true}}
	store.statuses[status.Uri.String] = status
	store.statusesByID[status.ID] = status
	store.favourites[favouriteKey{accountID: remote.ID, statusID: status.ID}] = db.Favourite{
		AccountID: remote.ID,
		StatusID:  status.ID,
	}
	store.stats[status.ID] = &statusStat{favourites: 1}

	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeLike,
			Actor:          remote.Uri,
			ActivityObject: status.Uri.String,
		},
	}

	if err := service.HandleUndo(context.Background(), undo); err != nil {
		t.Fatalf("HandleUndo() error = %v", err)
	}
	if _, ok := store.favourites[favouriteKey{accountID: remote.ID, statusID: status.ID}]; ok {
		t.Fatal("expected favourite to be removed")
	}
	if store.stats[status.ID].favourites != 0 {
		t.Fatalf("favourites count = %d, want 0", store.stats[status.ID].favourites)
	}
}

func TestUndoService_UndoAnnounceRemovesReblog(t *testing.T) {
	store := newUndoStore()
	remote := db.Account{ID: 2, Uri: "https://remote.example/users/alice", Domain: pgtype.Text{String: "remote.example", Valid: true}}
	store.accounts[remote.Uri] = remote
	status := db.Status{ID: 10, Uri: pgtype.Text{String: "https://local.test/statuses/1", Valid: true}}
	store.statuses[status.Uri.String] = status
	store.statusesByID[status.ID] = status

	announceID := "https://remote.example/activities/boost1"
	reblog := db.Status{
		ID:         20,
		Uri:        pgtype.Text{String: announceID, Valid: true},
		AccountID:  remote.ID,
		ReblogOfID: pgtype.Int8{Int64: status.ID, Valid: true},
	}
	store.statuses[reblog.Uri.String] = reblog
	store.statusesByID[reblog.ID] = reblog
	store.stats[status.ID] = &statusStat{reblogs: 1}

	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			ID:             announceID,
			Type:           activitypub.TypeAnnounce,
			Actor:          remote.Uri,
			ActivityObject: status.Uri.String,
		},
	}

	if err := service.HandleUndo(context.Background(), undo); err != nil {
		t.Fatalf("HandleUndo() error = %v", err)
	}
	if _, ok := store.statusesByID[reblog.ID]; ok {
		t.Fatal("expected reblog to be removed")
	}
	if store.stats[status.ID].reblogs != 0 {
		t.Fatalf("reblogs count = %d, want 0", store.stats[status.ID].reblogs)
	}
}

func TestUndoService_UndoAnnounceFallbackLookup(t *testing.T) {
	store := newUndoStore()
	remote := db.Account{ID: 2, Uri: "https://remote.example/users/alice", Domain: pgtype.Text{String: "remote.example", Valid: true}}
	store.accounts[remote.Uri] = remote
	status := db.Status{ID: 10, Uri: pgtype.Text{String: "https://local.test/statuses/1", Valid: true}}
	store.statuses[status.Uri.String] = status
	store.statusesByID[status.ID] = status

	reblog := db.Status{
		ID:         20,
		Uri:        pgtype.Text{String: "https://remote.example/statuses/boosted", Valid: true},
		AccountID:  remote.ID,
		ReblogOfID: pgtype.Int8{Int64: status.ID, Valid: true},
	}
	store.statuses[reblog.Uri.String] = reblog
	store.statusesByID[reblog.ID] = reblog
	store.stats[status.ID] = &statusStat{reblogs: 1}

	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeAnnounce,
			Actor:          remote.Uri,
			ActivityObject: status.Uri.String,
		},
	}

	if err := service.HandleUndo(context.Background(), undo); err != nil {
		t.Fatalf("HandleUndo() error = %v", err)
	}
	if _, ok := store.statusesByID[reblog.ID]; ok {
		t.Fatal("expected reblog to be removed")
	}
	if store.stats[status.ID].reblogs != 0 {
		t.Fatalf("reblogs count = %d, want 0", store.stats[status.ID].reblogs)
	}
}

func TestUndoService_UndoBlockRemovesBlock(t *testing.T) {
	store := newUndoStore()
	local := db.Account{ID: 1, Uri: "https://local.test/users/bob"}
	remote := db.Account{ID: 2, Uri: "https://remote.example/users/alice", Domain: pgtype.Text{String: "remote.example", Valid: true}}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote

	key := followKey{accountID: remote.ID, targetID: local.ID}
	store.blocks[key] = db.Block{AccountID: remote.ID, TargetAccountID: local.ID}

	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeBlock,
			Actor:          remote.Uri,
			ActivityObject: local.Uri,
		},
	}

	if err := service.HandleUndo(context.Background(), undo); err != nil {
		t.Fatalf("HandleUndo() error = %v", err)
	}
	if _, ok := store.blocks[key]; ok {
		t.Fatal("expected block to be removed")
	}
}

func TestUndoService_ActorMismatch(t *testing.T) {
	store := newUndoStore()
	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: "https://remote.example/users/alice",
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          "https://remote.example/users/other",
			ActivityObject: "https://local.test/users/bob",
		},
	}

	err := service.HandleUndo(context.Background(), undo)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusBadRequest)
	}
}

func TestUndoService_UnsupportedObjectIsNoop(t *testing.T) {
	store := newUndoStore()
	service := NewUndoService(store, "local.test")
	undo := &activitypub.Activity{
		Type:  activitypub.TypeUndo,
		Actor: "https://remote.example/users/alice",
		ActivityObject: map[string]any{
			"type":   "Add",
			"actor":  "https://remote.example/users/alice",
			"object": "https://example.com/collection",
		},
	}

	if err := service.HandleUndo(context.Background(), undo); err != nil {
		t.Fatalf("HandleUndo() error = %v", err)
	}
}
