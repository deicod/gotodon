package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// RoleLookup resolves the role of the authenticated user. *db.Queries
// satisfies it; tests use stubs.
type RoleLookup interface {
	GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error)
	GetUserRole(ctx context.Context, id int64) (db.UserRole, error)
}

// NewRoleLookup returns queries as a RoleLookup. A nil *db.Queries yields a
// nil RoleLookup rather than a non-nil interface holding a nil pointer, so
// RequireAccess reports it as unavailable instead of panicking.
func NewRoleLookup(queries *db.Queries) RoleLookup {
	if queries == nil {
		return nil
	}
	return queries
}

// Errors returned by RequireAccess. The response has already been written
// when one is returned.
var (
	ErrNotAuthenticated = errors.New("admin: no authenticated account")
	ErrOutsideScopes    = errors.New("admin: token lacks the required scope")
	ErrNotPermitted     = errors.New("admin: role lacks the required permission")
	ErrRoleLookup       = errors.New("admin: role lookup failed")
)

// RequireAccess allows the request when the token holds scope and the
// authenticated user's role has any of the given permissions or the
// administrator bit. Mastodon's admin controllers require both: the scope
// through authorize_if_got_token! and the permission through role.can?(...).
// It then returns r with that role in its context, where ActingRole finds
// it. Otherwise it writes the response and returns an error. The checks run
// in this order, so the scope is checked before any database lookup:
//
//   - no authenticated account: 401 The access token is invalid
//   - the token's scopes don't include scope: 403 This action is outside the
//     authorized scopes
//   - roles is nil: 500
//   - no user, or a user without a role: 403 This action is not allowed
//   - a role without any of the permissions: 403 This action is not allowed
//   - a lookup fails otherwise: 500
//
// scope is matched with oauth.HasScope, so admin:read covers
// admin:read:accounts but admin:read:accounts doesn't cover admin:read. A
// token without scopes is refused: oauth.HasScope would read it as the
// default "read", which an admin scope never matches either, but the refusal
// shouldn't depend on that.
//
// Mastodon answers a request without a token with 403, because it skips the
// scope check then so that its admin dashboard can call these routes with a
// session cookie. gotodon's API accepts no session cookies, and keeps 401.
//
// Disabled users never get this far: the token validator rejects their
// tokens. So UserRole.nobody, which Mastodon's ApplicationPolicy#role gives
// them, never applies here.
func RequireAccess(w http.ResponseWriter, r *http.Request, roles RoleLookup, scope string, anyOf ...int64) (*http.Request, error) {
	ctx := r.Context()

	userID, hasUser := middleware.GetUserID(ctx)
	accountID, hasAccount := middleware.GetAccountID(ctx)
	if !hasUser && !hasAccount {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return nil, ErrNotAuthenticated
	}

	scopes := middleware.GetScopes(ctx)
	if strings.TrimSpace(scopes) == "" || !oauth.HasScope(scopes, scope) {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return nil, ErrOutsideScopes
	}

	if roles == nil {
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return nil, ErrRoleLookup
	}

	if !hasUser {
		user, err := roles.GetUserByAccountID(ctx, accountID)
		if err != nil {
			return nil, roleLookupFailed(w, r, err)
		}
		userID = user.ID
	}

	role, err := roles.GetUserRole(ctx, userID)
	if err != nil {
		return nil, roleLookupFailed(w, r, err)
	}

	if !auth.HasPermission(role.Permissions, anyOf...) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return nil, ErrNotPermitted
	}

	return r.WithContext(context.WithValue(ctx, actingRoleKey{}, role)), nil
}

// actingRoleKey is the context key under which RequireAccess stores the
// acting user's role.
type actingRoleKey struct{}

// ActingRole returns the role of the user making an admin request, as
// RequireAccess resolved it, for the record-level checks of Mastodon's
// policies. Every handler that WithAccess runs finds one; ok is false only
// for a request that didn't pass RequireAccess.
func ActingRole(ctx context.Context) (db.UserRole, bool) {
	role, ok := ctx.Value(actingRoleKey{}).(db.UserRole)
	return role, ok
}

// everyonePosition is the position of Mastodon's everyone role
// (UserRole::NOBODY_POSITION), which User#role gives a user without a role.
const everyonePosition int32 = -1

// overridesRole is Mastodon's UserRole#overrides?: whether acting outranks a
// target whose role is at position target. A nil target is an account
// without a user, which every role overrides. Equal positions don't
// override, so no role overrides itself, the Owner's included. Only
// positions count, never role names.
func overridesRole(acting db.UserRole, target *int32) bool {
	return target == nil || acting.Position > *target
}

// roleLookupFailed answers a failed user or role lookup: a missing row means
// the account has no user or the user has no role, which is a refusal, and
// anything else is a server error.
func roleLookupFailed(w http.ResponseWriter, r *http.Request, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return ErrNotPermitted
	}
	slog.ErrorContext(r.Context(), "Admin role lookup failed", "error", err)
	web.JSONError(w, http.StatusInternalServerError, "Database error")
	return errors.Join(ErrRoleLookup, err)
}

// WithAccess returns a wrapper that runs a handler only when RequireAccess
// allows the request for scope and any of the given permissions, with the
// request RequireAccess returned, so ActingRole works in the handler. It is
// applied at route registration so that every route's scope and permission
// are declared together in its handler's RegisterRoutes, and the checks run
// before the handler reads the request body.
//
// It panics when scope isn't an admin scope (admin:read, admin:write or one
// of their per-resource children) or when no permission is given, so that
// the mistake shows at startup: a user scope such as "read" would open the
// route to every ordinary token, and a route without a permission could
// never be reached.
func WithAccess(roles RoleLookup, scope string, anyOf ...int64) func(http.HandlerFunc) http.HandlerFunc {
	if !isAdminScope(scope) {
		panic(fmt.Sprintf("admin: WithAccess needs an admin scope, got %q", scope))
	}
	if len(anyOf) == 0 {
		panic("admin: WithAccess needs at least one permission")
	}
	required := append([]int64(nil), anyOf...)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r, err := RequireAccess(w, r, roles, scope, required...)
			if err != nil {
				return
			}
			next(w, r)
		}
	}
}

// isAdminScope reports whether scope is a valid scope in the admin:
// namespace.
func isAdminScope(scope string) bool {
	return strings.HasPrefix(scope, "admin:") && oauth.ValidScopes[scope]
}

// RouteRegistrar is the part of *http.ServeMux that the admin handlers'
// RegisterRoutes use. Tests pass a recorder to check that every registered
// admin route declares its scope and permission.
type RouteRegistrar interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}
