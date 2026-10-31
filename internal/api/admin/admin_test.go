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

	accounts := NewAccountsHandler(queries)
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

	return mux
}

// adminRoutes is the expected route → permission mapping, from Mastodon's
// app/policies. moderator records whether the seeded Moderator role may use
// the route, as #103's acceptance criteria state it.
var adminRoutes = []struct {
	pattern    string
	permission int64
	moderator  bool
}{
	// AccountPolicy, UserPolicy
	{"GET /api/v1/admin/accounts", auth.PermissionManageUsers, true},
	{"GET /api/v1/admin/accounts/{id}", auth.PermissionManageUsers, true},
	{"DELETE /api/v1/admin/accounts/{id}", auth.PermissionDeleteUserData, false},
	{"POST /api/v1/admin/accounts/{id}/action", auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/approve", auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/reject", auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/enable", auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/unsilence", auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/unsuspend", auth.PermissionManageUsers, true},
	{"POST /api/v1/admin/accounts/{id}/unsensitive", auth.PermissionManageUsers, true},

	// DomainAllowPolicy
	{"GET /api/v1/admin/domain_allows", auth.PermissionManageFederation, false},
	{"POST /api/v1/admin/domain_allows", auth.PermissionManageFederation, false},
	{"GET /api/v1/admin/domain_allows/{id}", auth.PermissionManageFederation, false},
	{"DELETE /api/v1/admin/domain_allows/{id}", auth.PermissionManageFederation, false},

	// DomainBlockPolicy
	{"GET /api/v1/admin/domain_blocks", auth.PermissionManageFederation, false},
	{"POST /api/v1/admin/domain_blocks", auth.PermissionManageFederation, false},
	{"GET /api/v1/admin/domain_blocks/{id}", auth.PermissionManageFederation, false},
	{"PUT /api/v1/admin/domain_blocks/{id}", auth.PermissionManageFederation, false},
	{"PATCH /api/v1/admin/domain_blocks/{id}", auth.PermissionManageFederation, false},
	{"DELETE /api/v1/admin/domain_blocks/{id}", auth.PermissionManageFederation, false},

	// IpBlockPolicy
	{"GET /api/v1/admin/ip_blocks", auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/ip_blocks", auth.PermissionManageBlocks, false},
	{"GET /api/v1/admin/ip_blocks/{id}", auth.PermissionManageBlocks, false},
	{"PUT /api/v1/admin/ip_blocks/{id}", auth.PermissionManageBlocks, false},
	{"PATCH /api/v1/admin/ip_blocks/{id}", auth.PermissionManageBlocks, false},
	{"DELETE /api/v1/admin/ip_blocks/{id}", auth.PermissionManageBlocks, false},

	// CanonicalEmailBlockPolicy
	{"GET /api/v1/admin/canonical_email_blocks", auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/canonical_email_blocks", auth.PermissionManageBlocks, false},
	{"GET /api/v1/admin/canonical_email_blocks/{id}", auth.PermissionManageBlocks, false},
	{"DELETE /api/v1/admin/canonical_email_blocks/{id}", auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/canonical_email_blocks/test", auth.PermissionManageBlocks, false},

	// EmailDomainBlockPolicy
	{"GET /api/v1/admin/email_domain_blocks", auth.PermissionManageBlocks, false},
	{"POST /api/v1/admin/email_domain_blocks", auth.PermissionManageBlocks, false},
	{"GET /api/v1/admin/email_domain_blocks/{id}", auth.PermissionManageBlocks, false},
	{"DELETE /api/v1/admin/email_domain_blocks/{id}", auth.PermissionManageBlocks, false},

	// TagPolicy
	{"GET /api/v1/admin/tags", auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/tags/{id}", auth.PermissionManageTaxonomies, true},
	{"PUT /api/v1/admin/tags/{id}", auth.PermissionManageTaxonomies, true},
	{"PATCH /api/v1/admin/tags/{id}", auth.PermissionManageTaxonomies, true},

	// ReportPolicy
	{"GET /api/v1/admin/reports", auth.PermissionManageReports, true},
	{"GET /api/v1/admin/reports/{id}", auth.PermissionManageReports, true},
	{"PUT /api/v1/admin/reports/{id}", auth.PermissionManageReports, true},
	{"PATCH /api/v1/admin/reports/{id}", auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/resolve", auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/assign_to_self", auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/unassign", auth.PermissionManageReports, true},
	{"POST /api/v1/admin/reports/{id}/reopen", auth.PermissionManageReports, true},

	// SettingsPolicy (instance settings are gotodon-specific)
	{"GET /api/v1/admin/instance", auth.PermissionManageSettings, false},
	{"PATCH /api/v1/admin/instance", auth.PermissionManageSettings, false},

	// TagPolicy#review?, Admin::StatusPolicy#review?, PreviewCardPolicy,
	// PreviewCardProviderPolicy
	{"GET /api/v1/admin/trends/tags", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/tags/{id}/approve", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/tags/{id}/reject", auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/trends/statuses", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/statuses/{id}/approve", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/statuses/{id}/reject", auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/trends/links", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/{id}/approve", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/{id}/reject", auth.PermissionManageTaxonomies, true},
	{"GET /api/v1/admin/trends/links/publishers", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/publishers/{id}/approve", auth.PermissionManageTaxonomies, true},
	{"POST /api/v1/admin/trends/links/publishers/{id}/reject", auth.PermissionManageTaxonomies, true},

	// DashboardPolicy
	{"POST /api/v1/admin/dimensions", auth.PermissionViewDashboard, true},
	{"POST /api/v1/admin/measures", auth.PermissionViewDashboard, true},
	{"POST /api/v1/admin/retention", auth.PermissionViewDashboard, true},
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

func TestAdminRoutesAuthorization(t *testing.T) {
	type caller struct {
		name          string
		authenticated bool
		hasRole       bool
		permissions   func(routePermission int64) int64
		allowed       func(route int) bool
		refusal       int
	}
	always := func(int) bool { return true }
	never := func(int) bool { return false }
	fixed := func(p int64) func(int64) int64 { return func(int64) int64 { return p } }

	callers := []caller{
		{name: "unauthenticated", allowed: never, refusal: http.StatusUnauthorized},
		{name: "user without a role", authenticated: true, allowed: never, refusal: http.StatusForbidden},
		{name: "Owner", authenticated: true, hasRole: true, permissions: fixed(ownerPermissions), allowed: always},
		{name: "Admin", authenticated: true, hasRole: true, permissions: fixed(adminPermissions), allowed: always},
		{name: "administrator bit only", authenticated: true, hasRole: true, permissions: fixed(auth.PermissionAdministrator), allowed: always},
		{
			name: "Moderator", authenticated: true, hasRole: true, permissions: fixed(moderatorPermissions),
			allowed: func(route int) bool { return adminRoutes[route].moderator }, refusal: http.StatusForbidden,
		},
		{
			name: "exactly the route's permission", authenticated: true, hasRole: true,
			permissions: func(p int64) int64 { return p }, allowed: always,
		},
		{
			name: "every permission but the route's", authenticated: true, hasRole: true,
			permissions: func(p int64) int64 { return auth.PermissionAll &^ (p | auth.PermissionAdministrator) },
			allowed:     never, refusal: http.StatusForbidden,
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
					req = withScopes(withIdentity(req, int64Ptr(10), int64Ptr(20)), adminToken)
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
