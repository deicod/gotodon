package federation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type stubWebFinger struct {
	resp     *activitypub.WebFingerResponse
	err      error
	calls    int
	lastAcct string
}

func (s *stubWebFinger) Lookup(ctx context.Context, acct string) (*activitypub.WebFingerResponse, error) {
	s.calls++
	s.lastAcct = acct
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

type resolverActorFetcher struct {
	payload actorPayload
	err     error
	calls   int
	lastURI string
}

func (s *resolverActorFetcher) FetchActorPayload(ctx context.Context, actorURI string) (actorPayload, error) {
	s.calls++
	s.lastURI = actorURI
	if s.err != nil {
		return actorPayload{}, s.err
	}
	return s.payload, nil
}

type resolverRefresher struct {
	calls   int
	lastURI string
	err     error
}

func (r *resolverRefresher) EnqueueRemoteAccount(ctx context.Context, actorURI string) error {
	r.calls++
	r.lastURI = actorURI
	return r.err
}

type resolverStore struct {
	accounts     map[int64]db.Account
	byURI        map[string]int64
	byHandle     map[string]int64
	nextID       int64
	updates      int
	statsCreated int
}

func newResolverStore() *resolverStore {
	return &resolverStore{
		accounts: make(map[int64]db.Account),
		byURI:    make(map[string]int64),
		byHandle: make(map[string]int64),
	}
}

func (s *resolverStore) GetAccountByUsername(ctx context.Context, username string) (db.Account, error) {
	key := strings.ToLower(username)
	id, ok := s.byHandle[key]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	acc := s.accounts[id]
	if acc.Domain.Valid {
		return db.Account{}, pgx.ErrNoRows
	}
	return acc, nil
}

func (s *resolverStore) GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error) {
	key := strings.ToLower(arg.Username + "@" + arg.Domain)
	id, ok := s.byHandle[key]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return s.accounts[id], nil
}

func (s *resolverStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	id, ok := s.byURI[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return s.accounts[id], nil
}

func (s *resolverStore) CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error) {
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
		Discoverable: arg.Discoverable,
		Indexable:    arg.Indexable,
		ActorType:    arg.ActorType,
		Protocol:     arg.Protocol,
	}
	s.accounts[account.ID] = account
	if account.Uri != "" {
		s.byURI[account.Uri] = account.ID
	}
	s.byHandle[handleKey(account.Username, account.Domain)] = account.ID
	return account, nil
}

func (s *resolverStore) CreateAccountStats(ctx context.Context, accountID int64) (db.AccountStat, error) {
	s.statsCreated++
	return db.AccountStat{AccountID: accountID}, nil
}

func (s *resolverStore) UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error) {
	acc, ok := s.accounts[arg.ID]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	if arg.DisplayName.Valid {
		acc.DisplayName = arg.DisplayName.String
	}
	if arg.Note.Valid {
		acc.Note = arg.Note.String
	}
	if arg.Locked.Valid {
		acc.Locked = arg.Locked.Bool
	}
	if arg.Discoverable.Valid {
		acc.Discoverable = arg.Discoverable
	}
	if arg.Indexable.Valid {
		acc.Indexable = arg.Indexable.Bool
	}
	if arg.Fields != nil {
		acc.Fields = arg.Fields
	}
	if arg.Url.Valid {
		acc.Url = arg.Url
	}
	if arg.PublicKey.Valid {
		acc.PublicKey = arg.PublicKey.String
	}
	if arg.ActorType.Valid {
		acc.ActorType = arg.ActorType
	}
	if arg.InboxUrl.Valid {
		acc.InboxUrl = arg.InboxUrl.String
	}
	if arg.OutboxUrl.Valid {
		acc.OutboxUrl = arg.OutboxUrl.String
	}
	if arg.SharedInboxUrl.Valid {
		acc.SharedInboxUrl = arg.SharedInboxUrl.String
	}
	if arg.FollowersUrl.Valid {
		acc.FollowersUrl = arg.FollowersUrl.String
	}
	if arg.FollowingUrl.Valid {
		acc.FollowingUrl = arg.FollowingUrl.String
	}
	if arg.FeaturedCollectionUrl.Valid {
		acc.FeaturedCollectionUrl = arg.FeaturedCollectionUrl
	}
	if arg.AvatarRemoteUrl.Valid {
		acc.AvatarRemoteUrl = arg.AvatarRemoteUrl
	}
	if arg.HeaderRemoteUrl.Valid {
		acc.HeaderRemoteUrl = arg.HeaderRemoteUrl.String
	}
	if arg.LastWebfingeredAt.Valid {
		acc.LastWebfingeredAt = arg.LastWebfingeredAt
	}
	s.accounts[arg.ID] = acc
	s.updates++
	return acc, nil
}

func handleKey(username string, domain pgtype.Text) string {
	if domain.Valid && domain.String != "" {
		return strings.ToLower(username + "@" + domain.String)
	}
	return strings.ToLower(username)
}

func TestRemoteActorResolver_ResolveCreatesAccount(t *testing.T) {
	store := newResolverStore()
	webfinger := &stubWebFinger{
		resp: &activitypub.WebFingerResponse{
			Links: []activitypub.WebFingerLink{
				{Rel: activitypub.WebFingerRelSelf, Type: activitypub.ActivityPubContentType, Href: "https://remote.test/users/alice"},
				{Rel: activitypub.WebFingerRelProfilePage, Type: "text/html", Href: "https://remote.test/@alice"},
			},
		},
	}
	fetcher := &resolverActorFetcher{
		payload: actorPayload{
			ID:                           "https://remote.test/users/alice",
			Type:                         activitypub.TypePerson,
			Name:                         "Alice",
			Summary:                      "Bio",
			PreferredUsername:            "alice",
			Inbox:                        "https://remote.test/inbox",
			Outbox:                       "https://remote.test/outbox",
			Followers:                    "https://remote.test/followers",
			Following:                    "https://remote.test/following",
			Featured:                     "https://remote.test/featured",
			SharedInbox:                  "https://remote.test/shared",
			ManuallyApprovesFollowers:    true,
			HasManuallyApprovesFollowers: true,
			HasPublicKey:                 true,
			PublicKey:                    "public-key",
			HasSummary:                   true,
		},
	}
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	resolver := NewRemoteActorResolver(store, "local.test")
	resolver.webfinger = webfinger
	resolver.fetcher = fetcher
	resolver.now = func() time.Time { return now }

	account, err := resolver.ResolveAccount(context.Background(), "alice@remote.test")
	if err != nil {
		t.Fatalf("ResolveAccount error: %v", err)
	}
	if account.Username != "alice" {
		t.Fatalf("username = %q, want alice", account.Username)
	}
	if !account.Domain.Valid || account.Domain.String != "remote.test" {
		t.Fatalf("domain = %+v, want remote.test", account.Domain)
	}
	if account.InboxUrl != "https://remote.test/inbox" {
		t.Fatalf("inbox = %q, want https://remote.test/inbox", account.InboxUrl)
	}
	if account.SharedInboxUrl != "https://remote.test/shared" {
		t.Fatalf("shared inbox = %q, want https://remote.test/shared", account.SharedInboxUrl)
	}
	if account.DisplayName != "Alice" {
		t.Fatalf("display name = %q, want Alice", account.DisplayName)
	}
	if account.Note != "Bio" {
		t.Fatalf("note = %q, want Bio", account.Note)
	}
	if !account.LastWebfingeredAt.Valid || !account.LastWebfingeredAt.Time.Equal(now) {
		t.Fatalf("last_webfingered_at = %v, want %v", account.LastWebfingeredAt, now)
	}
	if webfinger.calls != 1 {
		t.Fatalf("webfinger calls = %d, want 1", webfinger.calls)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetcher calls = %d, want 1", fetcher.calls)
	}
}

func TestRemoteActorResolver_RefreshesStaleAccountAsync(t *testing.T) {
	store := newResolverStore()
	account := db.Account{
		ID:        1,
		Username:  "alice",
		Domain:    pgtype.Text{String: "remote.test", Valid: true},
		Uri:       "https://remote.test/users/alice",
		PublicKey: "pk",
		LastWebfingeredAt: pgtype.Timestamptz{
			Time:  time.Now().Add(-48 * time.Hour),
			Valid: true,
		},
	}
	store.accounts[account.ID] = account
	store.byURI[account.Uri] = account.ID
	store.byHandle[handleKey(account.Username, account.Domain)] = account.ID

	webfinger := &stubWebFinger{
		resp: &activitypub.WebFingerResponse{
			Links: []activitypub.WebFingerLink{
				{Rel: activitypub.WebFingerRelSelf, Type: activitypub.ActivityPubContentType, Href: account.Uri},
			},
		},
	}
	fetcher := &resolverActorFetcher{
		payload: actorPayload{
			ID:                account.Uri,
			Type:              activitypub.TypePerson,
			PreferredUsername: "alice",
			Inbox:             "https://remote.test/inbox",
			HasPublicKey:      true,
			PublicKey:         "pk",
		},
	}
	refresher := &resolverRefresher{}

	resolver := NewRemoteActorResolver(store, "local.test")
	resolver.webfinger = webfinger
	resolver.fetcher = fetcher
	resolver.SetRefresher(refresher)

	got, err := resolver.ResolveAccount(context.Background(), "alice@remote.test")
	if err != nil {
		t.Fatalf("ResolveAccount error: %v", err)
	}
	if got.ID != account.ID {
		t.Fatalf("account.ID = %d, want %d", got.ID, account.ID)
	}
	if refresher.calls != 1 {
		t.Fatalf("refresher.calls = %d, want 1", refresher.calls)
	}
	if refresher.lastURI != account.Uri {
		t.Fatalf("refresher.lastURI = %q, want %q", refresher.lastURI, account.Uri)
	}
	if webfinger.calls != 0 {
		t.Fatalf("webfinger.calls = %d, want 0", webfinger.calls)
	}
	if fetcher.calls != 0 {
		t.Fatalf("fetcher.calls = %d, want 0", fetcher.calls)
	}
}

func TestRemoteActorResolver_UsesCachedAccount(t *testing.T) {
	store := newResolverStore()
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	account, _ := store.CreateAccount(context.Background(), db.CreateAccountParams{
		Username:    "alice",
		Domain:      pgtype.Text{String: "remote.test", Valid: true},
		PublicKey:   "public-key",
		DisplayName: "Alice",
		Note:        "Bio",
		Uri:         "https://remote.test/users/alice",
		Protocol:    1,
	})
	_, _ = store.UpdateAccount(context.Background(), db.UpdateAccountParams{
		ID:                account.ID,
		LastWebfingeredAt: pgtype.Timestamptz{Time: now, Valid: true},
	})

	resolver := NewRemoteActorResolver(store, "local.test")
	resolver.webfinger = &stubWebFinger{}
	resolver.fetcher = &resolverActorFetcher{}
	resolver.now = func() time.Time { return now }

	got, err := resolver.ResolveAccount(context.Background(), "alice@remote.test")
	if err != nil {
		t.Fatalf("ResolveAccount error: %v", err)
	}
	if got.ID != account.ID {
		t.Fatalf("account id = %d, want %d", got.ID, account.ID)
	}
	if resolver.webfinger.(*stubWebFinger).calls != 0 {
		t.Fatalf("expected no webfinger calls")
	}
	if resolver.fetcher.(*resolverActorFetcher).calls != 0 {
		t.Fatalf("expected no fetcher calls")
	}
}

func TestRemoteActorResolver_RefreshesStaleAccount(t *testing.T) {
	store := newResolverStore()
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)
	account, _ := store.CreateAccount(context.Background(), db.CreateAccountParams{
		Username:    "alice",
		Domain:      pgtype.Text{String: "remote.test", Valid: true},
		PublicKey:   "old-key",
		DisplayName: "Old",
		Note:        "Old note",
		Uri:         "https://remote.test/users/alice",
		Protocol:    1,
	})
	_, _ = store.UpdateAccount(context.Background(), db.UpdateAccountParams{
		ID:                account.ID,
		LastWebfingeredAt: pgtype.Timestamptz{Time: stale, Valid: true},
	})

	webfinger := &stubWebFinger{
		resp: &activitypub.WebFingerResponse{
			Links: []activitypub.WebFingerLink{
				{Rel: activitypub.WebFingerRelSelf, Type: activitypub.ActivityPubContentType, Href: "https://remote.test/users/alice"},
			},
		},
	}
	fetcher := &resolverActorFetcher{
		payload: actorPayload{
			ID:                "https://remote.test/users/alice",
			Type:              activitypub.TypePerson,
			Name:              "New",
			Summary:           "New note",
			PreferredUsername: "alice",
			Inbox:             "https://remote.test/inbox",
			HasPublicKey:      true,
			PublicKey:         "new-key",
			HasSummary:        true,
		},
	}

	resolver := NewRemoteActorResolver(store, "local.test")
	resolver.webfinger = webfinger
	resolver.fetcher = fetcher
	resolver.now = func() time.Time { return now }

	got, err := resolver.ResolveAccount(context.Background(), "alice@remote.test")
	if err != nil {
		t.Fatalf("ResolveAccount error: %v", err)
	}
	if got.DisplayName != "New" {
		t.Fatalf("display name = %q, want New", got.DisplayName)
	}
	if !got.LastWebfingeredAt.Valid || !got.LastWebfingeredAt.Time.Equal(now) {
		t.Fatalf("last_webfingered_at = %v, want %v", got.LastWebfingeredAt, now)
	}
	if webfinger.calls != 1 {
		t.Fatalf("webfinger calls = %d, want 1", webfinger.calls)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetcher calls = %d, want 1", fetcher.calls)
	}
}

func TestRemoteActorResolver_FallsBackOnRefreshFailure(t *testing.T) {
	store := newResolverStore()
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)
	account, _ := store.CreateAccount(context.Background(), db.CreateAccountParams{
		Username:    "alice",
		Domain:      pgtype.Text{String: "remote.test", Valid: true},
		PublicKey:   "public-key",
		DisplayName: "Cached",
		Note:        "Cached note",
		Uri:         "https://remote.test/users/alice",
		Protocol:    1,
	})
	_, _ = store.UpdateAccount(context.Background(), db.UpdateAccountParams{
		ID:                account.ID,
		LastWebfingeredAt: pgtype.Timestamptz{Time: stale, Valid: true},
	})

	webfinger := &stubWebFinger{err: errors.New("webfinger down")}

	resolver := NewRemoteActorResolver(store, "local.test")
	resolver.webfinger = webfinger
	resolver.fetcher = &resolverActorFetcher{}
	resolver.now = func() time.Time { return now }

	got, err := resolver.ResolveAccount(context.Background(), "alice@remote.test")
	if err != nil {
		t.Fatalf("ResolveAccount error: %v", err)
	}
	if got.DisplayName != "Cached" {
		t.Fatalf("display name = %q, want Cached", got.DisplayName)
	}
	if webfinger.calls != 1 {
		t.Fatalf("webfinger calls = %d, want 1", webfinger.calls)
	}
}
