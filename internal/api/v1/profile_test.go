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
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func mockProfileRequest(method, path string, accountID int64) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

type stubProfileQueries struct {
	account           db.Account
	clearAvatarCalled bool
	clearHeaderCalled bool
	// role is the role of the account's user, 1. nil means none.
	role    *db.UserRole
	roleErr error
}

func (s *stubProfileQueries) ClearAccountAvatar(ctx context.Context, id int64) (db.Account, error) {
	s.clearAvatarCalled = true
	return s.account, nil
}

func (s *stubProfileQueries) ClearAccountHeader(ctx context.Context, id int64) (db.Account, error) {
	s.clearHeaderCalled = true
	return s.account, nil
}

func (s *stubProfileQueries) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	return s.account, nil
}

func (s *stubProfileQueries) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *stubProfileQueries) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubProfileQueries) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubProfileQueries) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *stubProfileQueries) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	return nil, nil
}

func (s *stubProfileQueries) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *stubProfileQueries) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func (s *stubProfileQueries) GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error) {
	return db.User{ID: 1, AccountID: accountID}, nil
}

func (s *stubProfileQueries) CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error) {
	return 0, nil
}

func (s *stubProfileQueries) GetUserRole(ctx context.Context, userID int64) (db.UserRole, error) {
	if s.roleErr != nil {
		return db.UserRole{}, s.roleErr
	}
	if s.role == nil || userID != 1 {
		return db.UserRole{}, pgx.ErrNoRows
	}
	return *s.role, nil
}

func TestProfileHandler_DeleteAvatar(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		role      *db.UserRole
		roleErr   error
		wantCode  int
		// wantRole is the role's JSON in a 200 response.
		wantRole string
	}{
		{
			name:      "authenticated user can delete avatar",
			accountID: 12345,
			wantCode:  http.StatusOK,
			wantRole:  everyoneRoleJSON,
		},
		{
			name:      "the role has computed permissions",
			accountID: 12345,
			role:      &moderatorRole,
			wantCode:  http.StatusOK,
			wantRole:  moderatorRoleJSON,
		},
		{
			name:      "role lookup fails after the avatar is deleted",
			accountID: 12345,
			roleErr:   errors.New("database unavailable"),
			wantCode:  http.StatusInternalServerError,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubProfileQueries{account: db.Account{ID: 12345, Username: "user"}, role: tt.role, roleErr: tt.roleErr}
			h := NewProfileHandler(stub, &config.Config{})
			req, rec := mockProfileRequest(http.MethodDelete, "/api/v1/profile/avatar", tt.accountID)

			h.DeleteAvatar(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.accountID != 0 && !stub.clearAvatarCalled {
				t.Fatal("expected ClearAccountAvatar to be called")
			}

			switch tt.wantCode {
			case http.StatusOK:
				var body map[string]json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				assertJSONEqual(t, body["role"], tt.wantRole)
			case http.StatusInternalServerError:
				assertJSONEqual(t, rec.Body.Bytes(), `{"error":"Failed to fetch account"}`)
			}
		})
	}
}

func TestProfileHandler_DeleteHeader(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		role      *db.UserRole
		roleErr   error
		wantCode  int
		// wantRole is the role's JSON in a 200 response.
		wantRole string
	}{
		{
			name:      "authenticated user can delete header",
			accountID: 12345,
			wantCode:  http.StatusOK,
			wantRole:  everyoneRoleJSON,
		},
		{
			name:      "the role has computed permissions",
			accountID: 12345,
			role:      &moderatorRole,
			wantCode:  http.StatusOK,
			wantRole:  moderatorRoleJSON,
		},
		{
			name:      "role lookup fails after the header is deleted",
			accountID: 12345,
			roleErr:   errors.New("database unavailable"),
			wantCode:  http.StatusInternalServerError,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubProfileQueries{account: db.Account{ID: 12345, Username: "user"}, role: tt.role, roleErr: tt.roleErr}
			h := NewProfileHandler(stub, &config.Config{})
			req, rec := mockProfileRequest(http.MethodDelete, "/api/v1/profile/header", tt.accountID)

			h.DeleteHeader(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.accountID != 0 && !stub.clearHeaderCalled {
				t.Fatal("expected ClearAccountHeader to be called")
			}

			switch tt.wantCode {
			case http.StatusOK:
				var body map[string]json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				assertJSONEqual(t, body["role"], tt.wantRole)
			case http.StatusInternalServerError:
				assertJSONEqual(t, rec.Body.Bytes(), `{"error":"Failed to fetch account"}`)
			}
		})
	}
}
