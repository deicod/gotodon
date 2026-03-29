package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type stubFamiliarFollowersQueries struct {
	db.Querier
	accountsByID          map[int64]db.Account
	familiarByRequesterID map[int64]map[int64][]db.Account
	getFollowersCalled    bool
	getFollowsCalled      bool
}

func (s *stubFamiliarFollowersQueries) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	out := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if acc, ok := s.accountsByID[id]; ok {
			out = append(out, acc)
		}
	}
	return out, nil
}

func (s *stubFamiliarFollowersQueries) GetFamiliarFollowers(ctx context.Context, arg db.GetFamiliarFollowersParams) ([]db.Account, error) {
	byTarget := s.familiarByRequesterID[arg.AccountID]
	if byTarget == nil {
		return []db.Account{}, nil
	}
	accs := byTarget[arg.TargetAccountID]
	out := make([]db.Account, len(accs))
	copy(out, accs)
	return out, nil
}

// Regression guard: familiar followers must not rely on legacy "load all followers and filter in Go".
func (s *stubFamiliarFollowersQueries) GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error) {
	s.getFollowersCalled = true
	return []db.Account{}, nil
}

// Regression guard: familiar followers must not rely on post-query follow intersection in Go.
func (s *stubFamiliarFollowersQueries) GetFollowsByAccountIDs(ctx context.Context, arg db.GetFollowsByAccountIDsParams) ([]int64, error) {
	s.getFollowsCalled = true
	return []int64{}, nil
}

func (s *stubFamiliarFollowersQueries) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *stubFamiliarFollowersQueries) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubFamiliarFollowersQueries) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubFamiliarFollowersQueries) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *stubFamiliarFollowersQueries) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *stubFamiliarFollowersQueries) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func TestAccountsHandler_FamiliarFollowers_DedupesRequestedIDs(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
			20: {ID: 20, Username: "requested-20"},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?id=10&id=20&id=20&id=10", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "10" || got[1].ID != "20" {
		t.Fatalf("ids = [%s %s], want [10 20]", got[0].ID, got[1].ID)
	}
}

func TestAccountsHandler_FamiliarFollowers_MixedAliasesPreserveRequestOrder(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
			20: {ID: 20, Username: "requested-20"},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?account_id=20&id=10&id=20", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "20" || got[1].ID != "10" {
		t.Fatalf("ids = [%s %s], want [20 10]", got[0].ID, got[1].ID)
	}
}

func TestAccountsHandler_FamiliarFollowers_ReturnsOneFamiliarFollower(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{
			1: {
				10: {
					{ID: 50, Username: "familiar-50"},
				},
			},
		},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?id=10", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "10" {
		t.Fatalf("id = %s, want 10", got[0].ID)
	}
	if len(got[0].Accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(got[0].Accounts))
	}
	if got[0].Accounts[0].ID != "50" {
		t.Fatalf("familiar follower id = %s, want 50", got[0].Accounts[0].ID)
	}
}

func TestAccountsHandler_FamiliarFollowers_RequestedHideCollectionsReturnsEmpty(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {
				ID:              10,
				Username:        "requested-10",
				HideCollections: pgtype.Bool{Bool: true, Valid: true},
			},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{
			1: {
				10: {
					{ID: 50, Username: "familiar-50"},
				},
			},
		},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?id=10", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Accounts) != 0 {
		t.Fatalf("accounts len = %d, want 0", len(got[0].Accounts))
	}
}

func TestAccountsHandler_FamiliarFollowers_ExcludesHiddenFamiliarFollower(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{
			1: {
				10: {},
			},
		},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?id=10", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Accounts) != 0 {
		t.Fatalf("accounts len = %d, want 0", len(got[0].Accounts))
	}
}

func TestAccountsHandler_FamiliarFollowers_AcceptsAccountIDParam(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?account_id=10", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "10" {
		t.Fatalf("id = %s, want 10", got[0].ID)
	}
}

func TestAccountsHandler_FamiliarFollowers_OmitsSuspendedRequestedAccount(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
			20: {
				ID:          20,
				Username:    "requested-20",
				SuspendedAt: pgtype.Timestamptz{Valid: true},
			},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{
			1: {
				10: {
					{ID: 50, Username: "familiar-50"},
				},
				20: {
					{ID: 60, Username: "familiar-60"},
				},
			},
		},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?id=10&id=20", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []familiarFollowersResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "10" {
		t.Fatalf("id = %s, want 10", got[0].ID)
	}
}

func TestAccountsHandler_FamiliarFollowers_UsesIntersectionQuery(t *testing.T) {
	stub := &stubFamiliarFollowersQueries{
		accountsByID: map[int64]db.Account{
			10: {ID: 10, Username: "requested-10"},
		},
		familiarByRequesterID: map[int64]map[int64][]db.Account{
			1: {
				10: {
					{ID: 50, Username: "familiar-50"},
				},
			},
		},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)

	req, rec := mockAccountsRequest(http.MethodGet, "/api/v1/accounts/familiar_followers?id=10", "", 1)
	h.FamiliarFollowers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if stub.getFollowersCalled {
		t.Fatalf("GetFollowers should not be called")
	}
	if stub.getFollowsCalled {
		t.Fatalf("GetFollowsByAccountIDs should not be called")
	}
}
