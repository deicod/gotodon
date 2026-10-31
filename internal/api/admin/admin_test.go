package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/oauth"
)

// mockAdminRequest creates a request carrying an authenticated account, for
// calling handler methods directly. Authorization is not involved: it runs
// in the wrapper that RegisterRoutes applies.
func mockAdminRequest(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(12345))

	rec := httptest.NewRecorder()
	return req.WithContext(ctx), rec
}

// recordingMux records the patterns RegisterRoutes registers and serves them.
type recordingMux struct {
	*http.ServeMux
	patterns []string
}

func (m *recordingMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, handler)
}

// newAdminMux registers every permission-guarded admin route with the given
// role lookup. The handlers' queries fail every call, so a request that
// passes authorization gets the handler's own error instead of needing
// Postgres.
func newAdminMux(roles RoleLookup) *recordingMux {
	queries := db.New(failingDB{})
	cfg := &config.Config{}
	mux := &recordingMux{ServeMux: http.NewServeMux()}

	accounts := NewAccountsHandler(queries, "example.com")
	accounts.roles = roles
	accounts.RegisterRoutes(mux)

	domainAllows := NewDomainAllowsHandler(queries)
	domainAllows.roles = roles
	domainAllows.RegisterRoutes(mux)

	domainBlocks := NewDomainBlocksHandler(queries)
	domainBlocks.roles = roles
	domainBlocks.RegisterRoutes(mux)

	ipBlocks := NewIpBlocksHandler(queries)
	ipBlocks.roles = roles
	ipBlocks.RegisterRoutes(mux)

	canonicalEmailBlocks := NewCanonicalEmailBlocksHandler(queries)
	canonicalEmailBlocks.roles = roles
	canonicalEmailBlocks.RegisterRoutes(mux)

	emailDomainBlocks := NewEmailDomainBlocksHandler(queries, nil)
	emailDomainBlocks.roles = roles
	emailDomainBlocks.RegisterRoutes(mux)

	tags := NewTagsHandler(queries)
	tags.roles = roles
	tags.RegisterRoutes(mux)

	reports := NewReportsHandler(queries)
	reports.roles = roles
	reports.RegisterRoutes(mux)

	instance := NewInstanceHandler(queries, cfg)
	instance.roles = roles
	instance.RegisterRoutes(mux)

	trends := NewTrendsHandler(queries, cfg)
	trends.roles = roles
	trends.RegisterRoutes(mux)

	dimensions := NewDimensionsHandler(queries, cfg, nil)
	dimensions.roles = roles
	dimensions.RegisterRoutes(mux)

	measures := NewMeasuresHandler(queries)
	measures.roles = roles
	measures.RegisterRoutes(mux)

	retention := NewRetentionHandler(queries)
	retention.roles = roles
	retention.RegisterRoutes(mux)

	customEmojis := NewCustomEmojisHandler(queries, newStubStorage())
	customEmojis.roles = roles
	customEmojis.RegisterRoutes(mux)

	return mux
}

// adminRoutes is the expected mapping of route to OAuth scope, from the
// authorize_if_got_token! calls in Mastodon's app/controllers/api/v1/admin,
// and to permission, from Mastodon's app/policies. moderator records whether
// the seeded Moderator role may use the route, as #103's acceptance criteria
// state it.
var adminRoutes = []struct {
	pattern    string
	scope      string
	permission int64
	moderator  bool
}{
	// AccountPolicy, UserPolicy
	{"GET /api/v1/admin/accounts", oauth.ScopeAdminReadAccounts, auth.PermissionManageUsers, true},
	{"GET /api/v1/admin/accounts/{id}", oauth.ScopeAdminReadAccounts, auth.PermissionManageUsers, true},
	{"DELETE /api/v1/admin/accounts/{id}", oauth.ScopeAdminWriteAccounts, auth.PermissionDeleteUserData, false},
	{"POST /api/v1/admin/accounts/{id}/action", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/approve", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/reject", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/enable", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/unsilence", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/unsuspend", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/unsensitive", oauth.ScopeAdminWriteAccounts, auth.PermissionManageUsers, true},

	// DomainAllowPolicy
	{"GET /api/v1/admin/domain_allows", oauth.ScopeAdminReadDomainAllows, auth.PermissionManageFederation, false},
	{"POST /api/v1/admin/domain_allows", oauth.ScopeAdminWriteDomainAllows, auth.PermissionManageFederation, false},
	{"GET /api/v1/admin/domain_allows/{id}", oauth.ScopeAdminReadDomainAllows, auth.PermissionManageFederation, false},
	{"DELETE /api/v1/admin/domain_allows/{id}", oauth.ScopeAdminWriteDomainAllows, auth.PermissionManageFederation, false},

	// DomainBlockPolicy
	{"GET /api/v1/admin/domain_blocks", oauth.ScopeAdminReadDomainBlocks, auth.PermissionManageFederation, false},
	{"POST /api/v1/admin/domain_blocks", oauth.ScopeAdminWriteDomainBlocks, auth.PermissionManageFederation, false},
	{"GET /api/v1/admin/domain_blocks/{id}", oauth.ScopeAdminReadDomainBlocks, auth.PermissionManageFederation, false},
	{"PUT /api/v1/admin/domain_blocks/{id}", oauth.ScopeAdminWriteDomainBlocks, auth.PermissionManageFederation, false},
	{"PATCH /api/v1/admin/domain_blocks/{id}", oauth.ScopeAdminWriteDomainBlocks, auth.PermissionManageFederation, false},
	{"DELETE /api/v1/admin/domain_blocks/{id}", oauth.ScopeAdminWriteDomainBlocks, auth.PermissionManageFederation, false},

	// IpBlockPolicy
	{"GET /api/v1/admin/ip_blocks", oauth.ScopeAdminReadIPBlocks, auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/ip_blocks", oauth.ScopeAdminWriteIPBlocks, auth.PermissionManageBlocks, false},
	{"GET /api/v1/admin/ip_blocks/{id}", oauth.ScopeAdminReadIPBlocks, auth.PermissionManageBlocks, false},
	{"PUT /api/v1/admin/ip_blocks/{id}", oauth.ScopeAdminWriteIPBlocks, auth.PermissionManageBlocks, false},
	{"PATCH /api/v1/admin/ip_blocks/{id}", oauth.ScopeAdminWriteIPBlocks, auth.PermissionManageBlocks, false},
	{"DELETE /api/v1/admin/ip_blocks/{id}", oauth.ScopeAdminWriteIPBlocks, auth.PermissionManageBlocks, false},

	// CanonicalEmailBlockPolicy
	{"GET /api/v1/admin/canonical_email_blocks", oauth.ScopeAdminReadCanonicalEmailBlocks, auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/canonical_email_blocks", oauth.ScopeAdminWriteCanonicalEmailBlocks, auth.PermissionManageBlocks, false},
	{"GET /api/v1/admin/canonical_email_blocks/{id}", oauth.ScopeAdminReadCanonicalEmailBlocks, auth.PermissionManageBlocks, false},
	{"DELETE /api/v1/admin/canonical_email_blocks/{id}", oauth.ScopeAdminWriteCanonicalEmailBlocks, auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/canonical_email_blocks/test", oauth.ScopeAdminReadCanonicalEmailBlocks, auth.PermissionManageBlocks, false},

	// EmailDomainBlockPolicy
	{"GET /api/v1/admin/email_domain_blocks", oauth.ScopeAdminReadEmailDomainBlocks, auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/email_domain_blocks", oauth.ScopeAdminWriteEmailDomainBlocks, auth.PermissionManageBlocks, false},
	{"GET /api/v1/admin/email_domain_blocks/{id}", oauth.ScopeAdminReadEmailDomainBlocks, auth.PermissionManageBlocks, false},
	{"DELETE /api/v1/admin/email_domain_blocks/{id}", oauth.ScopeAdminWriteEmailDomainBlocks, auth.PermissionManageBlocks, false},

	// TagPolicy
	{"GET /api/v1/admin/tags", oauth.ScopeAdminRead, auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/tags/{id}", oauth.ScopeAdminRead, auth.PermissionManageTaxonomies, true},
	{"PUT /api/v1/admin/tags/{id}", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"PATCH /api/v1/admin/tags/{id}", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},

	// ReportPolicy
	{"GET /api/v1/admin/reports", oauth.ScopeAdminReadReports, auth.PermissionManageReports, true},
	{"GET /api/v1/admin/reports/{id}", oauth.ScopeAdminReadReports, auth.PermissionManageReports, true},
	{"PUT /api/v1/admin/reports/{id}", oauth.ScopeAdminWriteReports, auth.PermissionManageReports, true},
	{"PATCH /api/v1/admin/reports/{id}", oauth.ScopeAdminWriteReports, auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/resolve", oauth.ScopeAdminWriteReports, auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/assign_to_self", oauth.ScopeAdminWriteReports, auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/unassign", oauth.ScopeAdminWriteReports, auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/reopen", oauth.ScopeAdminWriteReports, auth.PermissionManageReports, true},

	// SettingsPolicy (instance settings are gotodon-specific)
	{"GET /api/v1/admin/instance", oauth.ScopeAdminRead, auth.PermissionManageSettings, false},
	{"PATCH /api/v1/admin/instance", oauth.ScopeAdminWrite, auth.PermissionManageSettings, false},

	// TagPolicy#review?, Admin::StatusPolicy#review?, PreviewCardPolicy,
	// PreviewCardProviderPolicy
	{"GET /api/v1/admin/trends/tags", oauth.ScopeAdminRead, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/tags/{id}/approve", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/tags/{id}/reject", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/trends/statuses", oauth.ScopeAdminRead, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/statuses/{id}/approve", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/statuses/{id}/reject", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/trends/links", oauth.ScopeAdminRead, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/{id}/approve", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/{id}/reject", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/trends/links/publishers", oauth.ScopeAdminRead, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/publishers/{id}/approve", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/publishers/{id}/reject", oauth.ScopeAdminWrite, auth.PermissionManageTaxonomies, true},

	// DashboardPolicy
	{"POST /api/v1/admin/dimensions", oauth.ScopeAdminRead, auth.PermissionViewDashboard, true},
	{"POST /api/v1/admin/measures", oauth.ScopeAdminRead, auth.PermissionViewDashboard, true},
	{"POST /api/v1/admin/retention", oauth.ScopeAdminRead, auth.PermissionViewDashboard, true},

	// CustomEmojiPolicy. Mastodon has no REST API for custom emojis, so the
	// scopes are the parent admin scopes.
	{"GET /api/v1/admin/custom_emojis", oauth.ScopeAdminRead, auth.PermissionManageCustomEmojis, false},
	{"POST /api/v1/admin/custom_emojis", oauth.ScopeAdminWrite, auth.PermissionManageCustomEmojis, false},
	{"DELETE /api/v1/admin/custom_emojis/{id}", oauth.ScopeAdminWrite, auth.PermissionManageCustomEmojis, false},
}

func TestAdminRoutesAllDeclarePermissions(t *testing.T) {
	mux := newAdminMux(rolesFor(ownerPermissions))

	registered := slices.Clone(mux.patterns)
	slices.Sort(registered)
	expected := make([]string, 0, len(adminRoutes))
	for _, route := range adminRoutes {
		expected = append(expected, route.pattern)
	}
	slices.Sort(expected)

	if !slices.Equal(registered, expected) {
		for _, pattern := range registered {
			if !slices.Contains(expected, pattern) {
				t.Errorf("route %q is registered but has no expected permission in adminRoutes", pattern)
			}
		}
		for _, pattern := range expected {
			if !slices.Contains(registered, pattern) {
				t.Errorf("route %q is in adminRoutes but not registered", pattern)
			}
		}
		t.FailNow()
	}
}

// TestAdminRoutesModeratorColumn keeps the moderator column, which states the
// acceptance criteria, consistent with the seeded Moderator permissions.
func TestAdminRoutesModeratorColumn(t *testing.T) {
	for _, route := range adminRoutes {
		if got := auth.HasPermission(moderatorPermissions, route.permission); got != route.moderator {
			t.Errorf("%s: Moderator has the permission = %t, but moderator = %t", route.pattern, got, route.moderator)
		}
	}
}

// adminRouteRequest builds a request for a route pattern, with {id} set to 1.
func adminRouteRequest(pattern string) *http.Request {
	method, path, _ := strings.Cut(pattern, " ")
	path = strings.ReplaceAll(path, "{id}", "1")
	body := ""
	if method != http.MethodGet && method != http.MethodDelete {
		body = "{}"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// readPOSTs are the POST routes that only read, which Mastodon guards with
// admin:read.
var readPOSTs = []string{
	"POST /api/v1/admin/canonical_email_blocks/test",
	"POST /api/v1/admin/dimensions",
	"POST /api/v1/admin/measures",
	"POST /api/v1/admin/retention",
}

// isReadScope reports whether scope is admin:read or one of its children.
func isReadScope(scope string) bool {
	return scope == oauth.ScopeAdminRead || strings.HasPrefix(scope, oauth.ScopeAdminRead+":")
}

// TestAdminRoutesScopeColumn keeps the scope column consistent with the
// routes' methods: GET routes and readPOSTs need a read scope, every other
// route a write scope, and every scope is one WithAccess accepts.
func TestAdminRoutesScopeColumn(t *testing.T) {
	for _, route := range adminRoutes {
		if !isAdminScope(route.scope) {
			t.Errorf("%s: %q is not an admin scope", route.pattern, route.scope)
		}
		method, _, _ := strings.Cut(route.pattern, " ")
		wantRead := method == http.MethodGet || slices.Contains(readPOSTs, route.pattern)
		if got := isReadScope(route.scope); got != wantRead {
			t.Errorf("%s: scope %q is a read scope = %t, want %t", route.pattern, route.scope, got, wantRead)
		}
	}
}

func TestAdminRoutesAuthorization(t *testing.T) {
	const (
		invalidToken  = "The access token is invalid"
		outsideScopes = "This action is outside the authorized scopes"
		notAllowed    = "This action is not allowed"
	)

	type caller struct {
		name          string
		authenticated bool
		hasRole       bool
		permissions   func(routePermission int64) int64
		// scopes returns the token's scopes for a route; nil leaves them
		// out of the context.
		scopes  func(route int) string
		allowed func(route int) bool
		refusal int
		message string
	}
	always := func(int) bool { return true }
	never := func(int) bool { return false }
	fixed := func(p int64) func(int64) int64 { return func(int64) int64 { return p } }
	token := func(scopes string) func(int) string { return func(int) string { return scopes } }
	reads := func(route int) bool { return isReadScope(adminRoutes[route].scope) }
	writes := func(route int) bool { return !reads(route) }
	routeScopeIs := func(scope string) func(int) bool {
		return func(route int) bool { return adminRoutes[route].scope == scope }
	}
	parentScope := func(route int) string {
		if reads(route) {
			return oauth.ScopeAdminRead
		}
		return oauth.ScopeAdminWrite
	}
	// everyOtherAdminScope holds every admin scope but the route's own and
	// its parent, so the per-resource scopes are refused on routes that need
	// a parent, and no resource's scope covers another's.
	everyOtherAdminScope := func(route int) string {
		var scopes []string
		for scope := range oauth.ValidScopes {
			if isAdminScope(scope) && scope != adminRoutes[route].scope && scope != parentScope(route) {
				scopes = append(scopes, scope)
			}
		}
		slices.Sort(scopes)
		return strings.Join(scopes, " ")
	}
	owner := caller{authenticated: true, hasRole: true, permissions: fixed(ownerPermissions)}
	ownerWith := func(name string, scopes func(int) string, allowed func(int) bool) caller {
		c := owner
		c.name, c.scopes, c.allowed = name, scopes, allowed
		c.refusal, c.message = http.StatusForbidden, outsideScopes
		return c
	}

	callers := []caller{
		// Identity and role, with a token holding both admin scopes.
		{name: "unauthenticated", allowed: never, refusal: http.StatusUnauthorized, message: invalidToken},
		{name: "app token with admin scopes", scopes: token(adminToken), allowed: never, refusal: http.StatusUnauthorized, message: invalidToken},
		{name: "user without a role", authenticated: true, scopes: token(adminToken), allowed: never, refusal: http.StatusForbidden, message: notAllowed},
		{name: "Owner", authenticated: true, hasRole: true, permissions: fixed(ownerPermissions), scopes: token(adminToken), allowed: always},
		{name: "Admin", authenticated: true, hasRole: true, permissions: fixed(adminPermissions), scopes: token(adminToken), allowed: always},
		{name: "Owner storing every bit", authenticated: true, hasRole: true, permissions: fixed(-1), scopes: token(adminToken), allowed: always},
		{
			name: "Moderator", authenticated: true, hasRole: true, permissions: fixed(moderatorPermissions), scopes: token(adminToken),
			allowed: func(route int) bool { return adminRoutes[route].moderator }, refusal: http.StatusForbidden, message: notAllowed,
		},
		{
			name: "exactly the route's permission", authenticated: true, hasRole: true,
			permissions: func(p int64) int64 { return p }, scopes: token(adminToken), allowed: always,
		},
		{
			name: "every permission but the route's", authenticated: true, hasRole: true,
			permissions: func(p int64) int64 { return auth.PermissionAll &^ (p | auth.PermissionAdministrator) },
			scopes:      token(adminToken), allowed: never, refusal: http.StatusForbidden, message: notAllowed,
		},

		// Scopes, for an Owner.
		ownerWith("Owner, read write follow push", token("read write follow push"), never),
		ownerWith("Owner, empty scope string", token(""), never),
		ownerWith("Owner, no scopes", nil, never),
		ownerWith("Owner, admin:read", token(oauth.ScopeAdminRead), reads),
		ownerWith("Owner, admin:write", token(oauth.ScopeAdminWrite), writes),
		ownerWith("Owner, read write admin:read", token("read write admin:read"), reads),
		ownerWith("Owner, exactly the route's scope", func(route int) string { return adminRoutes[route].scope }, always),
		ownerWith("Owner, the route's parent scope", parentScope, always),
		ownerWith("Owner, every other admin scope", everyOtherAdminScope, never),
		ownerWith("Owner, admin:read:accounts", token(oauth.ScopeAdminReadAccounts), routeScopeIs(oauth.ScopeAdminReadAccounts)),
		ownerWith("Owner, admin:write:accounts", token(oauth.ScopeAdminWriteAccounts), routeScopeIs(oauth.ScopeAdminWriteAccounts)),
		ownerWith("Owner, admin:write:domain_blocks", token(oauth.ScopeAdminWriteDomainBlocks), routeScopeIs(oauth.ScopeAdminWriteDomainBlocks)),
		ownerWith("Owner, admin:read:canonical_email_blocks", token(oauth.ScopeAdminReadCanonicalEmailBlocks), routeScopeIs(oauth.ScopeAdminReadCanonicalEmailBlocks)),

		// The scope is checked before the role is looked up.
		{name: "user without a role, user scopes", authenticated: true, scopes: token("read write follow push"), allowed: never, refusal: http.StatusForbidden, message: outsideScopes},
		{
			name: "every permission but the route's, user scopes", authenticated: true, hasRole: true,
			permissions: func(p int64) int64 { return auth.PermissionAll &^ (p | auth.PermissionAdministrator) },
			scopes:      token("read write follow push"), allowed: never, refusal: http.StatusForbidden, message: outsideScopes,
		},
	}

	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			for i, route := range adminRoutes {
				roles := &stubRoles{
					usersByAccount: map[int64]db.User{10: {ID: 20, AccountID: 10}},
					roles:          map[int64]db.UserRole{},
				}
				if c.hasRole {
					roles.roles[20] = db.UserRole{ID: 3, Permissions: c.permissions(route.permission)}
				}
				mux := newAdminMux(roles)

				req := adminRouteRequest(route.pattern)
				if c.authenticated {
					req = withIdentity(req, int64Ptr(10), int64Ptr(20))
				}
				if c.scopes != nil {
					req = withScopes(req, c.scopes(i))
				}
				if _, matched := mux.Handler(req); matched != route.pattern {
					t.Fatalf("request for %s matched %q", route.pattern, matched)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)

				refused := rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden
				if c.allowed(i) {
					if refused {
						t.Errorf("%s: status = %d (%s), want the handler to run", route.pattern, rec.Code, strings.TrimSpace(rec.Body.String()))
					}
					continue
				}
				if rec.Code != c.refusal {
					t.Errorf("%s: status = %d (%s), want %d", route.pattern, rec.Code, strings.TrimSpace(rec.Body.String()), c.refusal)
					continue
				}
				if got := errorMessage(t, rec); got != c.message {
					t.Errorf("%s: error = %q, want %q", route.pattern, got, c.message)
				}
				if c.message == outsideScopes && (len(roles.userLookups) != 0 || len(roles.roleLookups) != 0) {
					t.Errorf("%s: refused by scope after looking up users %v and roles %v", route.pattern, roles.userLookups, roles.roleLookups)
				}
			}
		})
	}
}

// TestAdminRoutesWithoutDatabase checks the production wiring: a handler
// built without queries has no role lookup, so its routes answer 500 rather
// than running the handler or panicking.
func TestAdminRoutesWithoutDatabase(t *testing.T) {
	mux := &recordingMux{ServeMux: http.NewServeMux()}
	NewDomainBlocksHandler(nil).RegisterRoutes(mux)
	NewReportsHandler(nil).RegisterRoutes(mux)
	NewCustomEmojisHandler(nil, nil).RegisterRoutes(mux)

	for _, pattern := range mux.patterns {
		req := withScopes(withIdentity(adminRouteRequest(pattern), int64Ptr(10), int64Ptr(20)), adminToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want %d", pattern, rec.Code, http.StatusInternalServerError)
		}
		if got := errorMessage(t, rec); got != "Admin data unavailable" {
			t.Errorf("%s: error = %q, want %q", pattern, got, "Admin data unavailable")
		}
	}
}
