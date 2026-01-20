package activitypub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
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

type stubActivityPubStore struct {
	accountsByUsername    map[string]db.Account
	accountsByID          map[int64]db.Account
	accountsByURI         map[string]db.Account
	statusesByAccount     map[int64][]db.Status
	statusesByID          map[int64]db.Status
	mentionsByStatus      map[int64][]db.Mention
	tagsByStatus          map[int64][]db.GetTagsByStatusIDsRow
	attachmentsByStatus   map[int64][]db.MediaAttachment
	followersByAccount    map[int64][]db.Account
	followingByAccount    map[int64][]db.Account
	pinnedByAccount       map[int64][]db.Status
	featuredTagsByAccount map[int64][]db.FeaturedTag
}

func newStubActivityPubStore() *stubActivityPubStore {
	return &stubActivityPubStore{
		accountsByUsername:    map[string]db.Account{},
		accountsByID:          map[int64]db.Account{},
		accountsByURI:         map[string]db.Account{},
		statusesByAccount:     map[int64][]db.Status{},
		statusesByID:          map[int64]db.Status{},
		mentionsByStatus:      map[int64][]db.Mention{},
		tagsByStatus:          map[int64][]db.GetTagsByStatusIDsRow{},
		attachmentsByStatus:   map[int64][]db.MediaAttachment{},
		followersByAccount:    map[int64][]db.Account{},
		followingByAccount:    map[int64][]db.Account{},
		pinnedByAccount:       map[int64][]db.Status{},
		featuredTagsByAccount: map[int64][]db.FeaturedTag{},
	}
}

func (s *stubActivityPubStore) addAccount(account db.Account) {
	s.accountsByUsername[strings.ToLower(account.Username)] = account
	s.accountsByID[account.ID] = account
	if account.Uri != "" {
		s.accountsByURI[account.Uri] = account
	}
}

func (s *stubActivityPubStore) addStatus(status db.Status) {
	s.statusesByID[status.ID] = status
	s.statusesByAccount[status.AccountID] = append(s.statusesByAccount[status.AccountID], status)
}

func (s *stubActivityPubStore) addPinnedStatus(accountID int64, status db.Status) {
	s.pinnedByAccount[accountID] = append(s.pinnedByAccount[accountID], status)
}

func (s *stubActivityPubStore) addFeaturedTag(accountID int64, tag db.FeaturedTag) {
	s.featuredTagsByAccount[accountID] = append(s.featuredTagsByAccount[accountID], tag)
}

func (s *stubActivityPubStore) GetAccountByUsername(ctx context.Context, username string) (db.Account, error) {
	account, ok := s.accountsByUsername[strings.ToLower(username)]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *stubActivityPubStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	account, ok := s.accountsByURI[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *stubActivityPubStore) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	account, ok := s.accountsByID[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *stubActivityPubStore) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	accounts := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if account, ok := s.accountsByID[id]; ok {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (s *stubActivityPubStore) GetAccountOutboxStatuses(ctx context.Context, arg db.GetAccountOutboxStatusesParams) ([]db.Status, error) {
	statuses := append([]db.Status{}, s.statusesByAccount[arg.AccountID]...)
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].ID > statuses[j].ID
	})

	filtered := make([]db.Status, 0, len(statuses))
	for _, status := range statuses {
		if status.Visibility == 0 || status.Visibility == 1 {
			filtered = append(filtered, status)
		}
	}

	start := int(arg.Offset)
	if start < 0 {
		start = 0
	}
	if start >= len(filtered) {
		return []db.Status{}, nil
	}
	limit := int(arg.Limit)
	if limit <= 0 {
		limit = len(filtered)
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], nil
}

func (s *stubActivityPubStore) CountAccountOutboxStatuses(ctx context.Context, accountID int64) (int64, error) {
	total := int64(0)
	for _, status := range s.statusesByAccount[accountID] {
		if status.Visibility == 0 || status.Visibility == 1 {
			total++
		}
	}
	return total, nil
}

func (s *stubActivityPubStore) GetStatusesByIDs(ctx context.Context, ids []int64) ([]db.Status, error) {
	statuses := make([]db.Status, 0, len(ids))
	for _, id := range ids {
		if status, ok := s.statusesByID[id]; ok {
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

func (s *stubActivityPubStore) GetMentionsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Mention, error) {
	out := []db.Mention{}
	for _, id := range statusIds {
		out = append(out, s.mentionsByStatus[id]...)
	}
	return out, nil
}

func (s *stubActivityPubStore) GetTagsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetTagsByStatusIDsRow, error) {
	out := []db.GetTagsByStatusIDsRow{}
	for _, id := range statusIds {
		out = append(out, s.tagsByStatus[id]...)
	}
	return out, nil
}

func (s *stubActivityPubStore) GetMediaAttachmentsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.MediaAttachment, error) {
	out := []db.MediaAttachment{}
	for _, id := range statusIds {
		out = append(out, s.attachmentsByStatus[id]...)
	}
	return out, nil
}

func (s *stubActivityPubStore) GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error) {
	accounts := append([]db.Account{}, s.followersByAccount[arg.TargetAccountID]...)
	start := int(arg.Offset)
	if start < 0 {
		start = 0
	}
	if start >= len(accounts) {
		return []db.Account{}, nil
	}
	limit := int(arg.Limit)
	if limit <= 0 {
		limit = len(accounts)
	}
	end := start + limit
	if end > len(accounts) {
		end = len(accounts)
	}
	return accounts[start:end], nil
}

func (s *stubActivityPubStore) GetFollowing(ctx context.Context, arg db.GetFollowingParams) ([]db.Account, error) {
	accounts := append([]db.Account{}, s.followingByAccount[arg.AccountID]...)
	start := int(arg.Offset)
	if start < 0 {
		start = 0
	}
	if start >= len(accounts) {
		return []db.Account{}, nil
	}
	limit := int(arg.Limit)
	if limit <= 0 {
		limit = len(accounts)
	}
	end := start + limit
	if end > len(accounts) {
		end = len(accounts)
	}
	return accounts[start:end], nil
}

func (s *stubActivityPubStore) CountFollowers(ctx context.Context, accountID int64) (int64, error) {
	return int64(len(s.followersByAccount[accountID])), nil
}

func (s *stubActivityPubStore) CountFollowing(ctx context.Context, accountID int64) (int64, error) {
	return int64(len(s.followingByAccount[accountID])), nil
}

func (s *stubActivityPubStore) GetPinnedStatuses(ctx context.Context, accountID int64) ([]db.Status, error) {
	return append([]db.Status{}, s.pinnedByAccount[accountID]...), nil
}

func (s *stubActivityPubStore) ListFeaturedTags(ctx context.Context, accountID int64) ([]db.FeaturedTag, error) {
	return append([]db.FeaturedTag{}, s.featuredTagsByAccount[accountID]...), nil
}

func signedInboxRequest(t *testing.T, body []byte, keyID, privateKeyPEM string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/inbox", bytes.NewReader(body))
	signer, err := activitypub.NewHTTPSigner(keyID, privateKeyPEM)
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return req
}

func TestFederationHandler_PostInbox_VerifiesSignature(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

func TestFederationHandler_PostInbox_MissingSignature(t *testing.T) {
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          "https://remote.example/users/alice",
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://example.com/inbox", bytes.NewReader(body))

	handler := &FederationHandler{
		domain:       "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestFederationHandler_PostInbox_DigestMismatch(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	tampered := []byte(`{"type":"Follow","actor":"https://remote.example/users/alice","object":"https://example.com/users/eve"}`)
	req.Body = io.NopCloser(bytes.NewReader(tampered))
	req.ContentLength = int64(len(tampered))

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestFederationHandler_PostInbox_RoutesActivity(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handled := false
	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		handled = true
		if activity.Type != activitypub.TypeFollow {
			t.Errorf("activity type = %q, want %q", activity.Type, activitypub.TypeFollow)
		}
		return nil
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if !handled {
		t.Fatal("expected handler to be called")
	}
}

func TestFederationHandler_PostInbox_RoutesAcceptReject(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	localURI := "https://example.com/users/bob"

	cases := []struct {
		name         string
		activityType string
	}{
		{name: "accept", activityType: activitypub.TypeAccept},
		{name: "reject", activityType: activitypub.TypeReject},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"type":  tc.activityType,
				"actor": actorURI,
				"object": map[string]any{
					"type":   activitypub.TypeFollow,
					"actor":  localURI,
					"object": actorURI,
				},
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

			handled := false
			handler := &FederationHandler{
				domain: "example.com",
				actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
					PublicKey: &activitypub.PublicKey{
						ID:           actorURI + "#main-key",
						Owner:        actorURI,
						PublicKeyPem: publicKeyPEM,
					},
				}},
			}
			handler.RegisterInboxHandler(tc.activityType, func(ctx context.Context, activity *activitypub.Activity) error {
				handled = true
				if activity.Type != tc.activityType {
					t.Errorf("activity type = %q, want %q", activity.Type, tc.activityType)
				}
				object, ok := activity.ActivityObject.(map[string]any)
				if !ok {
					t.Fatalf("activity object type = %T, want map", activity.ActivityObject)
				}
				if objType, ok := object["type"].(string); !ok || objType != activitypub.TypeFollow {
					t.Errorf("follow type = %v, want %s", object["type"], activitypub.TypeFollow)
				}
				if object["actor"] != localURI {
					t.Errorf("follow actor = %v, want %s", object["actor"], localURI)
				}
				if object["object"] != actorURI {
					t.Errorf("follow object = %v, want %s", object["object"], actorURI)
				}
				return nil
			})

			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
			}
			if !handled {
				t.Fatal("expected handler to be called")
			}
		})
	}
}

func TestFederationHandler_PostInbox_ArrayTypeRoutes(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	body := []byte(fmt.Sprintf(`{"type":["Activity","Follow"],"actor":"%s","object":"https://example.com/users/bob"}`, actorURI))
	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handled := false
	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		handled = true
		if activity.Type != activitypub.TypeFollow {
			t.Errorf("activity type = %q, want %q", activity.Type, activitypub.TypeFollow)
		}
		return nil
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if !handled {
		t.Fatal("expected handler to be called")
	}
}

func TestFederationHandler_PostInbox_MissingType(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	body := []byte(`{"actor":"` + actorURI + `","object":"https://example.com/users/bob"}`)

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFederationHandler_PostInbox_HandlerErrorStatus(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		return &InboxError{Status: http.StatusUnprocessableEntity, Message: "Unprocessable activity"}
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestFederationHandler_GetOutbox_CollectionsAndPages(t *testing.T) {
	store := newStubActivityPubStore()
	alice := db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"}
	store.addAccount(alice)

	original := db.Status{
		ID:         3,
		AccountID:  2,
		Uri:        pgtype.Text{String: "https://remote.example/users/bob/statuses/3", Valid: true},
		Visibility: 0,
		CreatedAt:  pgtype.Timestamptz{Time: pgtype.Timestamptz{}.Time, Valid: true},
	}
	store.addStatus(original)
	store.addAccount(db.Account{
		ID:       2,
		Username: "bob",
		Domain:   pgtype.Text{String: "remote.example", Valid: true},
		Uri:      "https://remote.example/users/bob",
	})

	store.addStatus(db.Status{
		ID:         1,
		AccountID:  1,
		Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/1", Valid: true},
		Text:       "hello",
		Visibility: 0,
		CreatedAt:  pgtype.Timestamptz{Time: pgtype.Timestamptz{}.Time, Valid: true},
	})
	store.addStatus(db.Status{
		ID:         2,
		AccountID:  1,
		Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/2", Valid: true},
		Visibility: 0,
		ReblogOfID: pgtype.Int8{Int64: 3, Valid: true},
		CreatedAt:  pgtype.Timestamptz{Time: pgtype.Timestamptz{}.Time, Valid: true},
	})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/alice/outbox", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var collection map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &collection); err != nil {
		t.Fatalf("unmarshal collection: %v", err)
	}
	if collection["type"] != activitypub.TypeOrderedCollection {
		t.Fatalf("type = %v, want %s", collection["type"], activitypub.TypeOrderedCollection)
	}

	req = httptest.NewRequest(http.MethodGet, "/users/alice/outbox?page=true", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var page map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal page: %v", err)
	}
	if page["type"] != activitypub.TypeOrderedCollectionPage {
		t.Fatalf("page type = %v, want %s", page["type"], activitypub.TypeOrderedCollectionPage)
	}
	rawItems, ok := page["orderedItems"]
	if !ok {
		t.Fatalf("orderedItems missing, body=%s", rec.Body.String())
	}
	items, ok := rawItems.([]any)
	if !ok {
		t.Fatalf("orderedItems type = %T, want []any", rawItems)
	}
	if len(items) != 2 {
		t.Fatalf("orderedItems len = %d, want 2", len(items))
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("first item type = %T, want map", items[0])
	}
	if first["type"] != activitypub.TypeAnnounce {
		t.Fatalf("first type = %v, want %s", first["type"], activitypub.TypeAnnounce)
	}
	if first["object"] != "https://remote.example/users/bob/statuses/3" {
		t.Fatalf("announce object = %v, want %s", first["object"], "https://remote.example/users/bob/statuses/3")
	}
	second, ok := items[1].(map[string]any)
	if !ok {
		t.Fatalf("second item type = %T, want map", items[1])
	}
	if second["type"] != activitypub.TypeCreate {
		t.Fatalf("second type = %v, want %s", second["type"], activitypub.TypeCreate)
	}
}

func TestFederationHandler_GetFollowersPage(t *testing.T) {
	store := newStubActivityPubStore()
	alice := db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"}
	store.addAccount(alice)

	store.followersByAccount[1] = []db.Account{
		{ID: 2, Username: "bob", Uri: "https://remote.example/users/bob", Domain: pgtype.Text{String: "remote.example", Valid: true}},
		{ID: 3, Username: "carol", Uri: "https://example.net/users/carol", Domain: pgtype.Text{String: "example.net", Valid: true}},
	}

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/alice/followers?page=true", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var page map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal page: %v", err)
	}
	rawItems, ok := page["orderedItems"]
	if !ok {
		t.Fatalf("orderedItems missing, body=%s", rec.Body.String())
	}
	items, ok := rawItems.([]any)
	if !ok {
		t.Fatalf("orderedItems type = %T, want []any", rawItems)
	}
	if len(items) != 2 {
		t.Fatalf("orderedItems len = %d, want 2", len(items))
	}
	if items[0] != "https://remote.example/users/bob" {
		t.Fatalf("first follower = %v, want %s", items[0], "https://remote.example/users/bob")
	}
}

func TestFederationHandler_GetFeaturedCollections(t *testing.T) {
	store := newStubActivityPubStore()
	alice := db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"}
	store.addAccount(alice)

	store.addPinnedStatus(1, db.Status{
		ID:         10,
		AccountID:  1,
		Uri:        pgtype.Text{String: "https://example.com/users/alice/statuses/10", Valid: true},
		Text:       "pinned",
		Visibility: 0,
		CreatedAt:  pgtype.Timestamptz{Time: pgtype.Timestamptz{}.Time, Valid: true},
	})

	store.addFeaturedTag(1, db.FeaturedTag{ID: 1, Name: "golang"})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/alice/collections/featured?page=true", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var featured map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &featured); err != nil {
		t.Fatalf("unmarshal featured: %v", err)
	}
	rawFeaturedItems, ok := featured["orderedItems"]
	if !ok {
		t.Fatalf("featured orderedItems missing, body=%s", rec.Body.String())
	}
	featuredItems, ok := rawFeaturedItems.([]any)
	if !ok {
		t.Fatalf("featured orderedItems type = %T, want []any", rawFeaturedItems)
	}
	if len(featuredItems) != 1 {
		t.Fatalf("featured orderedItems len = %d, want 1", len(featuredItems))
	}

	req = httptest.NewRequest(http.MethodGet, "/users/alice/collections/tags?page=true", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var tags map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
		t.Fatalf("unmarshal tags: %v", err)
	}
	rawTagItems, ok := tags["orderedItems"]
	if !ok {
		t.Fatalf("tags orderedItems missing, body=%s", rec.Body.String())
	}
	tagItems, ok := rawTagItems.([]any)
	if !ok {
		t.Fatalf("tags orderedItems type = %T, want []any", rawTagItems)
	}
	if len(tagItems) != 1 {
		t.Fatalf("tags orderedItems len = %d, want 1", len(tagItems))
	}
	tag, ok := tagItems[0].(map[string]any)
	if !ok {
		t.Fatalf("tag item type = %T, want map", tagItems[0])
	}
	if tag["type"] != "Hashtag" {
		t.Fatalf("tag type = %v, want Hashtag", tag["type"])
	}
	if tag["name"] != "#golang" {
		t.Fatalf("tag name = %v, want #golang", tag["name"])
	}
}

func TestFederationHandler_ApAliasRoutes(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/ap/users/alice/followers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
