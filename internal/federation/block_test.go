package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type blockKey struct {
	accountID int64
	targetID  int64
}

type blockStore struct {
	*createStore
	blocks         map[blockKey]db.Block
	follows        map[blockKey]db.Follow
	followRequests map[blockKey]db.FollowRequest
}

func newBlockStore() *blockStore {
	return &blockStore{
		createStore:    newCreateStore(),
		blocks:         make(map[blockKey]db.Block),
		follows:        make(map[blockKey]db.Follow),
		followRequests: make(map[blockKey]db.FollowRequest),
	}
}

func (s *blockStore) CreateBlock(ctx context.Context, arg db.CreateBlockParams) (db.Block, error) {
	key := blockKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	if _, exists := s.blocks[key]; exists {
		return db.Block{}, &pgconn.PgError{Code: "23505"}
	}
	block := db.Block{
		AccountID:       arg.AccountID,
		TargetAccountID: arg.TargetAccountID,
		Uri:             arg.Uri,
	}
	s.blocks[key] = block
	return block, nil
}

func (s *blockStore) DeleteFollow(ctx context.Context, arg db.DeleteFollowParams) error {
	delete(s.follows, blockKey{accountID: arg.AccountID, targetID: arg.TargetAccountID})
	return nil
}

func (s *blockStore) DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error {
	delete(s.followRequests, blockKey{accountID: arg.AccountID, targetID: arg.TargetAccountID})
	return nil
}

func TestBlockService_CreatesBlockAndClearsRelationships(t *testing.T) {
	ctx := context.Background()
	store := newBlockStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	remoteURI := "https://remote.example/users/alice"
	service := NewBlockService(store, "local.test", stubActorFetcher{
		actor: makeActor(remoteURI, "alice"),
	})

	remote := db.Account{
		ID:       2,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.example", Valid: true},
		Uri:      remoteURI,
	}
	store.accountsByURI[remote.Uri] = remote
	store.accountsByID[remote.ID] = remote

	store.follows[blockKey{accountID: local.ID, targetID: remote.ID}] = db.Follow{
		AccountID:       local.ID,
		TargetAccountID: remote.ID,
	}
	store.follows[blockKey{accountID: remote.ID, targetID: local.ID}] = db.Follow{
		AccountID:       remote.ID,
		TargetAccountID: local.ID,
	}
	store.followRequests[blockKey{accountID: local.ID, targetID: remote.ID}] = db.FollowRequest{
		AccountID:       local.ID,
		TargetAccountID: remote.ID,
	}

	activity := &activitypub.Activity{
		Type:           activitypub.TypeBlock,
		Actor:          remoteURI,
		ActivityObject: local.Uri,
		ID:             "https://remote.example/activities/block1",
	}

	if err := service.HandleBlock(ctx, activity); err != nil {
		t.Fatalf("HandleBlock error: %v", err)
	}

	block, ok := store.blocks[blockKey{accountID: remote.ID, targetID: local.ID}]
	if !ok {
		t.Fatalf("block not created")
	}
	if !block.Uri.Valid || block.Uri.String != activity.ID {
		t.Fatalf("block uri = %#v, want %q", block.Uri, activity.ID)
	}

	if len(store.follows) != 0 {
		t.Fatalf("follows not cleared")
	}
	if len(store.followRequests) != 0 {
		t.Fatalf("follow requests not cleared")
	}
}

func TestBlockService_NonLocalTarget(t *testing.T) {
	store := newBlockStore()
	service := NewBlockService(store, "local.test", stubActorFetcher{})
	activity := &activitypub.Activity{
		Type:           activitypub.TypeBlock,
		Actor:          "https://remote.example/users/alice",
		ActivityObject: "https://elsewhere.example/users/bob",
	}

	err := service.HandleBlock(context.Background(), activity)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusBadRequest)
	}
}

func TestBlockService_IgnoresLocalActor(t *testing.T) {
	store := newBlockStore()
	service := NewBlockService(store, "local.test", nil)
	activity := &activitypub.Activity{
		Type:           activitypub.TypeBlock,
		Actor:          "https://local.test/users/alice",
		ActivityObject: "https://local.test/users/bob",
	}

	if err := service.HandleBlock(context.Background(), activity); err != nil {
		t.Fatalf("HandleBlock error: %v", err)
	}
	if len(store.blocks) != 0 {
		t.Fatalf("expected no blocks")
	}
}

func TestBlockService_DedupesExistingBlock(t *testing.T) {
	ctx := context.Background()
	store := newBlockStore()

	local := db.Account{
		ID:       1,
		Username: "local",
		Uri:      "https://local.test/users/local",
	}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	remoteURI := "https://remote.example/users/alice"
	remote := db.Account{
		ID:       2,
		Username: "alice",
		Domain:   pgtype.Text{String: "remote.example", Valid: true},
		Uri:      remoteURI,
	}
	store.accountsByURI[remote.Uri] = remote
	store.accountsByID[remote.ID] = remote

	store.blocks[blockKey{accountID: remote.ID, targetID: local.ID}] = db.Block{
		AccountID:       remote.ID,
		TargetAccountID: local.ID,
	}
	store.follows[blockKey{accountID: local.ID, targetID: remote.ID}] = db.Follow{
		AccountID:       local.ID,
		TargetAccountID: remote.ID,
	}

	service := NewBlockService(store, "local.test", stubActorFetcher{
		actor: makeActor(remoteURI, "alice"),
	})
	activity := &activitypub.Activity{
		Type:           activitypub.TypeBlock,
		Actor:          remoteURI,
		ActivityObject: local.Uri,
	}

	if err := service.HandleBlock(ctx, activity); err != nil {
		t.Fatalf("HandleBlock error: %v", err)
	}
	if len(store.blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(store.blocks))
	}
	if len(store.follows) != 0 {
		t.Fatalf("follows not cleared")
	}
}
