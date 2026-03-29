package authorizeinteraction

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubStore struct {
	accountsByURI             map[string]db.Account
	statusesByURI             map[string]db.Status
	statusesByURL             map[string]db.Status
	accountsByID              map[int64]db.Account
	statusesByID              map[int64]db.Status
	accountsByUsername        map[string]db.Account
	accountsByUsernameDomain  map[string]db.Account
	getAccountByUsernameCalls []string
}

func (s *stubStore) GetAccountByURI(_ context.Context, uri string) (db.Account, error) {
	if acc, ok := s.accountsByURI[uri]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubStore) GetStatusByURI(_ context.Context, uri pgtype.Text) (db.Status, error) {
	if !uri.Valid {
		return db.Status{}, pgx.ErrNoRows
	}
	if status, ok := s.statusesByURI[uri.String]; ok {
		return status, nil
	}
	return db.Status{}, pgx.ErrNoRows
}

func (s *stubStore) GetStatusByURL(_ context.Context, uri pgtype.Text) (db.Status, error) {
	if !uri.Valid {
		return db.Status{}, pgx.ErrNoRows
	}
	if status, ok := s.statusesByURL[uri.String]; ok {
		return status, nil
	}
	return db.Status{}, pgx.ErrNoRows
}

func (s *stubStore) GetAccountByID(_ context.Context, id int64) (db.Account, error) {
	if acc, ok := s.accountsByID[id]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubStore) GetStatusByID(_ context.Context, id int64) (db.Status, error) {
	if status, ok := s.statusesByID[id]; ok {
		return status, nil
	}
	return db.Status{}, pgx.ErrNoRows
}

func (s *stubStore) GetAccountByUsername(_ context.Context, username string) (db.Account, error) {
	s.getAccountByUsernameCalls = append(s.getAccountByUsernameCalls, username)
	if acc, ok := s.accountsByUsername[username]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *stubStore) GetAccountByUsernameAndDomain(_ context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error) {
	key := arg.Username + "@" + arg.Domain
	if acc, ok := s.accountsByUsernameDomain[key]; ok {
		return acc, nil
	}
	return db.Account{}, pgx.ErrNoRows
}

type stubResolver struct {
	accounts map[string]db.Account
	calls    []string
}

func (s *stubResolver) ResolveAccount(_ context.Context, acctOrActorURI string) (db.Account, error) {
	s.calls = append(s.calls, acctOrActorURI)
	if account, ok := s.accounts[acctOrActorURI]; ok {
		return account, nil
	}
	return db.Account{}, errors.New("not found")
}

type stubFetcher struct {
	statuses map[string]db.Status
	calls    []string
}

func (s *stubFetcher) FetchStatus(_ context.Context, uri string) (db.Status, error) {
	s.calls = append(s.calls, uri)
	if status, ok := s.statuses[uri]; ok {
		return status, nil
	}
	return db.Status{}, errors.New("not found")
}

func TestShowMissingParamReturns404(t *testing.T) {
	h := NewHandler(&stubStore{}, &config.Config{}, &stubResolver{}, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestShowAcctLikeLocalAccountRedirectsToShortProfile(t *testing.T) {
	store := &stubStore{
		accountsByUsername: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
	}
	resolver := &stubResolver{}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	h := NewHandler(store, cfg, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?acct=acct:alice@local.test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://web.test/@alice" {
		t.Fatalf("Location = %q, want %q", loc, "https://web.test/@alice")
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none", resolver.calls)
	}
}

func TestShowAcctLikeWebDomainLocalAccountNormalizesToLocalDomain(t *testing.T) {
	store := &stubStore{
		accountsByUsername: map[string]db.Account{
			"alice": {ID: 1, Username: "alice"},
		},
	}
	resolver := &stubResolver{}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	h := NewHandler(store, cfg, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=alice@web.test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://web.test/@alice" {
		t.Fatalf("Location = %q, want %q", loc, "https://web.test/@alice")
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none", resolver.calls)
	}
}

func TestShowAcctLikeRemoteAccountRedirectsCanonicalURL(t *testing.T) {
	resolver := &stubResolver{accounts: map[string]db.Account{
		"bob@remote.test": {
			ID:       9,
			Username: "bob",
			Domain:   pgtype.Text{String: "remote.test", Valid: true},
			Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=bob@remote.test", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(123)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob")
	}
}

func TestShowRemoteProfileURLFallsBackToAcctResolution(t *testing.T) {
	resolver := &stubResolver{accounts: map[string]db.Account{
		"bob@remote.test": {
			ID:       9,
			Username: "bob",
			Domain:   pgtype.Text{String: "remote.test", Valid: true},
			Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/@bob", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(123)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob")
	}
	if got, want := resolver.calls, []string{"https://remote.test/@bob", "bob@remote.test"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("resolver calls = %v, want %v", got, want)
	}
}

func TestShowLocalPublicStatusURLRedirectsToLocalShortStatus(t *testing.T) {
	store := &stubStore{
		statusesByID: map[int64]db.Status{
			42: {ID: 42, AccountID: 1, Visibility: visibilityPublic, Local: pgtype.Bool{Bool: true, Valid: true}},
		},
		accountsByID: map[int64]db.Account{
			1: {ID: 1, Username: "alice"},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	h := NewHandler(store, cfg, &stubResolver{}, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://web.test/@alice/42", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://web.test/@alice/42" {
		t.Fatalf("Location = %q, want %q", loc, "https://web.test/@alice/42")
	}
}

func TestShowLocalShortStatusURLDoesNotMatchRemoteSameUsername(t *testing.T) {
	store := &stubStore{
		statusesByID: map[int64]db.Status{
			42: {ID: 42, AccountID: 2, Visibility: visibilityPublic, Local: pgtype.Bool{Bool: true, Valid: true}},
		},
		accountsByID: map[int64]db.Account{
			2: {
				ID:       2,
				Username: "alice",
				Domain:   pgtype.Text{String: "other.example", Valid: true},
			},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	h := NewHandler(store, cfg, &stubResolver{}, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://web.test/@alice/42", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestShowRemotePublicStatusURLRedirectsCanonicalURL(t *testing.T) {
	fetcher := &stubFetcher{statuses: map[string]db.Status{
		"https://remote.test/users/bob/statuses/99": {
			ID:         99,
			Visibility: visibilityUnlisted,
			Local:      pgtype.Bool{Bool: false, Valid: true},
			Url:        pgtype.Text{String: "https://remote.test/@bob/99", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, &stubResolver{}, fetcher)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/users/bob/statuses/99", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(123)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob/99" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob/99")
	}
}

func TestShowUnauthenticatedRemoteStatusMissDoesNotFetchAndReturns404(t *testing.T) {
	fetcher := &stubFetcher{statuses: map[string]db.Status{
		"https://remote.test/users/bob/statuses/99": {
			ID:         99,
			Visibility: visibilityUnlisted,
			Local:      pgtype.Bool{Bool: false, Valid: true},
			Url:        pgtype.Text{String: "https://remote.test/@bob/99", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, &stubResolver{}, fetcher)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/users/bob/statuses/99", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("fetcher calls = %v, want none", fetcher.calls)
	}
}

func TestShowUnauthenticatedRemoteAcctProfileMissDoesNotResolveNetworkAndReturns404(t *testing.T) {
	resolver := &stubResolver{accounts: map[string]db.Account{
		"bob@remote.test": {
			ID:       9,
			Username: "bob",
			Domain:   pgtype.Text{String: "remote.test", Valid: true},
			Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/@bob", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none", resolver.calls)
	}
}

func TestShowUnauthenticatedCachedRemoteStatusStillRedirects(t *testing.T) {
	store := &stubStore{
		statusesByURI: map[string]db.Status{
			"https://remote.test/users/bob/statuses/99": {
				ID:         99,
				Visibility: visibilityUnlisted,
				Local:      pgtype.Bool{Bool: false, Valid: true},
				Url:        pgtype.Text{String: "https://remote.test/@bob/99", Valid: true},
			},
		},
	}
	fetcher := &stubFetcher{}
	h := NewHandler(store, &config.Config{}, &stubResolver{}, fetcher)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/users/bob/statuses/99", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob/99" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob/99")
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("fetcher calls = %v, want none", fetcher.calls)
	}
}

func TestShowUnauthenticatedCachedRemoteStatusByCanonicalURLStillRedirects(t *testing.T) {
	store := &stubStore{
		statusesByURL: map[string]db.Status{
			"https://remote.test/@bob/99": {
				ID:         99,
				Visibility: visibilityUnlisted,
				Local:      pgtype.Bool{Bool: false, Valid: true},
				Uri:        pgtype.Text{String: "https://remote.test/users/bob/statuses/99", Valid: true},
				Url:        pgtype.Text{String: "https://remote.test/@bob/99", Valid: true},
			},
		},
	}
	fetcher := &stubFetcher{}
	h := NewHandler(store, &config.Config{}, &stubResolver{}, fetcher)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/@bob/99", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob/99" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob/99")
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("fetcher calls = %v, want none", fetcher.calls)
	}
}

func TestShowUnauthenticatedCachedRemoteAccountStillRedirects(t *testing.T) {
	store := &stubStore{
		accountsByUsernameDomain: map[string]db.Account{
			"bob@remote.test": {
				ID:       9,
				Username: "bob",
				Domain:   pgtype.Text{String: "remote.test", Valid: true},
				Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
			},
		},
	}
	resolver := &stubResolver{accounts: map[string]db.Account{
		"bob@remote.test": {
			ID:       9,
			Username: "bob",
			Domain:   pgtype.Text{String: "remote.test", Valid: true},
			Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
		},
	}}
	h := NewHandler(store, &config.Config{}, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=bob@remote.test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob")
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none", resolver.calls)
	}
}

func TestShowAuthenticatedRemoteStatusResolutionStillWorks(t *testing.T) {
	fetcher := &stubFetcher{statuses: map[string]db.Status{
		"https://remote.test/users/bob/statuses/99": {
			ID:         99,
			Visibility: visibilityUnlisted,
			Local:      pgtype.Bool{Bool: false, Valid: true},
			Url:        pgtype.Text{String: "https://remote.test/@bob/99", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, &stubResolver{}, fetcher)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/users/bob/statuses/99", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(123)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob/99" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob/99")
	}
	if len(fetcher.calls) != 1 || fetcher.calls[0] != "https://remote.test/users/bob/statuses/99" {
		t.Fatalf("fetcher calls = %v, want [%q]", fetcher.calls, "https://remote.test/users/bob/statuses/99")
	}
}

func TestShowAuthenticatedRemoteAccountResolutionStillWorks(t *testing.T) {
	resolver := &stubResolver{accounts: map[string]db.Account{
		"bob@remote.test": {
			ID:       9,
			Username: "bob",
			Domain:   pgtype.Text{String: "remote.test", Valid: true},
			Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=bob@remote.test", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(123)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "https://remote.test/@bob" {
		t.Fatalf("Location = %q, want %q", loc, "https://remote.test/@bob")
	}
	if got, want := resolver.calls, []string{"bob@remote.test"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("resolver calls = %v, want %v", got, want)
	}
}

func TestShowAppOnlyTokenContextDoesNotResolveRemoteNetwork(t *testing.T) {
	resolver := &stubResolver{accounts: map[string]db.Account{
		"bob@remote.test": {
			ID:       9,
			Username: "bob",
			Domain:   pgtype.Text{String: "remote.test", Valid: true},
			Url:      pgtype.Text{String: "https://remote.test/@bob", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, resolver, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=bob@remote.test", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyTokenID, int64(123)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none", resolver.calls)
	}
}

func TestShowNonPublicStatusReturns404(t *testing.T) {
	fetcher := &stubFetcher{statuses: map[string]db.Status{
		"https://remote.test/users/bob/statuses/100": {
			ID:         100,
			Visibility: 2,
			Local:      pgtype.Bool{Bool: false, Valid: true},
			Uri:        pgtype.Text{String: "https://remote.test/users/bob/statuses/100", Valid: true},
		},
	}}
	h := NewHandler(&stubStore{}, &config.Config{}, &stubResolver{}, fetcher)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://remote.test/users/bob/statuses/100", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestShowInvalidOrUnresolvableReturns404(t *testing.T) {
	h := NewHandler(&stubStore{}, &config.Config{}, &stubResolver{}, &stubFetcher{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/authorize_interaction?uri=https://unknown.test/resource", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
