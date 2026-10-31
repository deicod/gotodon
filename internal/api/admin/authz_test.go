package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/oauth"
)

// Seeded role values after migration 047.
const (
	ownerPermissions     int64 = -1
	adminPermissions     int64 = 2031612
	moderatorPermissions int64 = 1049884
)

// stubRoles is a RoleLookup backed by maps. A user missing from roles has no
// role. Errors, when set, are returned instead of a lookup result.
type stubRoles struct {
	usersByAccount map[int64]db.User
	roles          map[int64]db.UserRole
	userErr        error
	roleErr        error

	userLookups []int64
	roleLookups []int64
}

func (s *stubRoles) GetUserByAccountID(_ context.Context, accountID int64) (db.User, error) {
	s.userLookups = append(s.userLookups, accountID)
	if s.userErr != nil {
		return db.User{}, s.userErr
	}
	user, ok := s.usersByAccount[accountID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *stubRoles) GetUserRole(_ context.Context, userID int64) (db.UserRole, error) {
	s.roleLookups = append(s.roleLookups, userID)
	if s.roleErr != nil {
		return db.UserRole{}, s.roleErr
	}
	role, ok := s.roles[userID]
	if !ok {
		return db.UserRole{}, pgx.ErrNoRows
	}
	return role, nil
}

// rolesFor returns a stub where account 10 belongs to user 20, whose role
// stores the given permissions.
func rolesFor(permissions int64) *stubRoles {
	return &stubRoles{
		usersByAccount: map[int64]db.User{10: {ID: 20, AccountID: 10}},
		roles:          map[int64]db.UserRole{20: {ID: 3, Name: "Role", Permissions: permissions}},
	}
}

// withIdentity returns r with the context values the auth middleware sets.
func withIdentity(r *http.Request, accountID, userID *int64) *http.Request {
	ctx := r.Context()
	if accountID != nil {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, *accountID)
	}
	if userID != nil {
		ctx = context.WithValue(ctx, middleware.ContextKeyUserID, *userID)
	}
	return r.WithContext(ctx)
}

// withScopes returns r with the token scopes the auth middleware sets.
func withScopes(r *http.Request, scopes string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyScopes, scopes))
}

// adminToken holds both admin scopes, which cover every admin route.
const adminToken = "admin:read admin:write"

func int64Ptr(v int64) *int64 { return &v }

// errDatabase stands in for a failing database.
var errDatabase = errors.New("database unavailable")

// failingDB is a db.DBTX on which every query fails, so handlers reached in
// tests answer with their own errors instead of needing Postgres.
type failingDB struct{}

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

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

func TestRequireAccess(t *testing.T) {
	tests := []struct {
		name      string
		roles     func() *stubRoles
		nilRoles  bool
		accountID *int64
		userID    *int64
		// scopes is the token's scope string; noScopes leaves it out of the
		// context altogether.
		scopes   string
		noScopes bool
		scope    string
		anyOf    []int64

		wantErr     error
		wantStatus  int
		wantMessage string
		// Expected lookups, by account and by user ID.
		wantUserLookups []int64
		wantRoleLookups []int64
	}{
		// Order: account, then scope, then role.
		{
			name:        "no authenticated account",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			scopes:      adminToken,
			scope:       oauth.ScopeAdminReadAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrNotAuthenticated,
			wantStatus:  http.StatusUnauthorized,
			wantMessage: "The access token is invalid",
		},
		{
			name:        "no authenticated account and no scopes",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			noScopes:    true,
			scope:       oauth.ScopeAdminReadAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrNotAuthenticated,
			wantStatus:  http.StatusUnauthorized,
			wantMessage: "The access token is invalid",
		},
		{
			name:        "scope checked before the role lookup is found missing",
			nilRoles:    true,
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      "read write",
			scope:       oauth.ScopeAdminReadAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "scope checked before the user is looked up",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(99),
			scopes:      "read write",
			scope:       oauth.ScopeAdminReadAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:            "scope held, permission missing",
			roles:           func() *stubRoles { return rolesFor(moderatorPermissions) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminWriteDomainBlocks,
			anyOf:           []int64{auth.PermissionManageFederation},
			wantErr:         ErrNotPermitted,
			wantStatus:      http.StatusForbidden,
			wantMessage:     "This action is not allowed",
			wantRoleLookups: []int64{20},
		},

		// Scope matching, for an Owner.
		{
			name:        "no scopes in the context",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			noScopes:    true,
			scope:       oauth.ScopeAdminRead,
			anyOf:       []int64{auth.PermissionManageTaxonomies},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "empty scope string",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      "",
			scope:       oauth.ScopeAdminRead,
			anyOf:       []int64{auth.PermissionManageTaxonomies},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "blank scope string",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      " \t ",
			scope:       oauth.ScopeAdminRead,
			anyOf:       []int64{auth.PermissionManageTaxonomies},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "user scopes only",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      "read write follow push",
			scope:       oauth.ScopeAdminRead,
			anyOf:       []int64{auth.PermissionManageTaxonomies},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:            "parent covers the resource scope",
			roles:           func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          oauth.ScopeAdminRead,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "exact resource scope",
			roles:           func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          oauth.ScopeAdminReadAccounts,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "resource scope among others",
			roles:           func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          "read write admin:write:domain_blocks push",
			scope:           oauth.ScopeAdminWriteDomainBlocks,
			anyOf:           []int64{auth.PermissionManageFederation},
			wantRoleLookups: []int64{20},
		},
		{
			name:        "resource scope doesn't cover the parent",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      oauth.ScopeAdminReadAccounts,
			scope:       oauth.ScopeAdminRead,
			anyOf:       []int64{auth.PermissionManageTaxonomies},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "other resource's scope",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      oauth.ScopeAdminWriteDomainBlocks,
			scope:       oauth.ScopeAdminWriteIPBlocks,
			anyOf:       []int64{auth.PermissionManageBlocks},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "admin:read on a write",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      oauth.ScopeAdminRead,
			scope:       oauth.ScopeAdminWriteAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},
		{
			name:        "admin:write on a read",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      oauth.ScopeAdminWrite,
			scope:       oauth.ScopeAdminReadAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrOutsideScopes,
			wantStatus:  http.StatusForbidden,
			wantMessage: "This action is outside the authorized scopes",
		},

		// Role checks, with a token holding both admin scopes.
		{
			name:        "no role lookup",
			nilRoles:    true,
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			scopes:      adminToken,
			scope:       oauth.ScopeAdminReadAccounts,
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrRoleLookup,
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "Admin data unavailable",
		},
		{
			name:            "unknown user",
			roles:           func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:       int64Ptr(99),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantErr:         ErrNotPermitted,
			wantStatus:      http.StatusForbidden,
			wantMessage:     "This action is not allowed",
			wantUserLookups: []int64{99},
		},
		{
			name: "user lookup error",
			roles: func() *stubRoles {
				s := rolesFor(ownerPermissions)
				s.userErr = errDatabase
				return s
			},
			accountID:       int64Ptr(10),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantErr:         errDatabase,
			wantStatus:      http.StatusInternalServerError,
			wantMessage:     "Database error",
			wantUserLookups: []int64{10},
		},
		{
			name:            "user without a role",
			roles:           func() *stubRoles { return &stubRoles{} },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantErr:         ErrNotPermitted,
			wantStatus:      http.StatusForbidden,
			wantMessage:     "This action is not allowed",
			wantRoleLookups: []int64{20},
		},
		{
			name: "role lookup error",
			roles: func() *stubRoles {
				s := rolesFor(ownerPermissions)
				s.roleErr = errDatabase
				return s
			},
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantErr:         errDatabase,
			wantStatus:      http.StatusInternalServerError,
			wantMessage:     "Database error",
			wantRoleLookups: []int64{20},
		},
		{
			name:            "role without permissions",
			roles:           func() *stubRoles { return rolesFor(0) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantErr:         ErrNotPermitted,
			wantStatus:      http.StatusForbidden,
			wantMessage:     "This action is not allowed",
			wantRoleLookups: []int64{20},
		},
		{
			name:            "granted permission",
			roles:           func() *stubRoles { return rolesFor(moderatorPermissions) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminWriteReports,
			anyOf:           []int64{auth.PermissionManageReports},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "administrator bit",
			roles:           func() *stubRoles { return rolesFor(auth.PermissionAdministrator) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminRead,
			anyOf:           []int64{auth.PermissionManageEmailSubscriptions},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "any of: second permission held",
			roles:           func() *stubRoles { return rolesFor(auth.PermissionManageReports) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadReports,
			anyOf:           []int64{auth.PermissionManageUsers, auth.PermissionManageReports},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "any of: none held",
			roles:           func() *stubRoles { return rolesFor(auth.PermissionViewDashboard) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadReports,
			anyOf:           []int64{auth.PermissionManageUsers, auth.PermissionManageReports},
			wantErr:         ErrNotPermitted,
			wantStatus:      http.StatusForbidden,
			wantMessage:     "This action is not allowed",
			wantRoleLookups: []int64{20},
		},
		{
			name:            "user resolved from the account",
			roles:           func() *stubRoles { return rolesFor(adminPermissions) },
			accountID:       int64Ptr(10),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantUserLookups: []int64{10},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "user ID without account ID",
			roles:           func() *stubRoles { return rolesFor(adminPermissions) },
			userID:          int64Ptr(20),
			scopes:          adminToken,
			scope:           oauth.ScopeAdminReadAccounts,
			anyOf:           []int64{auth.PermissionManageUsers},
			wantRoleLookups: []int64{20},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stub *stubRoles
			var roles RoleLookup
			if !tt.nilRoles {
				stub = tt.roles()
				roles = stub
			}

			req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts", nil), tt.accountID, tt.userID)
			if !tt.noScopes {
				req = withScopes(req, tt.scopes)
			}
			rec := httptest.NewRecorder()

			allowed, err := RequireAccess(rec, req, roles, tt.scope, tt.anyOf...)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("RequireAccess() = %v, want nil", err)
				}
				if rec.Body.Len() != 0 {
					t.Fatalf("allowed request wrote a response: %d %s", rec.Code, rec.Body.String())
				}
				if allowed == nil {
					t.Fatal("RequireAccess() returned no request")
				}
				role, ok := ActingRole(allowed.Context())
				if !ok {
					t.Fatal("ActingRole() found no role in the returned request")
				}
				if want := stub.roles[20]; role != want {
					t.Fatalf("ActingRole() = %+v, want %+v", role, want)
				}
				if _, ok := ActingRole(req.Context()); ok {
					t.Fatal("RequireAccess() changed the caller's request")
				}
			} else {
				if allowed != nil {
					t.Fatalf("RequireAccess() returned a request with %v", err)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RequireAccess() = %v, want %v", err, tt.wantErr)
				}
				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
				}
				if got := errorMessage(t, rec); got != tt.wantMessage {
					t.Fatalf("error = %q, want %q", got, tt.wantMessage)
				}
			}

			if stub != nil {
				if !equalIDs(stub.userLookups, tt.wantUserLookups) {
					t.Fatalf("user lookups = %v, want %v", stub.userLookups, tt.wantUserLookups)
				}
				if !equalIDs(stub.roleLookups, tt.wantRoleLookups) {
					t.Fatalf("role lookups = %v, want %v", stub.roleLookups, tt.wantRoleLookups)
				}
			}
		})
	}
}

func equalIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestActingRoleWithoutRequireAccess(t *testing.T) {
	req := withScopes(withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts", nil), int64Ptr(10), int64Ptr(20)), adminToken)
	if role, ok := ActingRole(req.Context()); ok {
		t.Fatalf("ActingRole() = %+v, want none", role)
	}
}

func TestNewRoleLookup(t *testing.T) {
	if roles := NewRoleLookup(nil); roles != nil {
		t.Fatalf("NewRoleLookup(nil) = %#v, want nil", roles)
	}
	if roles := NewRoleLookup(db.New(failingDB{})); roles == nil {
		t.Fatal("NewRoleLookup(queries) = nil, want queries")
	}
}

// bodyProbe records whether a handler read the request body.
type bodyProbe struct {
	io.Reader
	read bool
}

func (b *bodyProbe) Read(p []byte) (int, error) {
	b.read = true
	return b.Reader.Read(p)
}

func TestWithAccess(t *testing.T) {
	tests := []struct {
		name        string
		scopes      string
		permissions int64
		wantCalled  bool
		wantStatus  int
		wantMessage string
	}{
		{"allowed", oauth.ScopeAdminWriteReports, moderatorPermissions, true, http.StatusNoContent, ""},
		{"refused by scope", "read write", ownerPermissions, false, http.StatusForbidden, "This action is outside the authorized scopes"},
		{"refused by read scope", oauth.ScopeAdminReadReports, ownerPermissions, false, http.StatusForbidden, "This action is outside the authorized scopes"},
		{"refused by permission", oauth.ScopeAdminWriteReports, moderatorPermissions &^ auth.PermissionManageReports, false, http.StatusForbidden, "This action is not allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var acting db.UserRole
			var hasActing bool
			next := func(w http.ResponseWriter, r *http.Request) {
				called = true
				acting, hasActing = ActingRole(r.Context())
				if _, err := io.ReadAll(r.Body); err != nil {
					t.Errorf("reading body: %v", err)
				}
				w.WriteHeader(http.StatusNoContent)
			}
			roles := rolesFor(tt.permissions)
			handler := WithAccess(roles, oauth.ScopeAdminWriteReports, auth.PermissionManageReports)(next)

			probe := &bodyProbe{Reader: strings.NewReader(`{"comment":"x"}`)}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reports/1/resolve", probe)
			req = withScopes(withIdentity(req, int64Ptr(10), int64Ptr(20)), tt.scopes)
			rec := httptest.NewRecorder()

			handler(rec, req)

			if called != tt.wantCalled {
				t.Fatalf("handler called = %t, want %t", called, tt.wantCalled)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCalled && (!hasActing || acting != roles.roles[20]) {
				t.Fatalf("handler's ActingRole() = %+v, %t, want %+v", acting, hasActing, roles.roles[20])
			}
			if !tt.wantCalled {
				if probe.read {
					t.Fatal("refused request body was read")
				}
				if got := errorMessage(t, rec); got != tt.wantMessage {
					t.Fatalf("error = %q, want %q", got, tt.wantMessage)
				}
			}
		})
	}
}

func TestWithAccessPanics(t *testing.T) {
	tests := []struct {
		name  string
		scope string
		anyOf []int64
	}{
		{"no permissions", oauth.ScopeAdminRead, nil},
		{"empty scope", "", []int64{auth.PermissionManageUsers}},
		{"user scope", oauth.ScopeRead, []int64{auth.PermissionManageUsers}},
		{"granular user scope", "write:accounts", []int64{auth.PermissionManageUsers}},
		{"admin namespace only", "admin", []int64{auth.PermissionManageUsers}},
		{"unknown admin resource", "admin:read:custom_emojis", []int64{auth.PermissionManageCustomEmojis}},
		{"two scopes in one string", "admin:read admin:write", []int64{auth.PermissionManageUsers}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("WithAccess(%q, %v) did not panic", tt.scope, tt.anyOf)
				}
			}()
			WithAccess(rolesFor(ownerPermissions), tt.scope, tt.anyOf...)
		})
	}
}

// TestWithAccessAcceptsEveryAdminScope checks that every admin scope in
// oauth.ValidScopes can guard a route.
func TestWithAccessAcceptsEveryAdminScope(t *testing.T) {
	for scope := range oauth.ValidScopes {
		if !strings.HasPrefix(scope, "admin:") {
			continue
		}
		WithAccess(rolesFor(ownerPermissions), scope, auth.PermissionManageUsers)
	}
}

// TestWithAccessCopiesPermissions guards against the caller's slice being
// changed after registration.
func TestWithAccessCopiesPermissions(t *testing.T) {
	anyOf := []int64{auth.PermissionManageReports}
	wrap := WithAccess(rolesFor(auth.PermissionManageReports), oauth.ScopeAdminReadReports, anyOf...)
	anyOf[0] = auth.PermissionManageFederation

	called := false
	handler := wrap(func(http.ResponseWriter, *http.Request) { called = true })
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/admin/reports", nil), int64Ptr(10), int64Ptr(20))
	handler(httptest.NewRecorder(), withScopes(req, oauth.ScopeAdminRead))

	if !called {
		t.Fatal("handler not called: WithAccess used the caller's modified slice")
	}
}
