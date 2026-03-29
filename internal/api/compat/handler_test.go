package compat

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	accountsByID     map[int64]db.Account
	statusesByID     map[int64]db.Status
	deletionRequests map[int64]bool
}

func (s *stubStore) GetLocalAccountByUsernameAllowSuspended(_ context.Context, username string) (db.Account, error) {
	acc, ok := s.accounts[strings.ToLower(username)]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return acc, nil
}

func (s *stubStore) HasAccountDeletionRequest(_ context.Context, accountID int64) (bool, error) {
	return s.deletionRequests[accountID], nil
}

func (s *stubStore) GetStatusByID(_ context.Context, id int64) (db.Status, error) {
	status, ok := s.statusesByID[id]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	return status, nil
}

func (s *stubStore) GetAccountByID(_ context.Context, id int64) (db.Account, error) {
	account, ok := s.accountsByID[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

type stubActivityPubRenderer struct {
	statusCalled bool
	actorCalled  bool
	username    string
	statusID    string
	accept      string
	statusToSet int
	actorToSet  int
}

func (s *stubActivityPubRenderer) GetStatus(w http.ResponseWriter, r *http.Request) {
	s.statusCalled = true
	s.username = r.PathValue("username")
	s.statusID = r.PathValue("statusID")
	s.accept = r.Header.Get("Accept")
	if s.statusToSet == 0 {
		s.statusToSet = http.StatusOK
	}
	w.WriteHeader(s.statusToSet)
}

func (s *stubActivityPubRenderer) GetActor(w http.ResponseWriter, r *http.Request) {
	s.actorCalled = true
	s.username = r.PathValue("username")
	s.accept = r.Header.Get("Accept")
	if s.actorToSet == 0 {
		s.actorToSet = http.StatusOK
	}
	w.WriteHeader(s.actorToSet)
}

func TestShowAccountNotMatchingSegmentReturns404(t *testing.T) {
	mux := http.NewServeMux()
	handler := NewHandler(&stubStore{}, &config.Config{}, nil)
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestShowAccountSuspendedSemantics(t *testing.T) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	store := &stubStore{
		accounts: map[string]db.Account{
			"blocked": {ID: 10, Username: "blocked", SuspendedAt: now},
			"gone":    {ID: 11, Username: "gone", SuspendedAt: now},
		},
		deletionRequests: map[int64]bool{
			10: true,
			11: false,
		},
	}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, nil).RegisterRoutes(mux)

	blockedReq := httptest.NewRequest(http.MethodGet, "/@blocked", nil)
	blockedRec := httptest.NewRecorder()
	mux.ServeHTTP(blockedRec, blockedReq)
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("blocked status = %d, want %d", blockedRec.Code, http.StatusForbidden)
	}

	goneReq := httptest.NewRequest(http.MethodGet, "/@gone", nil)
	goneRec := httptest.NewRecorder()
	mux.ServeHTTP(goneRec, goneReq)
	if goneRec.Code != http.StatusGone {
		t.Fatalf("gone status = %d, want %d", goneRec.Code, http.StatusGone)
	}
}

func TestShowAccountSuspendedJSONDelegatesToActivityPub(t *testing.T) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	store := &stubStore{
		accounts: map[string]db.Account{
			"gone": {ID: 11, Username: "gone", SuspendedAt: now},
		},
	}
	renderer := &stubActivityPubRenderer{actorToSet: http.StatusNotFound}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, renderer).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@gone.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !renderer.actorCalled {
		t.Fatalf("expected activitypub renderer to be called")
	}
	if renderer.username != "gone" {
		t.Fatalf("username = %q, want %q", renderer.username, "gone")
	}
}

func TestShowAccountSuspendedAcceptDelegatesToActivityPub(t *testing.T) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	store := &stubStore{
		accounts: map[string]db.Account{
			"blocked": {ID: 10, Username: "blocked", SuspendedAt: now},
		},
	}
	renderer := &stubActivityPubRenderer{actorToSet: http.StatusNotFound}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, renderer).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@blocked", nil)
	req.Header.Set("Accept", "application/activity+json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !renderer.actorCalled {
		t.Fatalf("expected activitypub renderer to be called")
	}
	if renderer.username != "blocked" {
		t.Fatalf("username = %q, want %q", renderer.username, "blocked")
	}
	if got := rec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
}

func TestShowAccountRendersOGTagsAndFavicon(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {
				ID:             1,
				Username:       "alice",
				DisplayName:    "Alice",
				AvatarFileName: pgtype.Text{String: "avatars/alice.png", Valid: true},
			},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{
		LocalDomain: "social.example",
		WebDomain:   "web.example",
	}}

	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	assertContains(t, body, `<link rel="icon" href="https://web.example/icons/android-chrome-192x192.png">`)
	assertContains(t, body, `<meta property="og:title" content="Alice (@alice)">`)
	assertContains(t, body, `<meta property="og:type" content="profile">`)
	assertContains(t, body, `<meta property="og:image" content="https://web.example/files/avatars/alice.png">`)
	assertContains(t, body, `<meta property="og:url" content="https://web.example/@alice">`)
}

func TestShowStatusRendersPublicWithOGTags(t *testing.T) {
	created := pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice", DisplayName: "Alice"},
		},
		statusesByID: map[int64]db.Status{
			42: {
				ID:         42,
				AccountID:  1,
				Visibility: visibilityPublic,
				Text:       "hello fediverse from gotodon",
				CreatedAt:  created,
			},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example", WebDomain: "web.example"}}

	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/42", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	assertContains(t, body, `<meta property="og:type" content="article">`)
	assertContains(t, body, `<meta property="og:url" content="https://web.example/@alice/42">`)
	assertContains(t, body, `<meta property="og:published_time" content="2026-01-02T03:04:05Z">`)
	assertContains(t, body, `<meta property="og:description" content="hello fediverse from gotodon">`)
	assertContains(t, body, `<p>hello fediverse from gotodon</p>`)
}

func TestShowStatusWithCWUsesSpoilerTextForShortHTML(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice", DisplayName: "Alice"},
		},
		statusesByID: map[int64]db.Status{
			42: {
				ID:          42,
				AccountID:   1,
				Visibility:  visibilityPublic,
				Text:        "sensitive body should not leak",
				SpoilerText: "cw summary only",
			},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{WebDomain: "web.example"}}
	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/42", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	assertContains(t, body, `<title>Alice: cw summary only</title>`)
	assertContains(t, body, `<meta property="og:description" content="cw summary only">`)
	assertContains(t, body, `<p>cw summary only</p>`)
	if strings.Contains(body, "sensitive body should not leak") {
		t.Fatalf("body exposed status text for CW post: %s", body)
	}
}

func TestShowStatusPrivateAndDirectReturn404(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
		statusesByID: map[int64]db.Status{
			10: {ID: 10, AccountID: 1, Visibility: 2, Text: "private"},
			11: {ID: 11, AccountID: 1, Visibility: 3, Text: "direct"},
		},
	}

	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, nil).RegisterRoutes(mux)

	for _, path := range []string{"/@alice/10", "/@alice/11"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %s status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestShowStatusReblogRedirectsToOriginal(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
			"bob":   {ID: 2, Username: "bob"},
		},
		accountsByID: map[int64]db.Account{
			2: {ID: 2, Username: "bob"},
		},
		statusesByID: map[int64]db.Status{
			100: {ID: 100, AccountID: 1, ReblogOfID: pgtype.Int8{Int64: 200, Valid: true}},
			200: {ID: 200, AccountID: 2, Url: pgtype.Text{String: "https://remote.example/@bob/200", Valid: true}},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example", WebDomain: "web.example"}}

	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "https://remote.example/@bob/200" {
		t.Fatalf("Location = %q, want %q", got, "https://remote.example/@bob/200")
	}
	if got := rec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
}

func TestShowStatusPrivateReblogDoesNotRedirectAndReturns404(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
			"bob":   {ID: 2, Username: "bob"},
		},
		accountsByID: map[int64]db.Account{
			2: {ID: 2, Username: "bob"},
		},
		statusesByID: map[int64]db.Status{
			100: {ID: 100, AccountID: 1, Visibility: 2, ReblogOfID: pgtype.Int8{Int64: 200, Valid: true}},
			200: {ID: 200, AccountID: 2, Url: pgtype.Text{String: "https://remote.example/@bob/200", Valid: true}},
		},
	}

	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want empty", got)
	}
}

func TestShowStatusReblogWithoutURLOrURIReturns404WithoutRedirect(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
			"bob":   {ID: 2, Username: "bob", Domain: pgtype.Text{String: "remote.example", Valid: true}},
		},
		accountsByID: map[int64]db.Account{
			2: {ID: 2, Username: "bob", Domain: pgtype.Text{String: "remote.example", Valid: true}},
		},
		statusesByID: map[int64]db.Status{
			100: {ID: 100, AccountID: 1, Visibility: visibilityPublic, ReblogOfID: pgtype.Int8{Int64: 200, Valid: true}},
			200: {ID: 200, AccountID: 2},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example", WebDomain: "web.example"}}

	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want empty", got)
	}
}

func TestShowStatusReblogMissingOriginalReturns404WithoutRedirect(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
		statusesByID: map[int64]db.Status{
			100: {ID: 100, AccountID: 1, Visibility: visibilityPublic, ReblogOfID: pgtype.Int8{Int64: 200, Valid: true}},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{WebDomain: "web.example"}}

	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want empty", got)
	}
}

func TestShowStatusReblogSelfTargetDoesNotRedirectToSameURL(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
		accountsByID: map[int64]db.Account{
			1: {ID: 1, Username: "alice"},
		},
		statusesByID: map[int64]db.Status{
			100: {ID: 100, AccountID: 1, Visibility: visibilityPublic, ReblogOfID: pgtype.Int8{Int64: 100, Valid: true}},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{WebDomain: "web.example"}}

	mux := http.NewServeMux()
	NewHandler(store, cfg, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want empty", got)
	}
}

func TestShowStatusJSONAndAcceptDelegateToActivityPub(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
		statusesByID: map[int64]db.Status{
			7: {ID: 7, AccountID: 1, Visibility: visibilityPublic, Text: "ap me"},
		},
	}
	renderer := &stubActivityPubRenderer{}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, renderer).RegisterRoutes(mux)

	jsonReq := httptest.NewRequest(http.MethodGet, "/@alice/7.json", nil)
	jsonRec := httptest.NewRecorder()
	mux.ServeHTTP(jsonRec, jsonReq)
	if jsonRec.Code != http.StatusOK {
		t.Fatalf("json status = %d, want %d", jsonRec.Code, http.StatusOK)
	}
	if !renderer.statusCalled {
		t.Fatalf("expected activitypub renderer to be called for .json")
	}
	if renderer.username != "alice" || renderer.statusID != "7" {
		t.Fatalf("path values forwarded incorrectly: username=%q statusID=%q", renderer.username, renderer.statusID)
	}

	renderer.statusCalled = false
	acceptReq := httptest.NewRequest(http.MethodGet, "/@alice/7", nil)
	acceptReq.Header.Set("Accept", "application/activity+json")
	acceptRec := httptest.NewRecorder()
	mux.ServeHTTP(acceptRec, acceptReq)
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want %d", acceptRec.Code, http.StatusOK)
	}
	if !renderer.statusCalled {
		t.Fatalf("expected activitypub renderer to be called for Accept negotiation")
	}
	if renderer.accept != "application/activity+json" {
		t.Fatalf("accept = %q, want %q", renderer.accept, "application/activity+json")
	}
	if got := acceptRec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
	if got := jsonRec.Header().Get("Vary"); got != "" {
		t.Fatalf("json Vary = %q, want empty", got)
	}
}

func TestShowStatusSuspendedJSONDelegatesToActivityPub(t *testing.T) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	store := &stubStore{
		accounts: map[string]db.Account{
			"gone": {ID: 11, Username: "gone", SuspendedAt: now},
		},
		statusesByID: map[int64]db.Status{
			7: {ID: 7, AccountID: 11, Visibility: visibilityPublic, Text: "ap me"},
		},
	}
	renderer := &stubActivityPubRenderer{statusToSet: http.StatusNotFound}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, renderer).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@gone/7.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !renderer.statusCalled {
		t.Fatalf("expected activitypub renderer to be called")
	}
	if renderer.username != "gone" || renderer.statusID != "7" {
		t.Fatalf("path values forwarded incorrectly: username=%q statusID=%q", renderer.username, renderer.statusID)
	}
}

func TestShowStatusSuspendedAcceptDelegatesToActivityPub(t *testing.T) {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	store := &stubStore{
		accounts: map[string]db.Account{
			"blocked": {ID: 10, Username: "blocked", SuspendedAt: now},
		},
		statusesByID: map[int64]db.Status{
			7: {ID: 7, AccountID: 10, Visibility: visibilityPublic, Text: "ap me"},
		},
	}
	renderer := &stubActivityPubRenderer{statusToSet: http.StatusNotFound}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, renderer).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@blocked/7", nil)
	req.Header.Set("Accept", "application/activity+json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !renderer.statusCalled {
		t.Fatalf("expected activitypub renderer to be called")
	}
	if renderer.username != "blocked" || renderer.statusID != "7" {
		t.Fatalf("path values forwarded incorrectly: username=%q statusID=%q", renderer.username, renderer.statusID)
	}
	if got := rec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
}

func TestShowStatusHTMLSetsVaryAccept(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
		statusesByID: map[int64]db.Status{
			7: {ID: 7, AccountID: 1, Visibility: visibilityPublic, Text: "hello"},
		},
	}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, &stubActivityPubRenderer{}).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice/7", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
}

func TestAcceptNegotiatedShortRouteErrorsSetVaryAccept(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
	}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, nil).RegisterRoutes(mux)

	accountReq := httptest.NewRequest(http.MethodGet, "/@missing", nil)
	accountRec := httptest.NewRecorder()
	mux.ServeHTTP(accountRec, accountReq)
	if accountRec.Code != http.StatusNotFound {
		t.Fatalf("account status = %d, want %d", accountRec.Code, http.StatusNotFound)
	}
	if got := accountRec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("account Vary = %q, want %q", got, "Accept")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/@alice/999", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", statusRec.Code, http.StatusNotFound)
	}
	if got := statusRec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("status Vary = %q, want %q", got, "Accept")
	}
}

func TestShowAccountJSONAndAcceptDelegateToActivityPubActor(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice", DisplayName: "Alice"},
		},
	}
	renderer := &stubActivityPubRenderer{}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, renderer).RegisterRoutes(mux)

	jsonReq := httptest.NewRequest(http.MethodGet, "/@alice.json", nil)
	jsonRec := httptest.NewRecorder()
	mux.ServeHTTP(jsonRec, jsonReq)
	if jsonRec.Code != http.StatusOK {
		t.Fatalf("json status = %d, want %d", jsonRec.Code, http.StatusOK)
	}
	if !renderer.actorCalled {
		t.Fatalf("expected activitypub renderer to be called for account .json")
	}
	if renderer.username != "alice" {
		t.Fatalf("username = %q, want %q", renderer.username, "alice")
	}
	if got := jsonRec.Header().Get("Vary"); got != "" {
		t.Fatalf("Vary = %q, want empty", got)
	}

	renderer.actorCalled = false
	acceptReq := httptest.NewRequest(http.MethodGet, "/@alice", nil)
	acceptReq.Header.Set("Accept", "application/activity+json")
	acceptRec := httptest.NewRecorder()
	mux.ServeHTTP(acceptRec, acceptReq)
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want %d", acceptRec.Code, http.StatusOK)
	}
	if !renderer.actorCalled {
		t.Fatalf("expected activitypub renderer to be called for account Accept negotiation")
	}
	if renderer.accept != "application/activity+json" {
		t.Fatalf("accept = %q, want %q", renderer.accept, "application/activity+json")
	}
	if got := acceptRec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
}

func TestShowAccountHTMLSetsVaryAccept(t *testing.T) {
	store := &stubStore{
		accounts: map[string]db.Account{
			"alice": {ID: 1, Username: "alice", DisplayName: "Alice"},
		},
	}
	mux := http.NewServeMux()
	NewHandler(store, &config.Config{}, &stubActivityPubRenderer{}).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/@alice", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Vary"); got != "Accept" {
		t.Fatalf("Vary = %q, want %q", got, "Accept")
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("body missing %q\nbody=%s", want, got)
	}
}
