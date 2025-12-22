package federation

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type likeKey struct {
	accountID int64
	statusID  int64
}

type likeStore struct {
	*createStore
	favourites map[likeKey]db.Favourite
}

func newLikeStore() *likeStore {
	return &likeStore{
		createStore: newCreateStore(),
		favourites:  make(map[likeKey]db.Favourite),
	}
}

func (s *likeStore) GetFavourite(ctx context.Context, arg db.GetFavouriteParams) (db.Favourite, error) {
	fav, ok := s.favourites[likeKey{accountID: arg.AccountID, statusID: arg.StatusID}]
	if !ok {
		return db.Favourite{}, pgx.ErrNoRows
	}
	return fav, nil
}

func (s *likeStore) CreateFavourite(ctx context.Context, arg db.CreateFavouriteParams) (db.Favourite, error) {
	key := likeKey{accountID: arg.AccountID, statusID: arg.StatusID}
	if _, exists := s.favourites[key]; exists {
		return db.Favourite{}, &pgconn.PgError{Code: "23505"}
	}
	fav := db.Favourite{
		AccountID: arg.AccountID,
		StatusID:  arg.StatusID,
	}
	s.favourites[key] = fav
	return fav, nil
}

func (s *likeStore) IncrementStatusFavourites(ctx context.Context, statusID int64) error {
	stat, ok := s.statusStats[statusID]
	if !ok {
		stat = db.StatusStat{ID: statusID, StatusID: statusID}
	}
	stat.FavouritesCount++
	s.statusStats[statusID] = stat
	return nil
}

func TestLikeService_CreatesFavouriteAndNotification(t *testing.T) {
	ctx := context.Background()
	store := newLikeStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	status, err := store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        pgtype.Text{String: "https://local.test/statuses/1", Valid: true},
		AccountID:  local.ID,
		Visibility: httpVisibilityPublic,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateStatus error: %v", err)
	}

	likerURI := "https://remote.example/users/alice"
	service := NewLikeService(store, "local.test", stubActorFetcher{
		actor: makeActor(likerURI, "alice"),
	})

	activity := &activitypub.Activity{
		Type:           activitypub.TypeLike,
		Actor:          likerURI,
		ActivityObject: status.Uri.String,
		ID:             "https://remote.example/activities/like1",
	}

	if err := service.HandleLike(ctx, activity); err != nil {
		t.Fatalf("HandleLike error: %v", err)
	}

	liker, ok := store.accountsByURI[likerURI]
	if !ok {
		t.Fatalf("liker account not created")
	}

	if _, ok := store.favourites[likeKey{accountID: liker.ID, statusID: status.ID}]; !ok {
		t.Fatalf("favourite not created")
	}

	stat := store.statusStats[status.ID]
	if stat.FavouritesCount != 1 {
		t.Fatalf("favourites_count = %d, want 1", stat.FavouritesCount)
	}

	if len(store.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(store.notifications))
	}
	notif := store.notifications[0]
	if notif.AccountID != local.ID || !notif.Type.Valid || notif.Type.String != "favourite" {
		t.Fatalf("notification = %+v, want favourite for local account", notif)
	}
	if notif.ActivityID != status.ID {
		t.Fatalf("notification activity_id = %d, want %d", notif.ActivityID, status.ID)
	}
}

func TestLikeService_DedupesExistingFavourite(t *testing.T) {
	ctx := context.Background()
	store := newLikeStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	liker := db.Account{
		ID:       2,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.example", Valid: true},
		Uri:      "https://remote.example/users/alice",
	}
	store.accountsByURI[liker.Uri] = liker
	store.accountsByID[liker.ID] = liker

	status, _ := store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        pgtype.Text{String: "https://local.test/statuses/1", Valid: true},
		AccountID:  local.ID,
		Visibility: httpVisibilityPublic,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})

	store.favourites[likeKey{accountID: liker.ID, statusID: status.ID}] = db.Favourite{
		AccountID: liker.ID,
		StatusID:  status.ID,
	}
	store.statusStats[status.ID] = db.StatusStat{ID: status.ID, StatusID: status.ID, FavouritesCount: 1}

	service := NewLikeService(store, "local.test", stubActorFetcher{
		actor: makeActor(liker.Uri, "alice"),
	})
	activity := &activitypub.Activity{
		Type:           activitypub.TypeLike,
		Actor:          liker.Uri,
		ActivityObject: status.Uri.String,
		ID:             "https://remote.example/activities/like1",
	}

	if err := service.HandleLike(ctx, activity); err != nil {
		t.Fatalf("HandleLike error: %v", err)
	}

	if store.statusStats[status.ID].FavouritesCount != 1 {
		t.Fatalf("favourites_count = %d, want 1", store.statusStats[status.ID].FavouritesCount)
	}
	if len(store.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0", len(store.notifications))
	}
}

func TestLikeService_IgnoresLocalActor(t *testing.T) {
	ctx := context.Background()
	store := newLikeStore()

	service := NewLikeService(store, "local.test", nil)
	activity := &activitypub.Activity{
		Type:           activitypub.TypeLike,
		Actor:          "https://local.test/users/alice",
		ActivityObject: "https://local.test/statuses/1",
	}

	if err := service.HandleLike(ctx, activity); err != nil {
		t.Fatalf("HandleLike error: %v", err)
	}
	if len(store.favourites) != 0 || len(store.notifications) != 0 {
		t.Fatalf("expected no favourites or notifications")
	}
}

func TestLikeService_IgnoresMissingStatus(t *testing.T) {
	ctx := context.Background()
	store := newLikeStore()

	likerURI := "https://remote.example/users/alice"
	service := NewLikeService(store, "local.test", stubActorFetcher{
		actor: makeActor(likerURI, "alice"),
	})
	activity := &activitypub.Activity{
		Type:           activitypub.TypeLike,
		Actor:          likerURI,
		ActivityObject: "https://local.test/statuses/404",
	}

	if err := service.HandleLike(ctx, activity); err != nil {
		t.Fatalf("HandleLike error: %v", err)
	}
	if len(store.favourites) != 0 {
		t.Fatalf("expected no favourites")
	}
}
