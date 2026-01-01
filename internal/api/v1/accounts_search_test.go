package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubAccountSearchQuerier struct {
	searchArgs    []db.SearchAccountsParams
	followingArgs []db.SearchAccountsFollowingParams

	searchResults    []db.Account
	followingResults []db.Account

	usernameAccount db.Account
	usernameErr     error

	domainAccount db.Account
	domainErr     error

	followErr error
}

func (s *stubAccountSearchQuerier) SearchAccounts(_ context.Context, arg db.SearchAccountsParams) ([]db.Account, error) {
	s.searchArgs = append(s.searchArgs, arg)
	return s.searchResults, nil
}

func (s *stubAccountSearchQuerier) SearchAccountsFollowing(_ context.Context, arg db.SearchAccountsFollowingParams) ([]db.Account, error) {
	s.followingArgs = append(s.followingArgs, arg)
	return s.followingResults, nil
}

func (s *stubAccountSearchQuerier) GetAccountByUsername(_ context.Context, _ string) (db.Account, error) {
	return s.usernameAccount, s.usernameErr
}

func (s *stubAccountSearchQuerier) GetAccountByUsernameAndDomain(_ context.Context, _ db.GetAccountByUsernameAndDomainParams) (db.Account, error) {
	return s.domainAccount, s.domainErr
}

func (s *stubAccountSearchQuerier) GetFollow(_ context.Context, _ db.GetFollowParams) (db.Follow, error) {
	return db.Follow{}, s.followErr
}

type stubAccountSearchResolver struct {
	lastAcct string
	account  db.Account
	err      error
}

func (s *stubAccountSearchResolver) ResolveAccount(_ context.Context, acct string) (db.Account, error) {
	s.lastAcct = acct
	return s.account, s.err
}

func TestAccountsHandler_Search_Unauthorized(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/search?q=alice", "", 0)
	h.Search(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAccountsHandler_Search_EmptyQuery(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/search", "", 123)
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []Account
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty response, got %d items", len(got))
	}
}

func TestAccountSearch_ExactMatchUsesResolverAndDedupes(t *testing.T) {
	stub := &stubAccountSearchQuerier{
		searchResults: []db.Account{
			{ID: 10, Username: "alice"},
			{ID: 20, Username: "bob"},
		},
	}
	resolver := &stubAccountSearchResolver{
		account: db.Account{ID: 10, Username: "alice"},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}}

	results, err := accountSearch(context.Background(), stub, resolver, cfg, 99, "@alice@remote.test", 2, 0, true, false)
	if err != nil {
		t.Fatalf("accountSearch error: %v", err)
	}

	if resolver.lastAcct != "alice@remote.test" {
		t.Fatalf("resolver acct = %q, want %q", resolver.lastAcct, "alice@remote.test")
	}

	if len(stub.searchArgs) != 1 {
		t.Fatalf("SearchAccounts calls = %d, want 1", len(stub.searchArgs))
	}
	if stub.searchArgs[0].Limit != 1 {
		t.Fatalf("SearchAccounts limit = %d, want 1", stub.searchArgs[0].Limit)
	}

	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].ID != 10 || results[1].ID != 20 {
		t.Fatalf("result order = [%d %d], want [10 20]", results[0].ID, results[1].ID)
	}
}

func TestAccountSearch_FollowingSkipsExactMatchWhenNotFollowed(t *testing.T) {
	stub := &stubAccountSearchQuerier{
		usernameAccount: db.Account{ID: 20, Username: "alice"},
		followErr:       pgx.ErrNoRows,
		followingResults: []db.Account{
			{ID: 30, Username: "bob"},
		},
	}
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}}

	results, err := accountSearch(context.Background(), stub, nil, cfg, 1, "alice@local.test", 2, 7, false, true)
	if err != nil {
		t.Fatalf("accountSearch error: %v", err)
	}

	if len(stub.searchArgs) != 0 {
		t.Fatalf("SearchAccounts calls = %d, want 0", len(stub.searchArgs))
	}
	if len(stub.followingArgs) != 1 {
		t.Fatalf("SearchAccountsFollowing calls = %d, want 1", len(stub.followingArgs))
	}
	if stub.followingArgs[0].Query != "alice" {
		t.Fatalf("SearchAccountsFollowing query = %q, want %q", stub.followingArgs[0].Query, "alice")
	}
	if stub.followingArgs[0].Limit != 2 {
		t.Fatalf("SearchAccountsFollowing limit = %d, want 2", stub.followingArgs[0].Limit)
	}
	if stub.followingArgs[0].Offset != 7 {
		t.Fatalf("SearchAccountsFollowing offset = %d, want 7", stub.followingArgs[0].Offset)
	}

	if len(results) != 1 || results[0].ID != 30 {
		t.Fatalf("results = %+v, want account 30 only", results)
	}
}
