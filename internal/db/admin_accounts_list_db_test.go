package db

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// adminAccountsFixture holds the accounts of the admin account read tests,
// by username.
type adminAccountsFixture struct {
	ids map[string]int64
	// names maps an account ID back to its username.
	names map[int64]string
}

const testInstanceActorURI = "https://example.com/actor"

// newAdminAccountsFixture inserts, in this order:
//
//   - alice: an approved local user without a role, with an invite request,
//     an invite from mo, an application, a locale, stats and a sign-up IP;
//   - dina: a disabled local user;
//   - pete: a pending local user;
//   - sam: a suspended local user;
//   - mo: a local user with the Moderator role;
//   - actor: the local instance actor, without a user;
//   - ghost: a local account without a user;
//   - bob: a remote account on remote.example;
//   - rita: a silenced remote account on other.example;
//   - sid: a sensitized remote account on remote.example.
func newAdminAccountsFixture(t *testing.T, conn *pgx.Conn, q *Queries) adminAccountsFixture {
	t.Helper()
	ctx := context.Background()
	f := adminAccountsFixture{ids: map[string]int64{}, names: map[int64]string{}}

	account := func(username, domain, uri string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx,
			`INSERT INTO accounts (username, domain, uri) VALUES ($1, NULLIF($2::text, ''), $3) RETURNING id`,
			username, domain, uri,
		).Scan(&id); err != nil {
			t.Fatalf("insert account %s: %v", username, err)
		}
		f.ids[username] = id
		f.names[id] = username
		return id
	}
	user := func(accountID int64, username string, approved, disabled bool, role string) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx,
			`INSERT INTO users (email, account_id, approved, disabled, role_id)
			 VALUES ($1::text || '@example.com', $2, $3, $4, (SELECT id FROM user_roles WHERE name = NULLIF($5::text, '')))
			 RETURNING id`,
			username, accountID, approved, disabled, role,
		).Scan(&id); err != nil {
			t.Fatalf("insert user %s: %v", username, err)
		}
		return id
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	local := func(username string) int64 {
		return account(username, "", "https://example.com/users/"+username)
	}

	alice := local("alice")
	aliceUser := user(alice, "alice", true, false, "")
	user(local("dina"), "dina", true, true, "")
	user(local("pete"), "pete", false, false, "")
	sam := local("sam")
	user(sam, "sam", true, false, "")
	if err := q.AdminSuspendAccount(ctx, sam); err != nil {
		t.Fatalf("suspend sam: %v", err)
	}
	moUser := user(local("mo"), "mo", true, false, "Moderator")
	account("actor", "", testInstanceActorURI)
	local("ghost")
	account("bob", "remote.example", "https://remote.example/users/bob")
	rita := account("rita", "other.example", "https://other.example/users/rita")
	exec("silence rita", `UPDATE accounts SET silenced_at = now() WHERE id = $1`, rita)
	sid := account("sid", "remote.example", "https://remote.example/users/sid")
	exec("sensitize sid", `UPDATE accounts SET sensitized_at = now() WHERE id = $1`, sid)

	var appID, inviteID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO oauth_applications (name, uid, secret, redirect_uri) VALUES ('App', 'uid', 'secret', 'urn:ietf:wg:oauth:2.0:oob') RETURNING id`,
	).Scan(&appID); err != nil {
		t.Fatalf("insert application: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO invites (user_id, code) VALUES ($1, 'invite') RETURNING id`, moUser,
	).Scan(&inviteID); err != nil {
		t.Fatalf("insert invite: %v", err)
	}
	exec("update alice", `UPDATE users SET locale = 'de', sign_up_ip = '192.0.2.1', confirmed_at = now(),
		invite_id = $2, created_by_application_id = $3 WHERE id = $1`, aliceUser, inviteID, appID)
	exec("insert alice's invite request", `INSERT INTO user_invite_requests (user_id, text) VALUES ($1, 'Let me in')`, aliceUser)
	exec("insert alice's stats", `INSERT INTO account_stats (account_id, statuses_count, following_count, followers_count) VALUES ($1, 3, 2, 1)`, alice)
	return f
}

func (f adminAccountsFixture) usernames(ids []int64) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, f.names[id])
	}
	return names
}

// TestAdminGetAccount checks that the admin account read finds every
// account, with NULL user columns for one without a user.
func TestAdminGetAccount(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()
	f := newAdminAccountsFixture(t, conn, q)

	t.Run("local user", func(t *testing.T) {
		row, err := q.AdminGetAccount(ctx, f.ids["alice"])
		if err != nil {
			t.Fatalf("AdminGetAccount: %v", err)
		}
		if row.Username != "alice" || row.Domain.Valid || !row.UserID.Valid {
			t.Fatalf("row = %+v, want alice with a user", row)
		}
		if row.Email.String != "alice@example.com" || !row.ConfirmedAt.Valid || !row.Approved.Bool || row.Disabled.Bool {
			t.Fatalf("user columns = %+v", row)
		}
		if row.Locale.String != "de" || row.InviteRequest.String != "Let me in" || !row.CreatedByApplicationID.Valid {
			t.Fatalf("locale, invite request or application = %v, %v, %v", row.Locale, row.InviteRequest, row.CreatedByApplicationID)
		}
		if row.InvitedByAccountID != (pgtype.Int8{Int64: f.ids["mo"], Valid: true}) {
			t.Fatalf("invited by = %v, want mo (%d)", row.InvitedByAccountID, f.ids["mo"])
		}
		if row.SignUpIp == nil || *row.SignUpIp != netip.MustParseAddr("192.0.2.1") {
			t.Fatalf("sign-up IP = %v, want 192.0.2.1", row.SignUpIp)
		}
		if row.StatusesCount.Int64 != 3 || row.FollowingCount.Int64 != 2 || row.FollowersCount.Int64 != 1 {
			t.Fatalf("stats = %v %v %v, want 3 2 1", row.StatusesCount, row.FollowingCount, row.FollowersCount)
		}
		if row.RoleID.Valid {
			t.Fatalf("role = %v, want NULL for a user without a role", row.RoleID)
		}
	})

	for _, name := range []string{"bob", "actor", "ghost"} {
		t.Run("account without a user "+name, func(t *testing.T) {
			row, err := q.AdminGetAccount(ctx, f.ids[name])
			if err != nil {
				t.Fatalf("AdminGetAccount: %v", err)
			}
			if row.ID != f.ids[name] || row.Username != name {
				t.Fatalf("row = %+v, want %s", row, name)
			}
			if row.UserID.Valid || row.Email.Valid || row.ConfirmedAt.Valid || row.Approved.Valid || row.Disabled.Valid ||
				row.Locale.Valid || row.RoleID.Valid || row.InviteRequest.Valid || row.InvitedByAccountID.Valid {
				t.Fatalf("user columns = %+v, want NULL", row)
			}
		})
	}

	t.Run("unknown account", func(t *testing.T) {
		if _, err := q.AdminGetAccount(ctx, f.ids["sid"]+1000); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("AdminGetAccount(unknown) error = %v, want pgx.ErrNoRows", err)
		}
	})
}

// TestAdminListAccounts checks AccountFilter's params, the instance actor
// exclusion and the ID pagination.
func TestAdminListAccounts(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()
	f := newAdminAccountsFixture(t, conn, q)

	list := func(t *testing.T, params AdminListAccountsParams) []string {
		t.Helper()
		if params.Limit == 0 {
			params.Limit = 100
		}
		rows, err := q.AdminListAccounts(ctx, params)
		if err != nil {
			t.Fatalf("AdminListAccounts(%+v): %v", params, err)
		}
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return f.usernames(ids)
	}

	tests := []struct {
		name   string
		params AdminListAccountsParams
		// want is newest first.
		want []string
	}{
		{
			name:   "v1 defaults: local, active, without the instance actor",
			params: AdminListAccountsParams{Origin: "local", Status: "active", InstanceActorUri: testInstanceActorURI},
			want:   []string{"ghost", "mo", "pete", "dina", "alice"},
		},
		{
			name:   "without an instance actor URI",
			params: AdminListAccountsParams{Origin: "local", Status: "active"},
			want:   []string{"ghost", "actor", "mo", "pete", "dina", "alice"},
		},
		{
			name:   "remote",
			params: AdminListAccountsParams{Origin: "remote", InstanceActorUri: testInstanceActorURI},
			want:   []string{"sid", "rita", "bob"},
		},
		{
			name:   "remote and silenced",
			params: AdminListAccountsParams{Origin: "remote", Status: "silenced"},
			want:   []string{"rita"},
		},
		{
			name:   "remote and sensitized",
			params: AdminListAccountsParams{Origin: "remote", Status: "sensitized"},
			want:   []string{"sid"},
		},
		{
			name:   "a domain",
			params: AdminListAccountsParams{ByDomain: "remote.example"},
			want:   []string{"sid", "bob"},
		},
		{
			name:   "a domain that local accounts don't have",
			params: AdminListAccountsParams{Origin: "local", ByDomain: "remote.example"},
			want:   []string{},
		},
		{
			name:   "suspended",
			params: AdminListAccountsParams{Status: "suspended"},
			want:   []string{"sam"},
		},
		{
			name:   "pending",
			params: AdminListAccountsParams{Status: "pending"},
			want:   []string{"pete"},
		},
		{
			name:   "disabled",
			params: AdminListAccountsParams{Status: "disabled"},
			want:   []string{"dina"},
		},
		{
			name:   "staff",
			params: AdminListAccountsParams{Origin: "local", Status: "active", Permissions: "staff"},
			want:   []string{"mo"},
		},
		{
			name:   "username prefix",
			params: AdminListAccountsParams{Username: "Bo"},
			want:   []string{"bob"},
		},
		{
			name:   "email prefix",
			params: AdminListAccountsParams{Email: "ALI"},
			want:   []string{"alice"},
		},
		{
			name:   "sign-up IP in a range",
			params: AdminListAccountsParams{Ip: "192.0.2.0/24"},
			want:   []string{"alice"},
		},
		{
			name:   "invited by mo's user",
			params: AdminListAccountsParams{InvitedBy: f.userID(t, conn, "mo")},
			want:   []string{"alice"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(t, tt.params); !slices.Equal(got, tt.want) {
				t.Fatalf("listed %v, want %v", got, tt.want)
			}
		})
	}

	// The v1 defaults list ghost, mo, pete, dina, alice, newest first.
	page := func(limit int32, maxID, sinceID, minID string) AdminListAccountsParams {
		return AdminListAccountsParams{
			Limit: limit, Origin: "local", Status: "active", InstanceActorUri: testInstanceActorURI,
			MaxID: f.ids[maxID], SinceID: f.ids[sinceID], MinID: f.ids[minID],
		}
	}
	pages := []struct {
		name   string
		params AdminListAccountsParams
		want   []string
	}{
		{"first page", page(2, "", "", ""), []string{"ghost", "mo"}},
		{"before max_id", page(2, "mo", "", ""), []string{"pete", "dina"}},
		{"newest after since_id", page(2, "", "dina", ""), []string{"ghost", "mo"}},
		{"between since_id and max_id", page(10, "ghost", "dina", ""), []string{"mo", "pete"}},
		// Paginable#paginate_by_min_id: right after min_id, ascending.
		{"right after min_id", page(2, "", "", "alice"), []string{"dina", "pete"}},
		{"after min_id and before max_id", page(10, "mo", "", "alice"), []string{"dina", "pete"}},
	}
	for _, tt := range pages {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(t, tt.params); !slices.Equal(got, tt.want) {
				t.Fatalf("listed %v, want %v", got, tt.want)
			}
		})
	}
}

// userID returns the ID of the user of the account named username.
func (f adminAccountsFixture) userID(t *testing.T, conn *pgx.Conn, username string) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(context.Background(), `SELECT id FROM users WHERE account_id = $1`, f.ids[username]).Scan(&id); err != nil {
		t.Fatalf("load user of %s: %v", username, err)
	}
	return id
}
