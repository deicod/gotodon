package v1

import (
	"context"
	"encoding/json"
	"errors"
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
	// user is the account's user. A zero ID means the account has none.
	user    db.User
	userErr error
	// role is the user's role. nil means the user has none.
	role    *db.UserRole
	roleErr error
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
	if s.userErr != nil {
		return db.User{}, s.userErr
	}
	if s.user.ID == 0 || accountID != s.account.ID {
		return db.User{}, pgx.ErrNoRows
	}
	return s.user, nil
}

func (s *stubVerifyCredentialsQueries) CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error) {
	return 0, nil
}

func (s *stubVerifyCredentialsQueries) GetUserRole(ctx context.Context, userID int64) (db.UserRole, error) {
	if s.roleErr != nil {
		return db.UserRole{}, s.roleErr
	}
	if s.role == nil || userID != s.user.ID {
		return db.UserRole{}, pgx.ErrNoRows
	}
	return *s.role, nil
}

// TestAccountsHandler_VerifyCredentials_Role checks the role as Mastodon's
// CredentialAccountSerializer renders it: REST::RoleSerializer with computed
// permissions, and the everyone role for a user without a role.
func TestAccountsHandler_VerifyCredentials_Role(t *testing.T) {
	alice := db.Account{ID: 12345, Username: "alice"}
	user := db.User{ID: 20, AccountID: alice.ID}
	errDatabase := errors.New("database unavailable")

	tests := []struct {
		name    string
		user    db.User
		userErr error
		role    *db.UserRole
		roleErr error
		// wantRole is the role's JSON in a 200 response.
		wantRole   string
		wantStatus int
	}{
		{name: "Owner storing the administrator bit", user: user, role: &ownerRole, wantRole: ownerRoleJSON, wantStatus: http.StatusOK},
		{name: "Owner storing every bit", user: user, role: &everyBitOwnerRole, wantRole: ownerRoleJSON, wantStatus: http.StatusOK},
		{name: "Admin", user: user, role: &adminRole, wantRole: adminRoleJSON, wantStatus: http.StatusOK},
		{name: "Moderator", user: user, role: &moderatorRole, wantRole: moderatorRoleJSON, wantStatus: http.StatusOK},
		{name: "highlighted role", user: user, role: &highlightedRole, wantRole: highlightedRoleJSON, wantStatus: http.StatusOK},
		{name: "user without a role", user: user, wantRole: everyoneRoleJSON, wantStatus: http.StatusOK},
		{name: "account without a user", wantRole: "null", wantStatus: http.StatusOK},
		{name: "user lookup fails", userErr: errDatabase, wantStatus: http.StatusInternalServerError},
		{name: "role lookup fails", user: user, roleErr: errDatabase, wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubVerifyCredentialsQueries{
				account: alice,
				user:    tt.user,
				userErr: tt.userErr,
				role:    tt.role,
				roleErr: tt.roleErr,
			}
			h := NewAccountsHandler(stub, nil, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, alice.ID)
			ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read:accounts")
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()

			h.VerifyCredentials(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}
			if tt.wantStatus != http.StatusOK {
				assertJSONEqual(t, rec.Body.Bytes(), `{"error":"Failed to fetch account"}`)
				return
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			role, ok := body["role"]
			if !ok {
				t.Fatalf("response has no role key: %s", rec.Body.String())
			}
			assertJSONEqual(t, role, tt.wantRole)
		})
	}
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
