package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
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
	owner := stubRoles{hasRole: true, permissions: auth.PermissionAdministrator}

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
		{"Owner storing every bit", true, adminToken, false, stubRoles{hasRole: true, permissions: -1}, http.StatusInternalServerError, ""},
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

// stubAdminAccountsStore is an adminAccountsStore that lists rows and finds
// an account for every ID. Errors, when set, are returned instead.
type stubAdminAccountsStore struct {
	rows        []db.AdminListAccountsV2Row
	listErr     error
	accountsErr error
	ipsErr      error
	// missing is left out of GetAccountsByIDs.
	missing int64
}

func (s *stubAdminAccountsStore) AdminListAccountsV2(context.Context, db.AdminListAccountsV2Params) ([]db.AdminListAccountsV2Row, error) {
	return s.rows, s.listErr
}

func (s *stubAdminAccountsStore) GetAccountsByIDs(_ context.Context, ids []int64) ([]db.Account, error) {
	if s.accountsErr != nil {
		return nil, s.accountsErr
	}
	accounts := make([]db.Account, 0, len(ids))
	for _, id := range ids {
		if id != s.missing {
			accounts = append(accounts, db.Account{ID: id, Username: fmt.Sprintf("user%d", id)})
		}
	}
	return accounts, nil
}

func (s *stubAdminAccountsStore) AdminListUserIPs(context.Context, []int64) ([]db.AdminListUserIPsRow, error) {
	return nil, s.ipsErr
}

// serveAdminAccounts serves GET /api/v2/admin/accounts for an Owner, with
// WithAccess in front as in production.
func serveAdminAccounts(store *stubAdminAccountsStore) *httptest.ResponseRecorder {
	h := NewAdminAccountsHandler(nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})
	h.store = store
	h.roles = stubRoles{hasRole: true, permissions: auth.PermissionAdministrator}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/accounts", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(10))
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, int64(20))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "admin:read")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// v2AccountRow is a local account with a user, whose role is role, or a
// user without a role when role is nil.
func v2AccountRow(id int64, role *db.UserRole) db.AdminListAccountsV2Row {
	row := db.AdminListAccountsV2Row{
		ID:       id,
		Username: fmt.Sprintf("user%d", id),
		UserID:   pgtype.Int8{Int64: id + 100, Valid: true},
		Approved: pgtype.Bool{Bool: true, Valid: true},
		Disabled: pgtype.Bool{Bool: false, Valid: true},
	}
	if role != nil {
		row.RoleID = pgtype.Int8{Int64: role.ID, Valid: true}
		row.RoleName = pgtype.Text{String: role.Name, Valid: true}
		row.RoleColor = pgtype.Text{String: role.Color, Valid: true}
		row.RolePermissions = pgtype.Int8{Int64: role.Permissions, Valid: true}
		row.RoleHighlighted = pgtype.Bool{Bool: role.Highlighted, Valid: true}
	}
	return row
}

// TestAdminAccountsHandler_ListRole checks the role of each account as
// REST::Admin::AccountSerializer renders it: REST::RoleSerializer with
// computed permissions, the everyone role for a user without a role, and
// null for an account without a user.
func TestAdminAccountsHandler_ListRole(t *testing.T) {
	ownerJSON := `{"id":"1","name":"Owner","permissions":"8388607","color":"","highlighted":false}`
	remote := db.AdminListAccountsV2Row{
		ID:       306,
		Username: "bob",
		Domain:   pgtype.Text{String: "remote.example", Valid: true},
	}

	tests := []struct {
		name string
		row  db.AdminListAccountsV2Row
		want string
	}{
		{"Owner storing the administrator bit", v2AccountRow(300, &db.UserRole{ID: 1, Name: "Owner", Permissions: auth.PermissionAdministrator}), ownerJSON},
		{"Owner storing every bit", v2AccountRow(301, &db.UserRole{ID: 1, Name: "Owner", Permissions: -1}), ownerJSON},
		{
			"Admin", v2AccountRow(302, &db.UserRole{ID: 2, Name: "Admin", Permissions: 2031612}),
			`{"id":"2","name":"Admin","permissions":"2097148","color":"","highlighted":false}`,
		},
		{
			"Moderator", v2AccountRow(303, &db.UserRole{ID: 3, Name: "Moderator", Permissions: 1049884}),
			`{"id":"3","name":"Moderator","permissions":"1115420","color":"","highlighted":false}`,
		},
		{
			"highlighted role", v2AccountRow(304, &db.UserRole{ID: 7, Name: "Team", Color: "#ffcc00", Highlighted: true}),
			`{"id":"7","name":"Team","permissions":"65536","color":"#ffcc00","highlighted":true}`,
		},
		{
			"user without a role", v2AccountRow(305, nil),
			`{"id":"-99","name":"","permissions":"65536","color":"","highlighted":false}`,
		},
		{"account without a user", remote, "null"},
	}

	store := &stubAdminAccountsStore{}
	for _, tt := range tests {
		store.rows = append(store.rows, tt.row)
	}
	rec := serveAdminAccounts(store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
	}
	var accounts []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(accounts) != len(tests) {
		t.Fatalf("got %d accounts, want %d", len(accounts), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if want := fmt.Sprintf("%q", fmt.Sprint(tt.row.ID)); string(accounts[i]["id"]) != want {
				t.Fatalf("id = %s, want %s", accounts[i]["id"], want)
			}
			role, ok := accounts[i]["role"]
			if !ok {
				t.Fatalf("account has no role key: %v", accounts[i])
			}
			var got, want any
			if err := json.Unmarshal(role, &got); err != nil {
				t.Fatalf("decode role %s: %v", role, err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatalf("decode want %s: %v", tt.want, err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("role = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// TestAdminAccountsHandler_ListStoreFailures checks that a failed query
// answers 500.
func TestAdminAccountsHandler_ListStoreFailures(t *testing.T) {
	rows := []db.AdminListAccountsV2Row{v2AccountRow(300, nil)}

	tests := []struct {
		name  string
		store *stubAdminAccountsStore
	}{
		{"listing fails", &stubAdminAccountsStore{listErr: errDatabase}},
		{"account lookup fails", &stubAdminAccountsStore{rows: rows, accountsErr: errDatabase}},
		{"listed account not found", &stubAdminAccountsStore{rows: rows, missing: 300}},
		{"IP lookup fails", &stubAdminAccountsStore{rows: rows, ipsErr: errDatabase}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAdminAccounts(tt.store)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusInternalServerError)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"Database error"}` {
				t.Fatalf("body = %s, want the database error", got)
			}
		})
	}
}
