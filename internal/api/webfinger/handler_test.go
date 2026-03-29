package webfinger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubStore struct {
	accounts         map[string]db.Account
	accountsByURI    map[string]db.Account
	deletionRequests map[int64]bool
	nextID           int64
}

func (s *stubStore) GetLocalAccountByUsernameAllowSuspended(_ context.Context, username string) (db.Account, error) {
	if acc, ok := s.accounts[strings.ToLower(username)]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubStore) HasAccountDeletionRequest(_ context.Context, accountID int64) (bool, error) {
	return s.deletionRequests[accountID], nil
}

func (s *stubStore) GetAccountByUsername(_ context.Context, username string) (db.Account, error) {
	if acc, ok := s.accounts[strings.ToLower(username)]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubStore) GetAccountByURI(_ context.Context, uri string) (db.Account, error) {
	if acc, ok := s.accountsByURI[uri]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubStore) CreateAccount(_ context.Context, arg db.CreateAccountParams) (db.Account, error) {
	s.nextID++
	acc := db.Account{
		ID:           s.nextID,
		Username:     arg.Username,
		Domain:       arg.Domain,
		PrivateKey:   arg.PrivateKey,
		PublicKey:    arg.PublicKey,
		DisplayName:  arg.DisplayName,
		Uri:          arg.Uri,
		Url:          arg.Url,
		Locked:       arg.Locked,
		Discoverable: arg.Discoverable,
		Indexable:    arg.Indexable,
		ActorType:    arg.ActorType,
		Protocol:     arg.Protocol,
	}
	if s.accounts == nil {
		s.accounts = map[string]db.Account{}
	}
	if s.accountsByURI == nil {
		s.accountsByURI = map[string]db.Account{}
	}
	s.accounts[strings.ToLower(acc.Username)] = acc
	if acc.Uri != "" {
		s.accountsByURI[acc.Uri] = acc
	}
	return acc, nil
}

func (s *stubStore) UpdateAccount(_ context.Context, arg db.UpdateAccountParams) (db.Account, error) {
	for key, account := range s.accounts {
		if account.ID != arg.ID {
			continue
		}
		if arg.Uri.Valid {
			account.Uri = arg.Uri.String
		}
		if arg.Url.Valid {
			account.Url = arg.Url
		}
		if arg.ActorType.Valid {
			account.ActorType = arg.ActorType
		}
		if arg.Locked.Valid {
			account.Locked = arg.Locked.Bool
		}
		if arg.Discoverable.Valid {
			account.Discoverable = arg.Discoverable
		}
		if arg.Indexable.Valid {
			account.Indexable = arg.Indexable.Bool
		}
		if arg.InboxUrl.Valid {
			account.InboxUrl = arg.InboxUrl.String
		}
		if arg.OutboxUrl.Valid {
			account.OutboxUrl = arg.OutboxUrl.String
		}
		if arg.SharedInboxUrl.Valid {
			account.SharedInboxUrl = arg.SharedInboxUrl.String
		}
		if arg.PrivateKey.Valid {
			account.PrivateKey = arg.PrivateKey
		}
		if arg.PublicKey.Valid {
			account.PublicKey = arg.PublicKey.String
		}
		s.accounts[key] = account
		if account.Uri != "" {
			if s.accountsByURI == nil {
				s.accountsByURI = map[string]db.Account{}
			}
			s.accountsByURI[account.Uri] = account
		}
		return account, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func TestHandler_Show_AcctSuccess(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{
			"alice": {
				ID:       10,
				Username: "alice",
				Uri:      "https://social.example/users/alice",
			},
		},
	}, &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	})

	rec := performRequest(t, h, "acct:alice@social.example")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertSuccessHeaders(t, rec)

	body := decodeBody(t, rec)
	if got := body["subject"].(string); got != "acct:alice@social.example" {
		t.Fatalf("subject = %q, want %q", got, "acct:alice@social.example")
	}
}

func TestHandler_Show_WebDomainResourceSuccessWithFederationSubject(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{
			"alice": {
				ID:                11,
				Username:          "alice",
				Uri:               "https://social.example/users/alice",
				AvatarContentType: pgtype.Text{String: "image/jpeg", Valid: true},
				AvatarRemoteUrl:   pgtype.Text{String: "/accounts/avatars/alice.jpg", Valid: true},
			},
		},
	}, &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
			WebDomain:   "web.example",
		},
	})

	rec := performRequest(t, h, "alice@web.example")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertSuccessHeaders(t, rec)

	body := decodeBody(t, rec)
	if got := body["subject"].(string); got != "acct:alice@social.example" {
		t.Fatalf("subject = %q, want federation-domain subject", got)
	}

	aliases := asStringSlice(t, body["aliases"])
	if len(aliases) != 2 {
		t.Fatalf("aliases length = %d, want 2", len(aliases))
	}
	if aliases[0] != "https://web.example/@alice" {
		t.Fatalf("aliases[0] = %q, want %q", aliases[0], "https://web.example/@alice")
	}
	if aliases[1] != "https://social.example/users/alice" {
		t.Fatalf("aliases[1] = %q, want %q", aliases[1], "https://social.example/users/alice")
	}

	links := asLinks(t, body["links"])
	assertLink(t, links, webFingerProfilePageRel, "text/html", "https://web.example/@alice", "")
	assertLink(t, links, "self", activityPubContentType, "https://social.example/users/alice", "")
	assertLink(t, links, webFingerSubscribeRel, "", "", "https://web.example/authorize_interaction?uri={uri}")
	assertLink(t, links, webFingerAvatarRel, "image/jpeg", "https://web.example/accounts/avatars/alice.jpg", "")
}

func TestHandler_Show_URLResourceSuccess(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 12, Username: "alice", Uri: "https://social.example/users/alice"},
		},
	}, &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
			WebDomain:   "web.example",
		},
	})

	rec := performRequest(t, h, "https://web.example/users/alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertSuccessHeaders(t, rec)
}

func TestHandler_Show_MissingResource(t *testing.T) {
	h := NewHandler(&stubStore{}, &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example"}})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger", nil)
	rec := httptest.NewRecorder()
	h.Show(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertErrorHeaders(t, rec)
}

func TestHandler_Show_NonsenseResource(t *testing.T) {
	h := NewHandler(&stubStore{}, &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example"}})
	rec := performRequest(t, h, "df/:dfkj")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertErrorHeaders(t, rec)
}

func TestHandler_Show_UnknownAccount(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{},
	}, &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example"}})
	rec := performRequest(t, h, "acct:ghost@social.example")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	assertErrorHeaders(t, rec)
}

func TestHandler_Show_SuspendedWithDeletionRequestReturnsSuccess(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{
			"alice": {
				ID:          13,
				Username:    "alice",
				Uri:         "https://social.example/users/alice",
				SuspendedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
		deletionRequests: map[int64]bool{13: true},
	}, &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example"}})

	rec := performRequest(t, h, "acct:alice@social.example")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertSuccessHeaders(t, rec)
}

func TestHandler_Show_SuspendedWithoutDeletionRequestReturnsGone(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{
			"alice": {
				ID:          14,
				Username:    "alice",
				SuspendedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
		deletionRequests: map[int64]bool{14: false},
	}, &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example"}})

	rec := performRequest(t, h, "acct:alice@social.example")
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGone)
	}
	assertErrorHeaders(t, rec)
}

func TestHandler_Show_InstanceActorResource(t *testing.T) {
	h := NewHandler(&stubStore{
		accounts: map[string]db.Account{
			"social.example": {
				ID:       15,
				Username: "social.example",
				Uri:      "https://social.example/actor",
			},
		},
	}, &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
			WebDomain:   "web.example",
		},
	})

	rec := performRequest(t, h, "https://web.example/actor")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertSuccessHeaders(t, rec)

	body := decodeBody(t, rec)
	if got := body["subject"].(string); got != "acct:social.example@social.example" {
		t.Fatalf("subject = %q, want instance actor subject", got)
	}

	aliases := asStringSlice(t, body["aliases"])
	if len(aliases) != 1 || aliases[0] != "https://social.example/actor" {
		t.Fatalf("aliases = %#v, want [https://social.example/actor]", aliases)
	}

	links := asLinks(t, body["links"])
	assertLink(t, links, webFingerProfilePageRel, "text/html", "https://web.example/about/more?instance_actor=true", "")
	assertLink(t, links, "self", activityPubContentType, "https://social.example/actor", "")
}

func TestHandler_Show_InstanceActorBootstrapWhenMissing(t *testing.T) {
	store := &stubStore{
		accounts:      map[string]db.Account{},
		accountsByURI: map[string]db.Account{},
		nextID:        90,
	}
	h := NewHandler(store, &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
			WebDomain:   "web.example",
		},
	})

	rec := performRequest(t, h, "https://web.example/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertSuccessHeaders(t, rec)

	if _, ok := store.accounts["social.example"]; !ok {
		t.Fatalf("expected instance actor account to be created")
	}
	if _, ok := store.accountsByURI["https://social.example/actor"]; !ok {
		t.Fatalf("expected instance actor account to be indexed by actor URI")
	}
}

func performRequest(t *testing.T, h *Handler, resource string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger?resource="+url.QueryEscape(resource), nil)
	rec := httptest.NewRecorder()
	h.Show(rec, req)
	return rec
}

func assertSuccessHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, webFingerContentType) {
		t.Fatalf("Content-Type = %q, want prefix %q", got, webFingerContentType)
	}
	if got := rec.Header().Get("Cache-Control"); got != webFingerSuccessCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, webFingerSuccessCacheControl)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want %q", got, "Origin")
	}
}

func assertErrorHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != webFingerErrorCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, webFingerErrorCacheControl)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want %q", got, "Origin")
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return body
}

func asStringSlice(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("value %#v is not []any", v)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("item %#v is not string", item)
		}
		out = append(out, s)
	}
	return out
}

func asLinks(t *testing.T, v any) []map[string]any {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("links %#v is not []any", v)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("link %#v is not object", item)
		}
		out = append(out, m)
	}
	return out
}

func assertLink(t *testing.T, links []map[string]any, rel, typ, href, template string) {
	t.Helper()
	for _, l := range links {
		if relValue, _ := l["rel"].(string); relValue == rel {
			if typ != "" {
				if got, _ := l["type"].(string); got != typ {
					t.Fatalf("link rel=%q type = %q, want %q", rel, got, typ)
				}
			}
			if href != "" {
				if got, _ := l["href"].(string); got != href {
					t.Fatalf("link rel=%q href = %q, want %q", rel, got, href)
				}
			}
			if template != "" {
				if got, _ := l["template"].(string); got != template {
					t.Fatalf("link rel=%q template = %q, want %q", rel, got, template)
				}
			}
			return
		}
	}
	t.Fatalf("link with rel=%q not found", rel)
}
