package activitypub

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
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

type countingActorFetcher struct {
	stubActorFetcher
	calls atomic.Int64
}

func (f *countingActorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	f.calls.Add(1)
	return f.stubActorFetcher.FetchActor(ctx, actorURI)
}

type stubActivityPubStore struct {
	accountsByUsername     map[string]db.Account
	accountsByID           map[int64]db.Account
	accountsByURI          map[string]db.Account
	conversationsByParent  map[string]db.Conversation
	statusesByConversation map[int64][]db.Status
	statusesByAccount      map[int64][]db.Status
	statusesByID           map[int64]db.Status
	statusStatsByID        map[int64]db.StatusStat
	followerURIsByAccount  map[int64][]string
	quotesByID             map[int64]db.Quote
	conversationsByID      map[int64]db.Conversation
	mentionsByStatus       map[int64][]db.Mention
	tagsByStatus           map[int64][]db.GetTagsByStatusIDsRow
	attachmentsByStatus    map[int64][]db.MediaAttachment
	followersByAccount     map[int64][]db.Account
	followingByAccount     map[int64][]db.Account
	pinnedByAccount        map[int64][]db.Status
	featuredTagsByAccount  map[int64][]db.FeaturedTag
}

func conversationKey(accountID pgtype.Int8, statusID pgtype.Int8) string {
	if !accountID.Valid || !statusID.Valid {
		return ""
	}
	return fmt.Sprintf("%d-%d", accountID.Int64, statusID.Int64)
}

func newStubActivityPubStore() *stubActivityPubStore {
	return &stubActivityPubStore{
		accountsByUsername:     map[string]db.Account{},
		accountsByID:           map[int64]db.Account{},
		accountsByURI:          map[string]db.Account{},
		conversationsByParent:  map[string]db.Conversation{},
		statusesByConversation: map[int64][]db.Status{},
		statusesByAccount:      map[int64][]db.Status{},
		statusesByID:           map[int64]db.Status{},
		statusStatsByID:        map[int64]db.StatusStat{},
		followerURIsByAccount:  map[int64][]string{},
		quotesByID:             map[int64]db.Quote{},
		conversationsByID:      map[int64]db.Conversation{},
		mentionsByStatus:       map[int64][]db.Mention{},
		tagsByStatus:           map[int64][]db.GetTagsByStatusIDsRow{},
		attachmentsByStatus:    map[int64][]db.MediaAttachment{},
		followersByAccount:     map[int64][]db.Account{},
		followingByAccount:     map[int64][]db.Account{},
		pinnedByAccount:        map[int64][]db.Status{},
		featuredTagsByAccount:  map[int64][]db.FeaturedTag{},
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
	if status.ConversationID.Valid {
		s.statusesByConversation[status.ConversationID.Int64] = append(s.statusesByConversation[status.ConversationID.Int64], status)
	}
}

func (s *stubActivityPubStore) addConversation(conversation db.Conversation) {
	key := conversationKey(conversation.ParentAccountID, conversation.ParentStatusID)
	if key == "" {
		return
	}
	s.conversationsByParent[key] = conversation
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

func (s *stubActivityPubStore) GetConversationByParentAccountAndStatus(ctx context.Context, arg db.GetConversationByParentAccountAndStatusParams) (db.Conversation, error) {
	key := conversationKey(arg.ParentAccountID, arg.ParentStatusID)
	conversation, ok := s.conversationsByParent[key]
	if !ok {
		return db.Conversation{}, pgx.ErrNoRows
	}
	return conversation, nil
}

func (s *stubActivityPubStore) GetConversationsByIDs(ctx context.Context, ids []int64) ([]db.Conversation, error) {
	conversations := make([]db.Conversation, 0, len(ids))
	for _, id := range ids {
		if conversation, ok := s.conversationsByID[id]; ok {
			conversations = append(conversations, conversation)
		}
	}
	return conversations, nil
}

func (s *stubActivityPubStore) GetStatusByID(ctx context.Context, id int64) (db.Status, error) {
	status, ok := s.statusesByID[id]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	return status, nil
}

func (s *stubActivityPubStore) GetStatusStats(ctx context.Context, statusID int64) (db.StatusStat, error) {
	stat, ok := s.statusStatsByID[statusID]
	if !ok {
		return db.StatusStat{}, pgx.ErrNoRows
	}
	return stat, nil
}

func (s *stubActivityPubStore) GetStatusStatsByIDs(ctx context.Context, statusIds []int64) ([]db.StatusStat, error) {
	stats := make([]db.StatusStat, 0, len(statusIds))
	for _, id := range statusIds {
		if stat, ok := s.statusStatsByID[id]; ok {
			stats = append(stats, stat)
		}
	}
	return stats, nil
}

func (s *stubActivityPubStore) GetQuote(ctx context.Context, id int64) (db.Quote, error) {
	quote, ok := s.quotesByID[id]
	if !ok {
		return db.Quote{}, pgx.ErrNoRows
	}
	return quote, nil
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
		if status.Visibility != 0 && status.Visibility != 1 {
			continue
		}
		if arg.MaxID.Valid && status.ID >= arg.MaxID.Int64 {
			continue
		}
		if arg.MinID.Valid && status.ID <= arg.MinID.Int64 {
			continue
		}
		if arg.SinceID.Valid && status.ID <= arg.SinceID.Int64 {
			continue
		}
		filtered = append(filtered, status)
	}
	limit := int(arg.Limit)
	if limit <= 0 {
		limit = len(filtered)
	}
	if limit > len(filtered) {
		limit = len(filtered)
	}
	return filtered[:limit], nil
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

func (s *stubActivityPubStore) ListConversationStatuses(ctx context.Context, arg db.ListConversationStatusesParams) ([]db.Status, error) {
	statuses := append([]db.Status{}, s.statusesByConversation[arg.ConversationID.Int64]...)
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})

	filtered := make([]db.Status, 0, len(statuses))
	for _, status := range statuses {
		if arg.MinID > 0 && status.ID <= arg.MinID {
			continue
		}
		if status.Visibility != 0 && status.Visibility != 1 {
			continue
		}
		filtered = append(filtered, status)
	}

	limit := int(arg.Limit)
	if limit <= 0 || limit > len(filtered) {
		limit = len(filtered)
	}
	return filtered[:limit], nil
}

func (s *stubActivityPubStore) ListStatusReplies(ctx context.Context, arg db.ListStatusRepliesParams) ([]db.Status, error) {
	statuses := make([]db.Status, 0)
	for _, status := range s.statusesByID {
		if !status.InReplyToID.Valid || status.InReplyToID.Int64 != arg.StatusID.Int64 {
			continue
		}
		if status.Visibility != 0 && status.Visibility != 1 {
			continue
		}
		if arg.MinID.Valid && status.ID <= arg.MinID.Int64 {
			continue
		}
		if arg.AccountID.Valid && status.AccountID != arg.AccountID.Int64 {
			continue
		}
		if arg.ExcludeAccountID.Valid && status.AccountID == arg.ExcludeAccountID.Int64 {
			continue
		}
		statuses = append(statuses, status)
	}
	if len(statuses) == 0 {
		return []db.Status{}, nil
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})
	limit := int(arg.Limit)
	if limit <= 0 || limit > len(statuses) {
		limit = len(statuses)
	}
	return statuses[:limit], nil
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

func (s *stubActivityPubStore) ListFollowerURIsByPrefix(ctx context.Context, arg db.ListFollowerURIsByPrefixParams) ([]string, error) {
	uris := append([]string{}, s.followerURIsByAccount[arg.TargetAccountID]...)
	if !arg.Prefix.Valid || strings.TrimSpace(arg.Prefix.String) == "" {
		return uris, nil
	}
	prefix := strings.TrimSpace(arg.Prefix.String)
	filtered := make([]string, 0, len(uris))
	for _, uri := range uris {
		trimmed := strings.TrimSpace(uri)
		if trimmed == "" {
			continue
		}
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix) {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered, nil
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
	return signedInboxRequestTo(t, "https://example.com/inbox", body, keyID, privateKeyPEM)
}

func signedInboxRequestTo(t *testing.T, url string, body []byte, keyID, privateKeyPEM string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	signer, err := activitypub.NewHTTPSigner(keyID, privateKeyPEM)
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return req
}

func signedGetRequest(t *testing.T, url, keyID, privateKeyPEM string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, url, nil)
	signer, err := activitypub.NewHTTPSigner(keyID, privateKeyPEM)
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	if err := signer.Sign(req, nil); err != nil {
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
			Object: activitypub.Object{ID: actorURI},
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
			Object: activitypub.Object{ID: actorURI},
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
			Object: activitypub.Object{ID: actorURI},
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
					Object: activitypub.Object{ID: actorURI},
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
			Object: activitypub.Object{ID: actorURI},
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
			Object: activitypub.Object{ID: actorURI},
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
			Object: activitypub.Object{ID: actorURI},
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

func TestFederationHandler_PostInbox_RejectsReplays(t *testing.T) {
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
	follow := map[string]any{
		"id":     "https://remote.example/activities/follow-1",
		"type":   activitypub.TypeFollow,
		"actor":  actorURI,
		"object": "https://example.com/users/bob",
	}
	undo := map[string]any{
		"id":     "https://remote.example/activities/undo-1",
		"type":   activitypub.TypeUndo,
		"actor":  actorURI,
		"object": follow,
	}
	followBody, err := json.Marshal(follow)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	undoBody, err := json.Marshal(undo)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			Object: activitypub.Object{ID: actorURI},
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}
	following := false
	follows, undos := 0, 0
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		follows++
		following = true
		return nil
	})
	handler.RegisterInboxHandler(activitypub.TypeUndo, func(ctx context.Context, activity *activitypub.Activity) error {
		undos++
		following = false
		return nil
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	serve := func(req *http.Request) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
		}
	}
	replay := func(req *http.Request, body []byte) *http.Request {
		copied := httptest.NewRequest(req.Method, req.URL.String(), bytes.NewReader(body))
		copied.Host = req.Host
		copied.Header = req.Header.Clone()
		return copied
	}

	followReq := signedInboxRequest(t, followBody, actorURI+"#main-key", privateKeyPEM)
	serve(followReq)
	serve(signedInboxRequest(t, undoBody, actorURI+"#main-key", privateKeyPEM))
	if following {
		t.Fatal("follow not undone")
	}

	// Replaying the captured Follow while its signature is still valid must
	// not re-establish the relationship the actor withdrew.
	serve(replay(followReq, followBody))
	if following {
		t.Fatal("replayed Follow re-established the follow")
	}
	if follows != 1 || undos != 1 {
		t.Fatalf("follows = %d, undos = %d, want 1 each", follows, undos)
	}
}

// paddedCreateActivity returns a Create activity of exactly size bytes, padded
// out by its note's content.
func paddedCreateActivity(t *testing.T, actorURI string, size int64) []byte {
	t.Helper()

	activity := func(content string) []byte {
		body, err := json.Marshal(map[string]any{
			"type":  activitypub.TypeCreate,
			"actor": actorURI,
			"object": map[string]any{
				"type":    "Note",
				"content": content,
			},
		})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return body
	}

	padding := size - int64(len(activity("")))
	if padding < 0 {
		t.Fatalf("size %d is smaller than an empty activity", size)
	}
	body := activity(strings.Repeat("a", int(padding)))
	if int64(len(body)) != size {
		t.Fatalf("activity size = %d, want %d", len(body), size)
	}
	return body
}

func newSigningActor(t *testing.T, actorURI string) (privateKeyPEM string, actor *activitypub.Actor) {
	t.Helper()

	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	return activitypub.EncodePrivateKey(priv), &activitypub.Actor{
		Object: activitypub.Object{ID: actorURI},
		PublicKey: &activitypub.PublicKey{
			ID:           actorURI + "#main-key",
			Owner:        actorURI,
			PublicKeyPem: publicKeyPEM,
		},
	}
}

func TestFederationHandler_PostInbox_BodyLimit(t *testing.T) {
	actorURI := "https://remote.example/users/alice"
	privateKeyPEM, actor := newSigningActor(t, actorURI)
	body, err := json.Marshal(activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	size := int64(len(body))

	routes := []string{
		"/inbox",
		"/users/bob/inbox",
		"/actor/inbox",
		"/ap/inbox",
		"/ap/users/bob/inbox",
	}
	tests := []struct {
		name          string
		limit         int64
		unknownLength bool
		wantStatus    int
	}{
		{name: "within limit", limit: size + 1, wantStatus: http.StatusAccepted},
		{name: "at limit", limit: size, wantStatus: http.StatusAccepted},
		{name: "over limit", limit: size - 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "over limit without content length", limit: size - 1, unknownLength: true, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, route := range routes {
		for _, tt := range tests {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				fetcher := &countingActorFetcher{stubActorFetcher: stubActorFetcher{actor: actor}}
				handler := &FederationHandler{domain: "example.com", actorFetcher: fetcher}
				handler.SetInboxMaxBodyBytes(tt.limit)
				handled := 0
				handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
					handled++
					return nil
				})
				mux := http.NewServeMux()
				handler.RegisterRoutes(mux)

				req := signedInboxRequestTo(t, "https://example.com"+route, body, actorURI+"#main-key", privateKeyPEM)
				if tt.unknownLength {
					req.ContentLength = -1
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)

				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
				}
				want := 0
				if tt.wantStatus == http.StatusAccepted {
					want = 1
				}
				if calls := fetcher.calls.Load(); calls != int64(want) {
					t.Fatalf("actor fetches = %d, want %d", calls, want)
				}
				if handled != want {
					t.Fatalf("activities handled = %d, want %d", handled, want)
				}
			})
		}
	}
}

func TestFederationHandler_PostInbox_DefaultBodyLimit(t *testing.T) {
	actorURI := "https://remote.example/users/alice"
	privateKeyPEM, actor := newSigningActor(t, actorURI)

	tests := []struct {
		name       string
		configure  func(h *FederationHandler)
		size       int64
		wantStatus int
	}{
		{name: "unset limit at default", size: config.DefaultInboxMaxBodyBytes, wantStatus: http.StatusAccepted},
		{name: "unset limit over default", size: config.DefaultInboxMaxBodyBytes + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{
			name:       "zero limit at default",
			configure:  func(h *FederationHandler) { h.SetInboxMaxBodyBytes(0) },
			size:       config.DefaultInboxMaxBodyBytes,
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "negative limit over default",
			configure:  func(h *FederationHandler) { h.SetInboxMaxBodyBytes(-1) },
			size:       config.DefaultInboxMaxBodyBytes + 1,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &countingActorFetcher{stubActorFetcher: stubActorFetcher{actor: actor}}
			handler := &FederationHandler{domain: "example.com", actorFetcher: fetcher}
			if tt.configure != nil {
				tt.configure(handler)
			}
			var handled *activitypub.Activity
			handler.RegisterInboxHandler(activitypub.TypeCreate, func(ctx context.Context, activity *activitypub.Activity) error {
				handled = activity
				return nil
			})
			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)

			body := paddedCreateActivity(t, actorURI, tt.size)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus != http.StatusAccepted {
				if calls := fetcher.calls.Load(); calls != 0 || handled != nil {
					t.Fatalf("oversized activity reached actor fetch (%d) or handler (%v)", calls, handled != nil)
				}
				return
			}
			if handled == nil {
				t.Fatal("activity at the limit was not handled")
			}
			var sent struct {
				Object struct {
					Content string `json:"content"`
				} `json:"object"`
			}
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			object, ok := handled.ActivityObject.(map[string]any)
			if !ok {
				t.Fatalf("activity object = %T, want a note", handled.ActivityObject)
			}
			if content, _ := object["content"].(string); content != sent.Object.Content {
				t.Fatalf("note content has %d bytes, want %d", len(content), len(sent.Object.Content))
			}
		})
	}
}

// TestFederationHandler_PostInbox_BodyLimitOverHTTP checks the limit against a
// real server, where an oversized body may also arrive chunked.
func TestFederationHandler_PostInbox_BodyLimitOverHTTP(t *testing.T) {
	const limit = 64 << 10
	actorURI := "https://remote.example/users/alice"
	privateKeyPEM, actor := newSigningActor(t, actorURI)

	tests := []struct {
		name       string
		size       int64
		chunked    bool
		wantStatus int
		wantCalls  int64
	}{
		{name: "declared length at limit", size: limit, wantStatus: http.StatusAccepted, wantCalls: 1},
		{name: "chunked at limit", size: limit, chunked: true, wantStatus: http.StatusAccepted, wantCalls: 1},
		{name: "declared length over limit", size: limit + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "chunked over limit", size: 2 * limit, chunked: true, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &countingActorFetcher{stubActorFetcher: stubActorFetcher{actor: actor}}
			handler := &FederationHandler{domain: "example.com", actorFetcher: fetcher}
			handler.SetInboxMaxBodyBytes(limit)
			var handled atomic.Int64
			handler.RegisterInboxHandler(activitypub.TypeCreate, func(ctx context.Context, activity *activitypub.Activity) error {
				handled.Add(1)
				return nil
			})
			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)
			var contentLength atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contentLength.Store(r.ContentLength)
				mux.ServeHTTP(w, r)
			}))
			defer srv.Close()

			body := paddedCreateActivity(t, actorURI, tt.size)
			var reader io.Reader = bytes.NewReader(body)
			if tt.chunked {
				// A reader of unknown length is sent with chunked encoding.
				reader = io.MultiReader(reader)
			}
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/inbox", reader)
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			signer, err := activitypub.NewHTTPSigner(actorURI+"#main-key", privateKeyPEM)
			if err != nil {
				t.Fatalf("NewHTTPSigner() error = %v", err)
			}
			if err := signer.Sign(req, body); err != nil {
				t.Fatalf("Sign() error = %v", err)
			}

			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			wantLength := tt.size
			if tt.chunked {
				wantLength = -1
			}
			if got := contentLength.Load(); got != wantLength {
				t.Fatalf("server saw content length %d, want %d", got, wantLength)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if fetches, handled := fetcher.calls.Load(), handled.Load(); fetches != tt.wantCalls || handled != tt.wantCalls {
				t.Fatalf("actor fetches = %d, activities handled = %d, want %d each", fetches, handled, tt.wantCalls)
			}
		})
	}
}

func TestFederationHandler_PostInbox_RequiresVerifiedSignature(t *testing.T) {
	handler := &FederationHandler{domain: "example.com"}
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		t.Fatal("unverified activity was handled")
		return nil
	})

	body := []byte(`{"type":"Follow","actor":"https://remote.example/users/alice","object":"https://example.com/users/bob"}`)
	req := httptest.NewRequest(http.MethodPost, "https://example.com/inbox", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.PostInbox(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestFederationHandler_GetInstanceActor(t *testing.T) {
	store := newStubActivityPubStore()

	_, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	createdAt := time.Date(2024, time.March, 4, 10, 0, 0, 0, time.UTC)
	store.addAccount(db.Account{
		ID:        1,
		Username:  "example.com",
		Uri:       "https://example.com/actor",
		PublicKey: publicKeyPEM,
		ActorType: pgtype.Text{String: activitypub.TypeApplication, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/actor", nil)
	req.Header.Set("Accept", activitypub.ActivityPubContentType)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var actor map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &actor); err != nil {
		t.Fatalf("unmarshal actor: %v", err)
	}
	if actor["id"] != "https://example.com/actor" {
		t.Fatalf("id = %v, want %q", actor["id"], "https://example.com/actor")
	}
	if actor["type"] != activitypub.TypeApplication {
		t.Fatalf("type = %v, want %s", actor["type"], activitypub.TypeApplication)
	}
	if actor["preferredUsername"] != "example.com" {
		t.Fatalf("preferredUsername = %v, want %q", actor["preferredUsername"], "example.com")
	}
	if actor["inbox"] != "https://example.com/actor/inbox" {
		t.Fatalf("inbox = %v, want %q", actor["inbox"], "https://example.com/actor/inbox")
	}
	if actor["outbox"] != "https://example.com/actor/outbox" {
		t.Fatalf("outbox = %v, want %q", actor["outbox"], "https://example.com/actor/outbox")
	}
	if actor["url"] != "https://example.com/about/more?instance_actor=true" {
		t.Fatalf("url = %v, want %q", actor["url"], "https://example.com/about/more?instance_actor=true")
	}
	if actor["manuallyApprovesFollowers"] != true {
		t.Fatalf("manuallyApprovesFollowers = %v, want true", actor["manuallyApprovesFollowers"])
	}

	endpoints, ok := actor["endpoints"].(map[string]any)
	if !ok {
		t.Fatalf("endpoints missing or invalid: %T", actor["endpoints"])
	}
	if endpoints["sharedInbox"] != "https://example.com/inbox" {
		t.Fatalf("endpoints.sharedInbox = %v, want %q", endpoints["sharedInbox"], "https://example.com/inbox")
	}

	publicKey, ok := actor["publicKey"].(map[string]any)
	if !ok {
		t.Fatalf("publicKey missing or invalid: %T", actor["publicKey"])
	}
	if publicKey["publicKeyPem"] != publicKeyPEM {
		t.Fatalf("publicKey.publicKeyPem = %v, want %q", publicKey["publicKeyPem"], publicKeyPEM)
	}
}

func TestFederationHandler_GetInstanceOutbox(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "example.com", Uri: "https://example.com/actor"})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/actor/outbox", nil)
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
	if collection["id"] != "https://example.com/actor/outbox" {
		t.Fatalf("id = %v, want %q", collection["id"], "https://example.com/actor/outbox")
	}
	if collection["totalItems"] != float64(0) {
		t.Fatalf("totalItems = %v, want 0", collection["totalItems"])
	}
}

func TestFederationHandler_GetContext(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})

	conversation := db.Conversation{
		ID:              10,
		ParentAccountID: pgtype.Int8{Int64: 1, Valid: true},
		ParentStatusID:  pgtype.Int8{Int64: 100, Valid: true},
	}
	store.addConversation(conversation)

	store.addStatus(db.Status{
		ID:             101,
		AccountID:      1,
		ConversationID: pgtype.Int8{Int64: 10, Valid: true},
		Visibility:     0,
		Local:          pgtype.Bool{Bool: true, Valid: true},
	})
	store.addStatus(db.Status{
		ID:             102,
		AccountID:      1,
		ConversationID: pgtype.Int8{Int64: 10, Valid: true},
		Visibility:     1,
		Local:          pgtype.Bool{Bool: true, Valid: true},
	})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/contexts/1-100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal context: %v", err)
	}
	if resp["type"] != activitypub.TypeCollection {
		t.Fatalf("type = %v, want %s", resp["type"], activitypub.TypeCollection)
	}
	if resp["attributedTo"] != "https://example.com/users/alice" {
		t.Fatalf("attributedTo = %v, want %q", resp["attributedTo"], "https://example.com/users/alice")
	}

	first, ok := resp["first"].(map[string]any)
	if !ok {
		t.Fatalf("first missing or invalid: %T", resp["first"])
	}
	if first["type"] != activitypub.TypeCollectionPage {
		t.Fatalf("first.type = %v, want %s", first["type"], activitypub.TypeCollectionPage)
	}
	if first["partOf"] != "https://example.com/contexts/1-100/items" {
		t.Fatalf("first.partOf = %v, want %q", first["partOf"], "https://example.com/contexts/1-100/items")
	}
	items, ok := first["items"].([]any)
	if !ok {
		t.Fatalf("first.items missing or invalid: %T", first["items"])
	}
	if len(items) != 2 {
		t.Fatalf("first.items len = %d, want 2", len(items))
	}
	if items[0] != "https://example.com/users/alice/statuses/101" {
		t.Fatalf("first.items[0] = %v, want %q", items[0], "https://example.com/users/alice/statuses/101")
	}
	if items[1] != "https://example.com/users/alice/statuses/102" {
		t.Fatalf("first.items[1] = %v, want %q", items[1], "https://example.com/users/alice/statuses/102")
	}
}

func TestFederationHandler_GetContextItems(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})

	conversation := db.Conversation{
		ID:              10,
		ParentAccountID: pgtype.Int8{Int64: 1, Valid: true},
		ParentStatusID:  pgtype.Int8{Int64: 100, Valid: true},
	}
	store.addConversation(conversation)

	store.addStatus(db.Status{
		ID:             101,
		AccountID:      1,
		ConversationID: pgtype.Int8{Int64: 10, Valid: true},
		Visibility:     0,
		Local:          pgtype.Bool{Bool: true, Valid: true},
	})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/contexts/1-100/items?page=true", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal context items: %v", err)
	}
	if resp["type"] != activitypub.TypeCollectionPage {
		t.Fatalf("type = %v, want %s", resp["type"], activitypub.TypeCollectionPage)
	}
	if resp["partOf"] != "https://example.com/contexts/1-100/items" {
		t.Fatalf("partOf = %v, want %q", resp["partOf"], "https://example.com/contexts/1-100/items")
	}
	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("items missing or invalid: %T", resp["items"])
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	if items[0] != "https://example.com/users/alice/statuses/101" {
		t.Fatalf("items[0] = %v, want %q", items[0], "https://example.com/users/alice/statuses/101")
	}
}

func TestFederationHandler_GetStatus(t *testing.T) {
	store := newStubActivityPubStore()
	alice := db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"}
	store.addAccount(alice)

	conversation := db.Conversation{
		ID:              50,
		ParentAccountID: pgtype.Int8{Int64: 1, Valid: true},
		ParentStatusID:  pgtype.Int8{Int64: 10, Valid: true},
	}
	store.conversationsByID[50] = conversation

	store.addStatus(db.Status{
		ID:             10,
		AccountID:      1,
		Text:           "hello",
		Visibility:     0,
		Local:          pgtype.Bool{Bool: true, Valid: true},
		ConversationID: pgtype.Int8{Int64: 50, Valid: true},
	})
	store.addStatus(db.Status{
		ID:             11,
		AccountID:      1,
		Text:           "reply",
		InReplyToID:    pgtype.Int8{Int64: 10, Valid: true},
		Visibility:     0,
		Local:          pgtype.Bool{Bool: true, Valid: true},
		ConversationID: pgtype.Int8{Valid: false},
	})
	store.statusStatsByID[10] = db.StatusStat{StatusID: 10, FavouritesCount: 2, ReblogsCount: 3}

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/alice/statuses/10", nil)
	req.Header.Set("Accept", activitypub.ActivityPubContentType)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var note map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &note); err != nil {
		t.Fatalf("unmarshal note: %v", err)
	}
	if note["type"] != activitypub.TypeNote {
		t.Fatalf("type = %v, want %s", note["type"], activitypub.TypeNote)
	}
	if note["id"] != "https://example.com/users/alice/statuses/10" {
		t.Fatalf("id = %v, want %q", note["id"], "https://example.com/users/alice/statuses/10")
	}
	if note["attributedTo"] != "https://example.com/users/alice" {
		t.Fatalf("attributedTo = %v, want %q", note["attributedTo"], "https://example.com/users/alice")
	}
	if note["content"] != "<p>hello</p>" {
		t.Fatalf("content = %v, want %q", note["content"], "<p>hello</p>")
	}
	noteLikes, ok := note["likes"].(map[string]any)
	if !ok {
		t.Fatalf("likes missing or invalid: %T", note["likes"])
	}
	if noteLikes["totalItems"] != float64(2) {
		t.Fatalf("likes.totalItems = %v, want 2", noteLikes["totalItems"])
	}
	noteShares, ok := note["shares"].(map[string]any)
	if !ok {
		t.Fatalf("shares missing or invalid: %T", note["shares"])
	}
	if noteShares["totalItems"] != float64(3) {
		t.Fatalf("shares.totalItems = %v, want 3", noteShares["totalItems"])
	}
	noteReplies, ok := note["replies"].(map[string]any)
	if !ok {
		t.Fatalf("replies missing or invalid: %T", note["replies"])
	}
	if noteReplies["id"] != "https://example.com/users/alice/statuses/10/replies" {
		t.Fatalf("replies.id = %v, want %q", noteReplies["id"], "https://example.com/users/alice/statuses/10/replies")
	}
	if note["context"] != "https://example.com/contexts/1-10" {
		t.Fatalf("context = %v, want %q", note["context"], "https://example.com/contexts/1-10")
	}
	if note["conversation"] != "https://example.com/contexts/1-10" {
		t.Fatalf("conversation = %v, want %q", note["conversation"], "https://example.com/contexts/1-10")
	}

	req = httptest.NewRequest(http.MethodGet, "/users/alice/statuses/10/activity", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("activity status = %d, want %d", rec.Code, http.StatusOK)
	}

	var activity map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &activity); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	if activity["type"] != activitypub.TypeCreate {
		t.Fatalf("activity.type = %v, want %s", activity["type"], activitypub.TypeCreate)
	}

	req = httptest.NewRequest(http.MethodGet, "/users/alice/statuses/10/replies?page=true", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("replies status = %d, want %d", rec.Code, http.StatusOK)
	}

	var replies map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &replies); err != nil {
		t.Fatalf("unmarshal replies: %v", err)
	}
	if replies["type"] != activitypub.TypeCollectionPage {
		t.Fatalf("replies.type = %v, want %s", replies["type"], activitypub.TypeCollectionPage)
	}
	items, ok := replies["items"].([]any)
	if !ok {
		t.Fatalf("replies.items missing or invalid: %T", replies["items"])
	}
	if len(items) != 1 {
		t.Fatalf("replies.items len = %d, want 1", len(items))
	}
	if items[0] != "https://example.com/users/alice/statuses/11" {
		t.Fatalf("replies.items[0] = %v, want %q", items[0], "https://example.com/users/alice/statuses/11")
	}

	req = httptest.NewRequest(http.MethodGet, "/users/alice/statuses/10/likes", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("likes status = %d, want %d", rec.Code, http.StatusOK)
	}
	var likes map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &likes); err != nil {
		t.Fatalf("unmarshal likes: %v", err)
	}
	if likes["totalItems"] != float64(2) {
		t.Fatalf("likes.totalItems = %v, want 2", likes["totalItems"])
	}

	req = httptest.NewRequest(http.MethodGet, "/users/alice/statuses/10/shares", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("shares status = %d, want %d", rec.Code, http.StatusOK)
	}
	var shares map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &shares); err != nil {
		t.Fatalf("unmarshal shares: %v", err)
	}
	if shares["totalItems"] != float64(3) {
		t.Fatalf("shares.totalItems = %v, want 3", shares["totalItems"])
	}
}

func TestFederationHandler_GetFollowersSynchronization(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})
	store.followerURIsByAccount[1] = []string{
		"https://remote.example/users/bob",
		"https://other.example/users/carol",
	}

	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/admin"
	store.addAccount(db.Account{ID: 5, Uri: actorURI, PublicKey: publicKeyPEM})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := signedGetRequest(t, "https://example.com/users/alice/followers_synchronization", actorURI+"#main-key", privateKeyPEM)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["type"] != activitypub.TypeOrderedCollection {
		t.Fatalf("type = %v, want %s", resp["type"], activitypub.TypeOrderedCollection)
	}
	items, ok := resp["orderedItems"].([]any)
	if !ok {
		t.Fatalf("orderedItems missing or invalid: %T", resp["orderedItems"])
	}
	if len(items) != 1 {
		t.Fatalf("orderedItems len = %d, want 1", len(items))
	}
	if items[0] != "https://remote.example/users/bob" {
		t.Fatalf("orderedItems[0] = %v, want %q", items[0], "https://remote.example/users/bob")
	}
}

// signDateOnly signs req over the Date header alone, the draft-cavage default
// that gives no request or body integrity.
func signDateOnly(t *testing.T, req *http.Request, key *rsa.PrivateKey, keyID string) {
	t.Helper()

	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	hashed := sha256.Sum256([]byte("date: " + req.Header.Get("Date")))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}
	req.Header.Set("Signature", fmt.Sprintf(
		`keyId="%s",algorithm="rsa-sha256",headers="date",signature="%s"`,
		keyID, base64.StdEncoding.EncodeToString(signature),
	))
}

func TestFederationHandler_GetFollowersSynchronization_RejectsInvalidSignatures(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})
	store.followerURIsByAccount[1] = []string{"https://remote.example/users/bob"}

	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	otherPriv, otherPub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	otherPublicKeyPEM, err := activitypub.EncodePublicKey(otherPub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/admin"
	otherURI := "https://remote.example/users/mallory"
	store.addAccount(db.Account{ID: 5, Uri: actorURI, PublicKey: publicKeyPEM})
	store.addAccount(db.Account{ID: 6, Uri: otherURI, PublicKey: otherPublicKeyPEM})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	syncURL := "https://example.com/users/alice/followers_synchronization"
	tests := []struct {
		name    string
		request func(t *testing.T) *http.Request
	}{
		{
			name: "missing signature",
			request: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, syncURL, nil)
			},
		},
		{
			name: "date-only signature",
			request: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodGet, syncURL, nil)
				signDateOnly(t, req, priv, actorURI+"#main-key")
				return req
			},
		},
		{
			name: "stale signature",
			request: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodGet, syncURL, nil)
				req.Header.Set("Date", time.Now().Add(-2*time.Hour).UTC().Format(http.TimeFormat))
				signer, err := activitypub.NewHTTPSigner(actorURI+"#main-key", privateKeyPEM)
				if err != nil {
					t.Fatalf("NewHTTPSigner() error = %v", err)
				}
				if err := signer.Sign(req, nil); err != nil {
					t.Fatalf("Sign() error = %v", err)
				}
				return req
			},
		},
		{
			name: "signed with key of another actor",
			request: func(t *testing.T) *http.Request {
				return signedGetRequest(t, syncURL, actorURI+"#main-key", activitypub.EncodePrivateKey(otherPriv))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, tt.request(t))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

type uriActorFetcher struct {
	actors  map[string]*activitypub.Actor
	fetched []string
}

func (f *uriActorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	f.fetched = append(f.fetched, actorURI)
	actor, ok := f.actors[actorURI]
	if !ok {
		return nil, fmt.Errorf("actor %s not found", actorURI)
	}
	return actor, nil
}

func TestFederationHandler_GetFollowersSynchronization_KeyURIResolvesActor(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/admin"
	otherHostURI := "https://other.example/users/admin"
	actorDocument := func(id, keyID string) *activitypub.Actor {
		return &activitypub.Actor{
			Object:    activitypub.Object{ID: id},
			PublicKey: &activitypub.PublicKey{ID: keyID, Owner: id, PublicKeyPem: publicKeyPEM},
		}
	}

	tests := []struct {
		name       string
		keyID      string
		fetchURI   string
		actor      *activitypub.Actor
		wantStatus int
	}{
		{
			name:       "path key id serves actor document",
			keyID:      actorURI + "/main-key",
			fetchURI:   actorURI + "/main-key",
			actor:      actorDocument(actorURI, actorURI+"/main-key"),
			wantStatus: http.StatusOK,
		},
		{
			name:       "slash fragment key id serves actor document",
			keyID:      actorURI + "/#main-key",
			fetchURI:   actorURI + "/",
			actor:      actorDocument(actorURI, actorURI+"/#main-key"),
			wantStatus: http.StatusOK,
		},
		{
			name:       "document claims actor on another host",
			keyID:      actorURI + "/main-key",
			fetchURI:   actorURI + "/main-key",
			actor:      actorDocument(otherHostURI, actorURI+"/main-key"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "document declares another key",
			keyID:      actorURI + "/main-key",
			fetchURI:   actorURI + "/main-key",
			actor:      actorDocument(actorURI, actorURI+"#main-key"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "fetcher returns no actor",
			keyID:      actorURI + "/main-key",
			fetchURI:   actorURI + "/main-key",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubActivityPubStore()
			store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})
			store.followerURIsByAccount[1] = []string{
				"https://remote.example/users/bob",
				"https://other.example/users/carol",
			}

			fetcher := &uriActorFetcher{actors: map[string]*activitypub.Actor{tt.fetchURI: tt.actor}}
			handler := NewFederationHandler(store, "example.com")
			handler.actorFetcher = fetcher
			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)

			req := signedGetRequest(t, "https://example.com/users/alice/followers_synchronization", tt.keyID, privateKeyPEM)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if len(fetcher.fetched) != 1 || fetcher.fetched[0] != tt.fetchURI {
				t.Fatalf("fetched = %v, want [%s]", fetcher.fetched, tt.fetchURI)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}

			var resp struct {
				OrderedItems []string `json:"orderedItems"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if len(resp.OrderedItems) != 1 || resp.OrderedItems[0] != "https://remote.example/users/bob" {
				t.Fatalf("orderedItems = %v, want [https://remote.example/users/bob]", resp.OrderedItems)
			}
		})
	}
}

func TestFederationHandler_PostInbox_CachedActorKey(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	otherPriv, otherPub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	otherPublicKeyPEM, err := activitypub.EncodePublicKey(otherPub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	otherURI := "https://remote.example/users/mallory"
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 5, Username: "alice", Uri: actorURI, PublicKey: publicKeyPEM})
	store.addAccount(db.Account{ID: 6, Username: "mallory", Uri: otherURI, PublicKey: otherPublicKeyPEM})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body, err := json.Marshal(activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	tests := []struct {
		name          string
		keyID         string
		privateKeyPEM string
		dateOnly      bool
		wantStatus    int
	}{
		{name: "fragment key id", keyID: actorURI + "#main-key", privateKeyPEM: privateKeyPEM, wantStatus: http.StatusAccepted},
		{name: "path key id", keyID: actorURI + "/main-key", privateKeyPEM: privateKeyPEM, wantStatus: http.StatusAccepted},
		{name: "key id of another actor", keyID: otherURI + "#main-key", privateKeyPEM: privateKeyPEM, wantStatus: http.StatusUnauthorized},
		{name: "signed by another actor", keyID: otherURI + "#main-key", privateKeyPEM: activitypub.EncodePrivateKey(otherPriv), wantStatus: http.StatusUnauthorized},
		{name: "date-only signature", keyID: actorURI + "#main-key", dateOnly: true, wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.dateOnly {
				req = httptest.NewRequest(http.MethodPost, "https://example.com/inbox", bytes.NewReader(body))
				signDateOnly(t, req, priv, tt.keyID)
			} else {
				req = signedInboxRequest(t, body, tt.keyID, tt.privateKeyPEM)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestFederationHandler_GetQuoteAuthorization(t *testing.T) {
	store := newStubActivityPubStore()
	store.addAccount(db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice"})
	store.addAccount(db.Account{ID: 2, Username: "bob", Uri: "https://example.com/users/bob"})

	store.addStatus(db.Status{
		ID:         100,
		AccountID:  2,
		Visibility: 0,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})
	store.addStatus(db.Status{
		ID:         200,
		AccountID:  1,
		Visibility: 0,
		Local:      pgtype.Bool{Bool: true, Valid: true},
	})
	store.quotesByID[10] = db.Quote{
		ID:              10,
		AccountID:       2,
		StatusID:        100,
		QuotedStatusID:  pgtype.Int8{Int64: 200, Valid: true},
		QuotedAccountID: pgtype.Int8{Int64: 1, Valid: true},
		State:           quoteStateAccepted,
	}

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/users/alice/quote_authorizations/10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["type"] != "QuoteAuthorization" {
		t.Fatalf("type = %v, want QuoteAuthorization", resp["type"])
	}
	if resp["id"] != "https://example.com/users/alice/quote_authorizations/10" {
		t.Fatalf("id = %v, want %q", resp["id"], "https://example.com/users/alice/quote_authorizations/10")
	}
	if resp["attributedTo"] != "https://example.com/users/alice" {
		t.Fatalf("attributedTo = %v, want %q", resp["attributedTo"], "https://example.com/users/alice")
	}
	if resp["interactionTarget"] != "https://example.com/users/alice/statuses/200" {
		t.Fatalf("interactionTarget = %v, want %q", resp["interactionTarget"], "https://example.com/users/alice/statuses/200")
	}
	if resp["interactingObject"] != "https://example.com/users/bob/statuses/100" {
		t.Fatalf("interactingObject = %v, want %q", resp["interactingObject"], "https://example.com/users/bob/statuses/100")
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

	req := httptest.NewRequest(http.MethodGet, "/users/alice/collections/featured", nil)
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

	req = httptest.NewRequest(http.MethodGet, "/users/alice/collections/tags", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var tags map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
		t.Fatalf("unmarshal tags: %v", err)
	}
	rawTagItems, ok := tags["items"]
	if !ok {
		t.Fatalf("tags items missing, body=%s", rec.Body.String())
	}
	tagItems, ok := rawTagItems.([]any)
	if !ok {
		t.Fatalf("tags items type = %T, want []any", rawTagItems)
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

	req = httptest.NewRequest(http.MethodGet, "/ap/users/1/followers", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
