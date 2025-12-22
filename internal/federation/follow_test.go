package federation

import (
	"context"
	"errors"
	"testing"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubActorFetcher struct {
	actor *activitypub.Actor
	err   error
}

func (s stubActorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.actor, nil
}

type followKey struct {
	accountID int64
	targetID  int64
}

type fakeStore struct {
	accounts       map[string]db.Account
	follows        map[followKey]db.Follow
	followRequests map[followKey]db.FollowRequest
	blocks         map[followKey]db.Block
	nextID         int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		accounts:       make(map[string]db.Account),
		follows:        make(map[followKey]db.Follow),
		followRequests: make(map[followKey]db.FollowRequest),
		blocks:         make(map[followKey]db.Block),
	}
}

func (s *fakeStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	account, ok := s.accounts[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *fakeStore) CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error) {
	if _, exists := s.accounts[arg.Uri]; exists {
		return db.Account{}, &pgconn.PgError{Code: "23505"}
	}
	s.nextID++
	account := db.Account{
		ID:           s.nextID,
		Username:     arg.Username,
		Domain:       arg.Domain,
		PrivateKey:   arg.PrivateKey,
		PublicKey:    arg.PublicKey,
		DisplayName:  arg.DisplayName,
		Note:         arg.Note,
		Uri:          arg.Uri,
		Url:          arg.Url,
		Locked:       arg.Locked,
		Protocol:     arg.Protocol,
		ActorType:    arg.ActorType,
		Discoverable: arg.Discoverable,
		Indexable:    arg.Indexable,
	}
	s.accounts[arg.Uri] = account
	return account, nil
}

func (s *fakeStore) CreateAccountStats(ctx context.Context, accountID int64) (db.AccountStat, error) {
	return db.AccountStat{AccountID: accountID}, nil
}

func (s *fakeStore) GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	follow, ok := s.follows[key]
	if !ok {
		return db.Follow{}, pgx.ErrNoRows
	}
	return follow, nil
}

func (s *fakeStore) GetFollowRequest(ctx context.Context, arg db.GetFollowRequestParams) (db.FollowRequest, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	req, ok := s.followRequests[key]
	if !ok {
		return db.FollowRequest{}, pgx.ErrNoRows
	}
	return req, nil
}

func (s *fakeStore) CreateFollow(ctx context.Context, arg db.CreateFollowParams) (db.Follow, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	if _, exists := s.follows[key]; exists {
		return db.Follow{}, &pgconn.PgError{Code: "23505"}
	}
	follow := db.Follow{
		AccountID:       arg.AccountID,
		TargetAccountID: arg.TargetAccountID,
		ShowReblogs:     arg.ShowReblogs,
		Notify:          arg.Notify,
		Uri:             arg.Uri,
	}
	s.follows[key] = follow
	return follow, nil
}

func (s *fakeStore) CreateFollowRequest(ctx context.Context, arg db.CreateFollowRequestParams) (db.FollowRequest, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	if _, exists := s.followRequests[key]; exists {
		return db.FollowRequest{}, &pgconn.PgError{Code: "23505"}
	}
	req := db.FollowRequest{
		AccountID:       arg.AccountID,
		TargetAccountID: arg.TargetAccountID,
		ShowReblogs:     arg.ShowReblogs,
		Notify:          arg.Notify,
		Uri:             arg.Uri,
	}
	s.followRequests[key] = req
	return req, nil
}

func (s *fakeStore) DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	delete(s.followRequests, key)
	return nil
}

func (s *fakeStore) GetBlock(ctx context.Context, arg db.GetBlockParams) (db.Block, error) {
	key := followKey{accountID: arg.AccountID, targetID: arg.TargetAccountID}
	block, ok := s.blocks[key]
	if !ok {
		return db.Block{}, pgx.ErrNoRows
	}
	return block, nil
}

func TestFollowService_AutoAccept(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:         1,
		Uri:        "https://local.test/users/bob",
		PrivateKey: pgtype.Text{String: "local-private", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.nextID = local.ID

	actorURI := "https://remote.example/users/alice"
	actor := &activitypub.Actor{
		Object: activitypub.Object{
			ID:   actorURI,
			Type: activitypub.TypePerson,
			Name: "Alice",
		},
		PreferredUsername: "alice",
		Inbox:             "https://remote.example/inbox",
		PublicKey: &activitypub.PublicKey{
			PublicKeyPem: "remote-public",
		},
	}

	var deliveries []string
	deliver := func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
		deliveries = append(deliveries, activity.Type+"@"+inbox)
		return nil
	}

	service := NewFollowService(store, "local.test", stubActorFetcher{actor: actor}, deliver)
	follow := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: local.Uri,
		ID:             "https://remote.example/activities/1",
	}

	if err := service.HandleFollow(context.Background(), follow); err != nil {
		t.Fatalf("HandleFollow() error = %v", err)
	}

	remote := store.accounts[actorURI]
	if remote.ID == 0 {
		t.Fatal("remote account was not created")
	}

	key := followKey{accountID: remote.ID, targetID: local.ID}
	if _, ok := store.follows[key]; !ok {
		t.Fatal("expected follow record to be created")
	}
	if len(store.followRequests) != 0 {
		t.Fatal("did not expect follow request to be created")
	}
	if len(deliveries) != 1 || deliveries[0] != activitypub.TypeAccept+"@"+actor.Inbox {
		t.Fatalf("deliveries = %v, want Accept delivery to inbox", deliveries)
	}
}

func TestFollowService_LockedCreatesRequest(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:         1,
		Uri:        "https://local.test/users/bob",
		Locked:     true,
		PrivateKey: pgtype.Text{String: "local-private", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.nextID = local.ID

	actorURI := "https://remote.example/users/alice"
	actor := &activitypub.Actor{
		Object:            activitypub.Object{ID: actorURI, Type: activitypub.TypePerson},
		PreferredUsername: "alice",
		Inbox:             "https://remote.example/inbox",
		PublicKey:         &activitypub.PublicKey{PublicKeyPem: "remote-public"},
	}

	deliver := func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
		t.Fatalf("unexpected delivery to %s", inbox)
		return nil
	}

	service := NewFollowService(store, "local.test", stubActorFetcher{actor: actor}, deliver)
	follow := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: local.Uri,
	}

	if err := service.HandleFollow(context.Background(), follow); err != nil {
		t.Fatalf("HandleFollow() error = %v", err)
	}

	remote := store.accounts[actorURI]
	key := followKey{accountID: remote.ID, targetID: local.ID}
	if _, ok := store.followRequests[key]; !ok {
		t.Fatal("expected follow request to be created")
	}
	if len(store.follows) != 0 {
		t.Fatal("did not expect follow record to be created")
	}
}

func TestFollowService_BlockRejects(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:         1,
		Uri:        "https://local.test/users/bob",
		PrivateKey: pgtype.Text{String: "local-private", Valid: true},
	}
	remote := db.Account{
		ID:     2,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote
	store.nextID = remote.ID
	store.blocks[followKey{accountID: local.ID, targetID: remote.ID}] = db.Block{
		AccountID:       local.ID,
		TargetAccountID: remote.ID,
	}

	actor := &activitypub.Actor{
		Object:            activitypub.Object{ID: remote.Uri, Type: activitypub.TypePerson},
		PreferredUsername: "alice",
		Inbox:             "https://remote.example/inbox",
		PublicKey:         &activitypub.PublicKey{PublicKeyPem: "remote-public"},
	}

	var deliveries []string
	deliver := func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
		deliveries = append(deliveries, activity.Type+"@"+inbox)
		return nil
	}

	service := NewFollowService(store, "local.test", stubActorFetcher{actor: actor}, deliver)
	follow := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          remote.Uri,
		ActivityObject: local.Uri,
	}

	if err := service.HandleFollow(context.Background(), follow); err != nil {
		t.Fatalf("HandleFollow() error = %v", err)
	}

	if len(store.follows) != 0 {
		t.Fatal("did not expect follow record to be created")
	}
	if len(store.followRequests) != 0 {
		t.Fatal("did not expect follow request to be created")
	}
	if len(deliveries) != 1 || deliveries[0] != activitypub.TypeReject+"@"+actor.Inbox {
		t.Fatalf("deliveries = %v, want Reject delivery to inbox", deliveries)
	}
}

func TestFollowService_NonLocalTarget(t *testing.T) {
	store := newFakeStore()
	service := NewFollowService(store, "local.test", stubActorFetcher{}, nil)
	follow := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          "https://remote.example/users/alice",
		ActivityObject: "https://elsewhere.example/users/bob",
	}

	err := service.HandleFollow(context.Background(), follow)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusBadRequest)
	}
}
