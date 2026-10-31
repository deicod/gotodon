package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

var entityCreatedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func timestamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func text(s string) pgtype.Text                { return pgtype.Text{String: s, Valid: true} }
func int8Value(n int64) pgtype.Int8            { return pgtype.Int8{Int64: n, Valid: true} }
func boolValue(b bool) pgtype.Bool             { return pgtype.Bool{Bool: b, Valid: true} }

func addr(s string) *netip.Addr {
	a := netip.MustParseAddr(s)
	return &a
}

// entityCases are accounts with and without a user, and their JSON as
// REST::Admin::AccountSerializer renders it, without the nested account.
var entityCases = []struct {
	name string
	row  db.AdminListAccountsRow
	want string
	// wantAcct and wantStats describe the nested account.
	wantAcct  string
	wantStats [3]int64 // statuses, following, followers
}{
	{
		name: "local user",
		row: db.AdminListAccountsRow{
			ID: 201, Username: "alice", CreatedAt: timestamp(entityCreatedAt),
			StatusesCount: int8Value(3), FollowingCount: int8Value(2), FollowersCount: int8Value(1),
			UserID: int8Value(301), Email: text("alice@example.com"), SignUpIp: addr("192.0.2.1"),
			ConfirmedAt: timestamp(entityCreatedAt), Approved: boolValue(true), Disabled: boolValue(false),
			InviteID: int8Value(5), CreatedByApplicationID: int8Value(7), Locale: text("de"),
			InviteRequest: text("Let me in"), InvitedByAccountID: int8Value(42),
		},
		want: `{
			"id": "201", "username": "alice", "domain": null, "created_at": "2026-10-01T12:00:00Z",
			"email": "alice@example.com", "ip": "198.51.100.7", "confirmed": true,
			"suspended": false, "silenced": false, "sensitized": false,
			"disabled": false, "approved": true, "locale": "de", "invite_request": "Let me in",
			"created_by_application_id": "7", "invited_by_account_id": "42",
			"role": {"id": "-99", "name": "", "permissions": "65536", "color": "", "highlighted": false},
			"ips": [
				{"ip": "198.51.100.7", "used_at": "2026-10-01T13:00:00Z"},
				{"ip": "192.0.2.1", "used_at": "2026-10-01T12:00:00Z"}
			]
		}`,
		wantAcct:  "alice",
		wantStats: [3]int64{3, 2, 1},
	},
	{
		name: "pending user without IPs",
		row: db.AdminListAccountsRow{
			ID: 204, Username: "pete", CreatedAt: timestamp(entityCreatedAt),
			UserID: int8Value(304), Email: text("pete@example.com"),
			Approved: boolValue(false), Disabled: boolValue(true),
		},
		want: `{
			"id": "204", "username": "pete", "domain": null, "created_at": "2026-10-01T12:00:00Z",
			"email": "pete@example.com", "ip": null, "confirmed": false,
			"suspended": false, "silenced": false, "sensitized": false,
			"disabled": true, "approved": false, "locale": null, "invite_request": null,
			"role": {"id": "-99", "name": "", "permissions": "65536", "color": "", "highlighted": false},
			"ips": []
		}`,
		wantAcct: "pete",
	},
	{
		name: "remote account",
		row: db.AdminListAccountsRow{
			ID: 202, Username: "bob", Domain: text("remote.example"), CreatedAt: timestamp(entityCreatedAt),
			SilencedAt: timestamp(entityCreatedAt), SensitizedAt: timestamp(entityCreatedAt),
		},
		want: `{
			"id": "202", "username": "bob", "domain": "remote.example", "created_at": "2026-10-01T12:00:00Z",
			"email": null, "ip": null, "confirmed": null,
			"suspended": false, "silenced": true, "sensitized": true,
			"disabled": null, "approved": null, "locale": null, "invite_request": null,
			"role": null, "ips": null
		}`,
		wantAcct: "bob@remote.example",
	},
	{
		name: "local account without a user",
		row: db.AdminListAccountsRow{
			ID: 203, Username: "example.com", CreatedAt: timestamp(entityCreatedAt),
			SuspendedAt: timestamp(entityCreatedAt),
		},
		want: `{
			"id": "203", "username": "example.com", "domain": null, "created_at": "2026-10-01T12:00:00Z",
			"email": null, "ip": null, "confirmed": null,
			"suspended": true, "silenced": false, "sensitized": false,
			"disabled": null, "approved": null, "locale": null, "invite_request": null,
			"role": null, "ips": null
		}`,
		wantAcct: "example.com",
	},
}

// entityIPs are the IPs of alice's user, out of order, with rows that have
// no address or time, which are left out.
var entityIPs = []db.AdminListUserIPsRow{
	{UserID: 301, Ip: addr("192.0.2.1"), UsedAt: timestamp(entityCreatedAt)},
	{UserID: 301, Ip: addr("198.51.100.7"), UsedAt: timestamp(entityCreatedAt.Add(time.Hour))},
	{UserID: 301, UsedAt: timestamp(entityCreatedAt)},
	{UserID: 301, Ip: addr("203.0.113.9")},
}

// assertEntity checks that account is exactly want, apart from the nested
// account, whose ID, acct and counts it checks.
func assertEntity(t *testing.T, account map[string]json.RawMessage, id int64, want, wantAcct string, wantStats [3]int64) {
	t.Helper()

	var nested struct {
		ID             string `json:"id"`
		Acct           string `json:"acct"`
		StatusesCount  int64  `json:"statuses_count"`
		FollowingCount int64  `json:"following_count"`
		FollowersCount int64  `json:"followers_count"`
	}
	raw, ok := account["account"]
	if !ok {
		t.Fatalf("entity has no account: %v", account)
	}
	if err := json.Unmarshal(raw, &nested); err != nil {
		t.Fatalf("decode account %s: %v", raw, err)
	}
	if nested.ID != fmt.Sprint(id) || nested.Acct != wantAcct {
		t.Fatalf("account = %s, want id %d and acct %q", raw, id, wantAcct)
	}
	if got := [3]int64{nested.StatusesCount, nested.FollowingCount, nested.FollowersCount}; got != wantStats {
		t.Fatalf("account counts = %v, want %v", got, wantStats)
	}

	rest := make(map[string]json.RawMessage, len(account))
	for key, value := range account {
		if key != "account" {
			rest[key] = value
		}
	}
	gotJSON, err := json.Marshal(rest)
	if err != nil {
		t.Fatalf("encode entity: %v", err)
	}
	var got, wantValue any
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("decode entity: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !reflect.DeepEqual(got, wantValue) {
		wantJSON, _ := json.Marshal(wantValue)
		t.Fatalf("entity =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

// TestAccountsGetEntity checks GET /api/v1/admin/accounts/{id} for accounts
// with and without a user.
func TestAccountsGetEntity(t *testing.T) {
	for _, tc := range entityCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubAccountsStore{
				rows: map[int64]db.AdminGetAccountRow{tc.row.ID: db.AdminGetAccountRow(tc.row)},
				ips:  entityIPs,
			}
			rec := serveAccountReads(store, "/api/v1/admin/accounts/"+idOf(tc.row.ID))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
			}
			var account map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &account); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			assertEntity(t, account, tc.row.ID, tc.want, tc.wantAcct, tc.wantStats)
		})
	}
}

// TestAccountsGetAllEntities checks GET /api/v1/admin/accounts renders the
// same entities, in the query's order.
func TestAccountsGetAllEntities(t *testing.T) {
	store := &stubAccountsStore{ips: entityIPs}
	for _, tc := range entityCases {
		store.list = append(store.list, tc.row)
	}
	rec := serveAccountReads(store, "/api/v1/admin/accounts")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
	}
	var accounts []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(accounts) != len(entityCases) {
		t.Fatalf("got %d accounts, want %d", len(accounts), len(entityCases))
	}
	for i, tc := range entityCases {
		t.Run(tc.name, func(t *testing.T) {
			assertEntity(t, accounts[i], tc.row.ID, tc.want, tc.wantAcct, tc.wantStats)
		})
	}
}

// TestAccountsWriteRoutesRenderAccountsWithoutUser checks that the write
// routes that answer with the account render one without a user, as
// Mastodon's do, instead of reporting the change as a failure.
func TestAccountsWriteRoutesRenderAccountsWithoutUser(t *testing.T) {
	tests := []struct {
		name       string
		route      string
		account    int64
		wantChange string
		wantDomain any
	}{
		{"unsilence a remote account", "unsilence", targetRemote, "AdminUnsilenceAccount", "remote.example"},
		{"unsensitize a remote account", "unsensitive", targetRemote, "AdminUnsensitizeAccount", "remote.example"},
		{"unsuspend a remote account this server suspended", "unsuspend", targetRemoteSuspendedLocally, "AdminUnsuspendAccount", "remote.example"},
		{"unsilence the instance actor", "unsilence", targetInstanceActor, "AdminUnsilenceAccount", nil},
		{"unsensitize a local account without a user", "unsensitive", targetLocalNoUser, "AdminUnsensitizeAccount", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAccountsFixture(moderatorActor)
			rec := f.serve(http.MethodPost, "/api/v1/admin/accounts/"+idOf(tt.account)+"/"+tt.route, "")

			assertAllowed(t, f, rec,
				[]string{fmt.Sprintf("%s(%d)", tt.wantChange, tt.account)},
				[]string{fmt.Sprintf("%s:Account:%d", tt.route, tt.account)})

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body["id"] != idOf(tt.account) || body["domain"] != tt.wantDomain {
				t.Fatalf("body = %v, want account %d on %v", body, tt.account, tt.wantDomain)
			}
			for _, key := range []string{"email", "confirmed", "approved", "disabled", "role", "ips"} {
				if value, ok := body[key]; !ok || value != nil {
					t.Fatalf("%s = %v (present %v), want null", key, value, ok)
				}
			}
		})
	}
}

// TestAccountsWriteRoutesRenderLookupFails checks that a write route whose
// change took effect answers 500, not 404, when rendering the account fails.
func TestAccountsWriteRoutesRenderLookupFails(t *testing.T) {
	tests := []struct {
		route      string
		account    int64
		wantChange string
		wantLog    string
	}{
		{"approve", targetPending, "AdminApproveAccount", fmt.Sprintf("approve:User:%d", targetPending+100)},
		{"enable", targetNoRole, "AdminEnableAccount", fmt.Sprintf("enable:User:%d", targetNoRole+100)},
		{"unsilence", targetRemote, "AdminUnsilenceAccount", fmt.Sprintf("unsilence:Account:%d", targetRemote)},
		{"unsuspend", targetSuspended, "AdminUnsuspendAccount", fmt.Sprintf("unsuspend:Account:%d", targetSuspended)},
		{"unsensitive", targetRemote, "AdminUnsensitizeAccount", fmt.Sprintf("unsensitive:Account:%d", targetRemote)},
	}

	for _, tt := range tests {
		t.Run(tt.route, func(t *testing.T) {
			f := newAccountsFixture(ownerActor)
			f.store.getErr = errDatabase
			rec := f.serve(http.MethodPost, "/api/v1/admin/accounts/"+idOf(tt.account)+"/"+tt.route, "")

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusInternalServerError)
			}
			if got := errorMessage(t, rec); got != "Database error" {
				t.Fatalf("error = %q, want %q", got, "Database error")
			}
			if want := []string{fmt.Sprintf("%s(%d)", tt.wantChange, tt.account)}; !reflect.DeepEqual(f.store.changes, want) {
				t.Fatalf("changes = %v, want %v", f.store.changes, want)
			}
			if got := f.store.loggedActions(); !reflect.DeepEqual(got, []string{tt.wantLog}) {
				t.Fatalf("logged = %v, want %v", got, []string{tt.wantLog})
			}
		})
	}
}

// TestAccountsGetAllFilters checks how GET /api/v1/admin/accounts
// translates its query parameters, as translated_filter_params does.
func TestAccountsGetAllFilters(t *testing.T) {
	// base is the v1 default: local, active accounts without the instance
	// actor.
	base := db.AdminListAccountsParams{
		Limit:            100,
		Origin:           "local",
		Status:           "active",
		InstanceActorUri: activitypub.InstanceActorURI(localDomain),
	}
	with := func(change func(*db.AdminListAccountsParams)) db.AdminListAccountsParams {
		params := base
		change(&params)
		return params
	}

	tests := []struct {
		name  string
		query string
		want  db.AdminListAccountsParams
	}{
		{"no parameters", "", base},
		{"local only names the default", "local=true", base},
		{"remote", "remote=true", with(func(p *db.AdminListAccountsParams) { p.Origin = "remote" })},
		{"remote is a presence flag", "remote=false", with(func(p *db.AdminListAccountsParams) { p.Origin = "remote" })},
		{"blank remote isn't present", "remote=%20", base},
		{"active", "active=1", base},
		{"pending", "pending=1", with(func(p *db.AdminListAccountsParams) { p.Status = "pending" })},
		{"disabled", "disabled=1", with(func(p *db.AdminListAccountsParams) { p.Status = "disabled" })},
		{"silenced", "silenced=1", with(func(p *db.AdminListAccountsParams) { p.Status = "silenced" })},
		{"suspended", "suspended=1", with(func(p *db.AdminListAccountsParams) { p.Status = "suspended" })},
		{"the last status present wins", "suspended=1&pending=1&active=1", with(func(p *db.AdminListAccountsParams) { p.Status = "suspended" })},
		{"pending wins over active", "active=1&pending=1", with(func(p *db.AdminListAccountsParams) { p.Status = "pending" })},
		{"sensitized selects nothing in v1", "sensitized=1", base},
		{"staff", "staff=true", with(func(p *db.AdminListAccountsParams) { p.Permissions = "staff" })},
		{"a remote domain", "remote=1&by_domain=%20remote.example%20", with(func(p *db.AdminListAccountsParams) {
			p.Origin, p.ByDomain = "", "remote.example"
		})},
		{"a domain without remote stays local", "by_domain=remote.example", with(func(p *db.AdminListAccountsParams) {
			p.ByDomain = "remote.example"
		})},
		{"username without its @", "username=%20@Alice%20", with(func(p *db.AdminListAccountsParams) { p.Username = "Alice" })},
		{"display name, email and IP", "display_name=Al&email=al@&ip=192.0.2.0/24", with(func(p *db.AdminListAccountsParams) {
			p.DisplayName, p.Email, p.Ip = "Al", "al@", "192.0.2.0/24"
		})},
		{"v2 parameters are ignored", "origin=remote&status=suspended&permissions=staff&invited_by=5&role_ids[]=1", base},
		{"offset is ignored", "offset=20", base},
		{"limit", "limit=20", with(func(p *db.AdminListAccountsParams) { p.Limit = 20 })},
		{"limit above the maximum", "limit=500", with(func(p *db.AdminListAccountsParams) { p.Limit = 200 })},
		{"negative limit", "limit=-5", with(func(p *db.AdminListAccountsParams) { p.Limit = 5 })},
		{"limit that isn't a number", "limit=abc", with(func(p *db.AdminListAccountsParams) { p.Limit = 0 })},
		{"max_id and since_id", "max_id=50&since_id=10", with(func(p *db.AdminListAccountsParams) { p.MaxID, p.SinceID = 50, 10 })},
		{"min_id drops since_id", "min_id=10&since_id=5&max_id=50", with(func(p *db.AdminListAccountsParams) { p.MinID, p.MaxID = 10, 50 })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubAccountsStore{}
			rec := serveAccountReads(store, "/api/v1/admin/accounts?"+tt.query)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
			}
			if len(store.listParams) != 1 {
				t.Fatalf("listed %d times, want once", len(store.listParams))
			}
			if got := store.listParams[0]; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("params =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// TestAccountsGetAllSelectsNothing checks the parameters that can't select
// an account: the list is empty without a query.
func TestAccountsGetAllSelectsNothing(t *testing.T) {
	for _, query := range []string{"ip=not-an-ip", "ip=192.0.2.0/99", "max_id=abc", "max_id=0", "max_id=-3", "since_id=abc", "min_id=abc"} {
		t.Run(query, func(t *testing.T) {
			store := &stubAccountsStore{list: []db.AdminListAccountsRow{listRow(200, nil)}}
			rec := serveAccountReads(store, "/api/v1/admin/accounts?"+query)

			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
				t.Fatalf("response = %d %s, want 200 []", rec.Code, strings.TrimSpace(rec.Body.String()))
			}
			if len(store.listParams) != 0 {
				t.Fatalf("listed with %+v, want no query", store.listParams)
			}
			if link := rec.Header().Get("Link"); link != "" {
				t.Fatalf("Link = %q, want none", link)
			}
		})
	}
}

// TestAccountsGetAllPagination checks the order of a min_id page and the
// Link headers of insert_pagination_headers, which keep v1's own
// parameters.
func TestAccountsGetAllPagination(t *testing.T) {
	const base = "https://" + localDomain + "/api/v1/admin/accounts"
	rows := func(ids ...int64) []db.AdminListAccountsRow {
		list := make([]db.AdminListAccountsRow, 0, len(ids))
		for _, id := range ids {
			list = append(list, listRow(id, nil))
		}
		return list
	}

	tests := []struct {
		name     string
		query    string
		rows     []db.AdminListAccountsRow
		wantIDs  []string
		wantLink string
	}{
		{
			name:     "full page",
			query:    "limit=2&remote=true&offset=5&origin=local&max_id=10",
			rows:     rows(9, 8),
			wantIDs:  []string{"9", "8"},
			wantLink: `<` + base + `?limit=2&max_id=8&remote=true>; rel="next", <` + base + `?limit=2&min_id=9&remote=true>; rel="prev"`,
		},
		{
			name:     "last page",
			query:    "limit=2&staff=1",
			rows:     rows(3),
			wantIDs:  []string{"3"},
			wantLink: `<` + base + `?limit=2&min_id=3&staff=1>; rel="prev"`,
		},
		{
			name:     "min_id page in ascending order from the query",
			query:    "limit=2&min_id=4",
			rows:     rows(5, 6),
			wantIDs:  []string{"6", "5"},
			wantLink: `<` + base + `?limit=2&max_id=5>; rel="next", <` + base + `?limit=2&min_id=6>; rel="prev"`,
		},
		{
			name:    "empty page",
			query:   "pending=1",
			wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubAccountsStore{list: tt.rows}
			rec := serveAccountReads(store, "/api/v1/admin/accounts?"+tt.query)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusOK)
			}
			var accounts []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			ids := make([]string, 0, len(accounts))
			for _, account := range accounts {
				ids = append(ids, account.ID)
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tt.wantIDs)
			}
			if got := rec.Header().Get("Link"); got != tt.wantLink {
				t.Fatalf("Link =\n%s\nwant\n%s", got, tt.wantLink)
			}
		})
	}
}

// TestAccountsGetAllWithoutStore checks that the list fails closed without
// a database.
func TestAccountsGetAllWithoutStore(t *testing.T) {
	h := NewAccountsHandler(nil, nil)
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts", nil), int64Ptr(actingAccount), int64Ptr(20))
	rec := httptest.NewRecorder()
	h.GetAll(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), http.StatusInternalServerError)
	}
	if got := errorMessage(t, rec); got != "Admin data unavailable" {
		t.Fatalf("error = %q, want %q", got, "Admin data unavailable")
	}
}

func TestParseAccountsLimit(t *testing.T) {
	tests := []struct {
		raw  string
		want int32
	}{
		{"", 100},
		{"0", 0},
		{"1", 1},
		{"200", 200},
		{"201", 200},
		{"-7", 7},
		{"-500", 200},
		{"abc", 0},
		{"99999999999999999999", 200},
		{"-99999999999999999999", 200},
		{"-9223372036854775808", 200},
	}
	for _, tt := range tests {
		if got := parseAccountsLimit(tt.raw); got != tt.want {
			t.Errorf("parseAccountsLimit(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}
}
