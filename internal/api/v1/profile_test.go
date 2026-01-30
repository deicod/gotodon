package v1

import (
	"context"
	"encoding/json"
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
	return db.UserRole{}, pgx.ErrNoRows
}

func TestProfileHandler_DeleteAvatar(t *testing.T) {
	stub := &stubProfileQueries{account: db.Account{ID: 12345, Username: "user"}}
	h := NewProfileHandler(stub, &config.Config{})

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can delete avatar",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub.clearAvatarCalled = false
			req, rec := mockProfileRequest(http.MethodDelete, "/api/v1/profile/avatar", tt.accountID)

			h.DeleteAvatar(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var cred CredentialAccount
				if err := json.Unmarshal(rec.Body.Bytes(), &cred); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if !stub.clearAvatarCalled {
					t.Fatal("expected ClearAccountAvatar to be called")
				}
			}
		})
	}
}

func TestProfileHandler_DeleteHeader(t *testing.T) {
	stub := &stubProfileQueries{account: db.Account{ID: 12345, Username: "user"}}
	h := NewProfileHandler(stub, &config.Config{})

	tests := []struct {
		name      string
		accountID int64
		wantCode  int
	}{
		{
			name:      "authenticated user can delete header",
			accountID: 12345,
			wantCode:  http.StatusOK,
		},
		{
			name:      "unauthenticated user gets 401",
			accountID: 0,
			wantCode:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub.clearHeaderCalled = false
			req, rec := mockProfileRequest(http.MethodDelete, "/api/v1/profile/header", tt.accountID)

			h.DeleteHeader(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}

			if tt.wantCode == http.StatusOK {
				var cred CredentialAccount
				if err := json.Unmarshal(rec.Body.Bytes(), &cred); err != nil {
					t.Fatalf("Failed to unmarshal response: %v", err)
				}
				if !stub.clearHeaderCalled {
					t.Fatal("expected ClearAccountHeader to be called")
				}
			}
		})
	}
}
