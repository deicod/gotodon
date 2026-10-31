package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
)

// accountRoleStore answers the v1 admin account reads with fixed rows, so
// tests can set their role columns.
type accountRoleStore struct {
	stubAccountsStore
	list     []db.AdminListAccountsRow
	listErr  error
	accounts map[int64]db.AdminGetAccountRow
}

func (s *accountRoleStore) AdminListAccounts(context.Context, db.AdminListAccountsParams) ([]db.AdminListAccountsRow, error) {
	return s.list, s.listErr
}

func (s *accountRoleStore) AdminGetAccount(_ context.Context, id int64) (db.AdminGetAccountRow, error) {
	row, ok := s.accounts[id]
	if !ok {
		return db.AdminGetAccountRow{}, pgx.ErrNoRows
	}
	return row, nil
}

// The roles of the accounts in the responses, and their JSON as
// REST::RoleSerializer renders it, with exactly its keys. Both Owners report
// every permission.
var accountRoleCases = []struct {
	name string
	// role is the user's role; nil for a user without one.
	role *db.UserRole
	want string
}{
	{
		"Owner storing the administrator bit",
		&db.UserRole{ID: 1, Name: "Owner", Position: ownerPosition, Permissions: auth.PermissionAdministrator},
		`{"id":"1","name":"Owner","permissions":"8388607","color":"","highlighted":false}`,
	},
	{
		"Owner storing every bit",
		&db.UserRole{ID: 1, Name: "Owner", Position: ownerPosition, Permissions: -1},
		`{"id":"1","name":"Owner","permissions":"8388607","color":"","highlighted":false}`,
	},
	{
		"Admin",
		&db.UserRole{ID: 2, Name: "Admin", Position: adminPosition, Permissions: adminPermissions},
		`{"id":"2","name":"Admin","permissions":"2097148","color":"","highlighted":false}`,
	},
	{
		"Moderator",
		&db.UserRole{ID: 3, Name: "Moderator", Position: moderatorPosition, Permissions: moderatorPermissions},
		`{"id":"3","name":"Moderator","permissions":"1115420","color":"","highlighted":false}`,
	},
	{
		"highlighted role",
		&db.UserRole{ID: 7, Name: "Team", Color: "#ffcc00", Position: 5, Highlighted: true},
		`{"id":"7","name":"Team","permissions":"65536","color":"#ffcc00","highlighted":true}`,
	},
	{
		"user without a role",
		nil,
		`{"id":"-99","name":"","permissions":"65536","color":"","highlighted":false}`,
	},
}

// roleColumns returns role as the user_roles columns of a LEFT JOIN.
func roleColumns(role *db.UserRole) (id pgtype.Int8, name, color pgtype.Text, permissions pgtype.Int8, highlighted pgtype.Bool) {
	if role == nil {
		return
	}
	return pgtype.Int8{Int64: role.ID, Valid: true},
		pgtype.Text{String: role.Name, Valid: true},
		pgtype.Text{String: role.Color, Valid: true},
		pgtype.Int8{Int64: role.Permissions, Valid: true},
		pgtype.Bool{Bool: role.Highlighted, Valid: true}
}

func listRow(id int64, role *db.UserRole) db.AdminListAccountsRow {
	row := db.AdminListAccountsRow{ID: id, Username: "user" + idOf(id)}
	row.RoleID, row.RoleName, row.RoleColor, row.RolePermissions, row.RoleHighlighted = roleColumns(role)
	return row
}

func getRow(id int64, role *db.UserRole) db.AdminGetAccountRow {
	row := db.AdminGetAccountRow{ID: id, Username: "user" + idOf(id)}
	row.RoleID, row.RoleName, row.RoleColor, row.RolePermissions, row.RoleHighlighted = roleColumns(role)
	return row
}

// serveAccountReads serves the admin account routes for an Owner, with
// WithAccess in front as in production, and sends GET path.
func serveAccountReads(store *accountRoleStore, path string) *httptest.ResponseRecorder {
	h := NewAccountsHandler(nil, localDomain)
	h.store = store
	h.roles = &stubRoles{
		usersByAccount: map[int64]db.User{actingAccount: {ID: 20, AccountID: actingAccount}},
		roles:          map[int64]db.UserRole{20: {ID: 1, Name: ownerActor.name, Permissions: ownerActor.permissions, Position: ownerActor.position}},
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := withScopes(withIdentity(httptest.NewRequest(http.MethodGet, path, nil), int64Ptr(actingAccount), int64Ptr(20)), adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// assertRoleJSON checks that the role in an admin account is exactly want:
// the same keys and values.
func assertRoleJSON(t *testing.T, account map[string]json.RawMessage, want string) {
	t.Helper()

	raw, ok := account["role"]
	if !ok {
		t.Fatalf("account has no role key: %v", account)
	}
	var got, wantValue any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode role %s: %v", raw, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want %s: %v", want, err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(wantValue)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("role = %s, want %s", gotJSON, wantJSON)
	}
}

// TestAccountsGetAllRole checks GET /api/v1/admin/accounts renders each
// account's role as REST::Admin::AccountSerializer does.
func TestAccountsGetAllRole(t *testing.T) {
	store := &accountRoleStore{}
	for i, tc := range accountRoleCases {
		store.list = append(store.list, listRow(int64(200+i), tc.role))
	}

	rec := serveAccountReads(store, "/api/v1/admin/accounts")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
	}
	var accounts []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(accounts) != len(accountRoleCases) {
		t.Fatalf("got %d accounts, want %d", len(accounts), len(accountRoleCases))
	}
	for i, tc := range accountRoleCases {
		t.Run(tc.name, func(t *testing.T) {
			var id string
			if err := json.Unmarshal(accounts[i]["id"], &id); err != nil || id != idOf(int64(200+i)) {
				t.Fatalf("account %d id = %s, want %q", i, accounts[i]["id"], idOf(int64(200+i)))
			}
			assertRoleJSON(t, accounts[i], tc.want)
		})
	}
}

// TestAccountsGetRole checks GET /api/v1/admin/accounts/{id} renders the
// account's role as REST::Admin::AccountSerializer does.
func TestAccountsGetRole(t *testing.T) {
	for i, tc := range accountRoleCases {
		t.Run(tc.name, func(t *testing.T) {
			id := int64(200 + i)
			store := &accountRoleStore{accounts: map[int64]db.AdminGetAccountRow{id: getRow(id, tc.role)}}

			rec := serveAccountReads(store, "/api/v1/admin/accounts/"+idOf(id))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
			}
			var account map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			assertRoleJSON(t, account, tc.want)
		})
	}
}

// TestAccountsReadFailures checks the v1 admin account reads when the
// query finds nothing or fails.
func TestAccountsReadFailures(t *testing.T) {
	errDatabase := errors.New("database unavailable")

	tests := []struct {
		name       string
		store      *accountRoleStore
		path       string
		wantStatus int
		wantBody   string
	}{
		{"empty list", &accountRoleStore{}, "/api/v1/admin/accounts", http.StatusOK, "[]"},
		{"list fails", &accountRoleStore{listErr: errDatabase}, "/api/v1/admin/accounts", http.StatusInternalServerError, `{"error":"Database error"}`},
		{"unknown account", &accountRoleStore{}, "/api/v1/admin/accounts/999", http.StatusNotFound, `{"error":"Record not found"}`},
		{"malformed id", &accountRoleStore{}, "/api/v1/admin/accounts/abc", http.StatusNotFound, `{"error":"Record not found"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAccountReads(tt.store, tt.path)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Fatalf("body = %s, want %s", got, tt.wantBody)
			}
		})
	}
}
