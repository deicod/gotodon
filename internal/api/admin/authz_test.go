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

func TestRequirePermission(t *testing.T) {
	tests := []struct {
		name      string
		roles     func() *stubRoles
		nilRoles  bool
		accountID *int64
		userID    *int64
		anyOf     []int64

		wantErr     error
		wantStatus  int
		wantMessage string
		// Expected lookups, by account and by user ID.
		wantUserLookups []int64
		wantRoleLookups []int64
	}{
		{
			name:        "no authenticated account",
			roles:       func() *stubRoles { return rolesFor(ownerPermissions) },
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrNotAuthenticated,
			wantStatus:  http.StatusUnauthorized,
			wantMessage: "The access token is invalid",
		},
		{
			name:        "no role lookup",
			nilRoles:    true,
			accountID:   int64Ptr(10),
			userID:      int64Ptr(20),
			anyOf:       []int64{auth.PermissionManageUsers},
			wantErr:     ErrRoleLookup,
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "Admin data unavailable",
		},
		{
			name:            "unknown user",
			roles:           func() *stubRoles { return rolesFor(ownerPermissions) },
			accountID:       int64Ptr(99),
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
			anyOf:           []int64{auth.PermissionManageUsers},
			wantErr:         errDatabase,
			wantStatus:      http.StatusInternalServerError,
			wantMessage:     "Database error",
			wantRoleLookups: []int64{20},
		},
		{
			name:            "missing permission",
			roles:           func() *stubRoles { return rolesFor(moderatorPermissions) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			anyOf:           []int64{auth.PermissionManageFederation},
			wantErr:         ErrNotPermitted,
			wantStatus:      http.StatusForbidden,
			wantMessage:     "This action is not allowed",
			wantRoleLookups: []int64{20},
		},
		{
			name:            "role without permissions",
			roles:           func() *stubRoles { return rolesFor(0) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
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
			anyOf:           []int64{auth.PermissionManageReports},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "administrator bit",
			roles:           func() *stubRoles { return rolesFor(auth.PermissionAdministrator) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			anyOf:           []int64{auth.PermissionManageEmailSubscriptions},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "any of: second permission held",
			roles:           func() *stubRoles { return rolesFor(auth.PermissionManageReports) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
			anyOf:           []int64{auth.PermissionManageUsers, auth.PermissionManageReports},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "any of: none held",
			roles:           func() *stubRoles { return rolesFor(auth.PermissionViewDashboard) },
			accountID:       int64Ptr(10),
			userID:          int64Ptr(20),
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
			anyOf:           []int64{auth.PermissionManageUsers},
			wantUserLookups: []int64{10},
			wantRoleLookups: []int64{20},
		},
		{
			name:            "user ID without account ID",
			roles:           func() *stubRoles { return rolesFor(adminPermissions) },
			userID:          int64Ptr(20),
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
			rec := httptest.NewRecorder()

			err := RequirePermission(rec, req, roles, tt.anyOf...)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("RequirePermission() = %v, want nil", err)
				}
				if rec.Body.Len() != 0 {
					t.Fatalf("allowed request wrote a response: %d %s", rec.Code, rec.Body.String())
				}
			} else {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RequirePermission() = %v, want %v", err, tt.wantErr)
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

func TestWithPermission(t *testing.T) {
	tests := []struct {
		name        string
		permissions int64
		wantCalled  bool
		wantStatus  int
	}{
		{"allowed", moderatorPermissions, true, http.StatusNoContent},
		{"refused", moderatorPermissions &^ auth.PermissionManageReports, false, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := func(w http.ResponseWriter, r *http.Request) {
				called = true
				if _, err := io.ReadAll(r.Body); err != nil {
					t.Errorf("reading body: %v", err)
				}
				w.WriteHeader(http.StatusNoContent)
			}
			handler := WithPermission(rolesFor(tt.permissions), auth.PermissionManageReports)(next)

			probe := &bodyProbe{Reader: strings.NewReader(`{"comment":"x"}`)}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reports/1/resolve", probe)
			req = withIdentity(req, int64Ptr(10), int64Ptr(20))
			rec := httptest.NewRecorder()

			handler(rec, req)

			if called != tt.wantCalled {
				t.Fatalf("handler called = %t, want %t", called, tt.wantCalled)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !tt.wantCalled && probe.read {
				t.Fatal("refused request body was read")
			}
		})
	}
}

func TestWithPermissionPanicsWithoutPermissions(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("WithPermission() without permissions did not panic")
		}
	}()
	WithPermission(rolesFor(ownerPermissions))
}

// TestWithPermissionCopiesPermissions guards against the caller's slice
// being changed after registration.
func TestWithPermissionCopiesPermissions(t *testing.T) {
	anyOf := []int64{auth.PermissionManageReports}
	wrap := WithPermission(rolesFor(auth.PermissionManageReports), anyOf...)
	anyOf[0] = auth.PermissionManageFederation

	called := false
	handler := wrap(func(http.ResponseWriter, *http.Request) { called = true })
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/admin/reports", nil), int64Ptr(10), int64Ptr(20))
	handler(httptest.NewRecorder(), req)

	if !called {
		t.Fatal("handler not called: WithPermission used the caller's modified slice")
	}
}
