package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

type stubVerifyCredentialsQueries struct {
	db.Querier
	account db.Account
}

func (s *stubVerifyCredentialsQueries) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	return s.account, nil
}

func (s *stubVerifyCredentialsQueries) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func (s *stubVerifyCredentialsQueries) GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error) {
	return db.User{}, pgx.ErrNoRows
}

func (s *stubVerifyCredentialsQueries) CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error) {
	return 0, nil
}

func (s *stubVerifyCredentialsQueries) GetUserRole(ctx context.Context, userID int64) (db.UserRole, error) {
	return db.UserRole{}, pgx.ErrNoRows
}

func TestAccountsHandler_VerifyCredentials_UnauthorizedWithoutAccountContext(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAccountsHandler_VerifyCredentials_ForbiddenWithWrongScope(t *testing.T) {
	h := NewAccountsHandler(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write write:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got, want := errResp["error"], "This action is outside the authorized scopes"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAccountsHandler_VerifyCredentials_AllowsReadAccountsScope(t *testing.T) {
	stub := &stubVerifyCredentialsQueries{
		account: db.Account{ID: 12345, Username: "alice"},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_VerifyCredentials_AllowsProfileScope(t *testing.T) {
	stub := &stubVerifyCredentialsQueries{
		account: db.Account{ID: 12345, Username: "alice"},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "profile")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_VerifyCredentials_AllowsReadScopeHierarchy(t *testing.T) {
	stub := &stubVerifyCredentialsQueries{
		account: db.Account{ID: 12345, Username: "alice"},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_VerifyCredentials_AllowsEmptyScopesForCompatibility(t *testing.T) {
	stub := &stubVerifyCredentialsQueries{
		account: db.Account{ID: 12345, Username: "alice"},
	}
	h := NewAccountsHandler(stub, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.VerifyCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
