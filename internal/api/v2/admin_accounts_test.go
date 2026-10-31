package v2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
)

// These call List directly: authorization happens at route registration.
func TestAdminAccountsHandler_ListWithoutDB(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantCode int
		wantBody string
	}{
		{"no database", "/api/v2/admin/accounts", http.StatusInternalServerError, "Admin data unavailable"},
		{"invalid origin", "/api/v2/admin/accounts?origin=bad", http.StatusUnprocessableEntity, "Unknown origin: bad"},
		{"invalid status", "/api/v2/admin/accounts?status=bad", http.StatusUnprocessableEntity, "Unknown status: bad"},
		{"invalid ip matches nothing", "/api/v2/admin/accounts?ip=not-an-ip", http.StatusOK, "[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAdminAccountsHandler(nil, nil)
			req, rec := mockAccountRequest(http.MethodGet, tt.path, "")

			h.List(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// stubRoles is an adminapi.RoleLookup: account 10 belongs to user 20, whose
// role stores permissions unless hasRole is false.
type stubRoles struct {
	hasRole     bool
	permissions int64
}

func (s stubRoles) GetUserByAccountID(_ context.Context, accountID int64) (db.User, error) {
	if accountID != 10 {
		return db.User{}, pgx.ErrNoRows
	}
	return db.User{ID: 20, AccountID: 10}, nil
}

func (s stubRoles) GetUserRole(_ context.Context, userID int64) (db.UserRole, error) {
	if userID != 20 || !s.hasRole {
		return db.UserRole{}, pgx.ErrNoRows
	}
	return db.UserRole{ID: 3, Permissions: s.permissions}, nil
}

// failingDB is a db.DBTX on which every query fails.
type failingDB struct{}

var errDatabase = errors.New("database unavailable")

func (failingDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errDatabase
}

func (failingDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errDatabase
}

func (failingDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return failingRow{}
}

type failingRow struct{}

func (failingRow) Scan(...any) error { return errDatabase }

// GET /api/v2/admin/accounts needs manage_users (AccountPolicy#index?) and
// admin:read:accounts, which Api::V2::Admin::AccountsController inherits
// from the v1 controller.
func TestAdminAccountsHandler_RouteAuthorization(t *testing.T) {
	const (
		adminToken    = "admin:read admin:write"
		outsideScopes = "This action is outside the authorized scopes"
		notAllowed    = "This action is not allowed"
	)
	owner := stubRoles{hasRole: true, permissions: -1}

	tests := []struct {
		name          string
		authenticated bool
		// scopes is the token's scope string; noScopes leaves it out of
		// the context.
		scopes      string
		noScopes    bool
		roles       stubRoles
		wantStatus  int
		wantMessage string
	}{
		{"unauthenticated", false, "", true, owner, http.StatusUnauthorized, "The access token is invalid"},
		{"app token with admin scopes", false, adminToken, false, owner, http.StatusUnauthorized, "The access token is invalid"},
		{"user without a role", true, adminToken, false, stubRoles{}, http.StatusForbidden, notAllowed},
		{"Owner", true, adminToken, false, owner, http.StatusInternalServerError, ""},
		{"Admin", true, adminToken, false, stubRoles{hasRole: true, permissions: 2031612}, http.StatusInternalServerError, ""},
		{"Moderator", true, adminToken, false, stubRoles{hasRole: true, permissions: 1049884}, http.StatusInternalServerError, ""},
		{"manage_users only", true, adminToken, false, stubRoles{hasRole: true, permissions: auth.PermissionManageUsers}, http.StatusInternalServerError, ""},
		{
			"every permission but manage_users", true, adminToken, false,
			stubRoles{hasRole: true, permissions: auth.PermissionAll &^ (auth.PermissionManageUsers | auth.PermissionAdministrator)},
			http.StatusForbidden, notAllowed,
		},

		// Scopes, for an Owner.
		{"Owner, admin:read", true, "admin:read", false, owner, http.StatusInternalServerError, ""},
		{"Owner, admin:read:accounts", true, "admin:read:accounts", false, owner, http.StatusInternalServerError, ""},
		{"Owner, read write follow push", true, "read write follow push", false, owner, http.StatusForbidden, outsideScopes},
		{"Owner, empty scope string", true, "", false, owner, http.StatusForbidden, outsideScopes},
		{"Owner, no scopes", true, "", true, owner, http.StatusForbidden, outsideScopes},
		{"Owner, admin:write", true, "admin:write", false, owner, http.StatusForbidden, outsideScopes},
		{"Owner, admin:write:accounts", true, "admin:write:accounts", false, owner, http.StatusForbidden, outsideScopes},
		{"Owner, admin:read:reports", true, "admin:read:reports", false, owner, http.StatusForbidden, outsideScopes},
		{"user without a role, user scopes", true, "read write", false, stubRoles{}, http.StatusForbidden, outsideScopes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAdminAccountsHandler(db.New(failingDB{}), nil)
			h.roles = tt.roles
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)

			req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/accounts", nil)
			ctx := req.Context()
			if tt.authenticated {
				ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(10))
				ctx = context.WithValue(ctx, middleware.ContextKeyUserID, int64(20))
			}
			if !tt.noScopes {
				ctx = context.WithValue(ctx, middleware.ContextKeyScopes, tt.scopes)
			}
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			// Allowed requests reach the handler, whose queries fail with 500.
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), tt.wantStatus)
			}
			if tt.wantMessage != "" && !strings.Contains(rec.Body.String(), tt.wantMessage) {
				t.Fatalf("body = %s, want error %q", strings.TrimSpace(rec.Body.String()), tt.wantMessage)
			}
		})
	}
}
