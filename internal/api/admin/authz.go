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
// RequirePermission reports it as unavailable instead of panicking.
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
// Otherwise it writes the response and returns an error. The checks run in
// this order, so the scope is checked before any database lookup:
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
// Disabled users never get this far: the token validator rejects their tokens.
func RequireAccess(w http.ResponseWriter, r *http.Request, roles RoleLookup, scope string, anyOf ...int64) error {
	ctx := r.Context()

	userID, hasUser := middleware.GetUserID(ctx)
	accountID, hasAccount := middleware.GetAccountID(ctx)
	if !hasUser && !hasAccount {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return ErrNotAuthenticated
	}

	scopes := middleware.GetScopes(ctx)
	if strings.TrimSpace(scopes) == "" || !oauth.HasScope(scopes, scope) {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return ErrOutsideScopes
	}

	if roles == nil {
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return ErrRoleLookup
	}

	if !hasUser {
		user, err := roles.GetUserByAccountID(ctx, accountID)
		if err != nil {
			return roleLookupFailed(w, r, err)
		}
		userID = user.ID
	}

	role, err := roles.GetUserRole(ctx, userID)
	if err != nil {
		return roleLookupFailed(w, r, err)
	}

	if !auth.HasPermission(role.Permissions, anyOf...) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return ErrNotPermitted
	}

	return nil
}

// RequirePermission allows the request when the authenticated user's role has
// any of the given permissions or the administrator bit, as Mastodon's
// role.can?(...) does. Otherwise it writes the response and returns an error:
//
//   - no authenticated account: 401 The access token is invalid
//   - no user, or a user without a role: 403 This action is not allowed
//   - a role without any of the permissions: 403 This action is not allowed
//   - roles is nil or a lookup fails otherwise: 500
//
// Disabled users never get this far: the token validator rejects their tokens.
func RequirePermission(w http.ResponseWriter, r *http.Request, roles RoleLookup, anyOf ...int64) error {
	ctx := r.Context()

	userID, hasUser := middleware.GetUserID(ctx)
	accountID, hasAccount := middleware.GetAccountID(ctx)
	if !hasUser && !hasAccount {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return ErrNotAuthenticated
	}

	if roles == nil {
		web.JSONError(w, http.StatusInternalServerError, "Admin data unavailable")
		return ErrRoleLookup
	}

	if !hasUser {
		user, err := roles.GetUserByAccountID(ctx, accountID)
		if err != nil {
			return roleLookupFailed(w, r, err)
		}
		userID = user.ID
	}

	role, err := roles.GetUserRole(ctx, userID)
	if err != nil {
		return roleLookupFailed(w, r, err)
	}

	if !auth.HasPermission(role.Permissions, anyOf...) {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return ErrNotPermitted
	}

	return nil
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

// WithPermission returns a wrapper that runs a handler only when
// RequirePermission allows the request for any of the given permissions. It
// is applied at route registration so that every route's permission is
// declared in its handler's RegisterRoutes, and the check runs before the
// handler reads the request body.
//
// It panics when no permission is given, because such a route could never be
// reached; the mistake then shows at startup.
func WithPermission(roles RoleLookup, anyOf ...int64) func(http.HandlerFunc) http.HandlerFunc {
	if len(anyOf) == 0 {
		panic("admin: WithPermission needs at least one permission")
	}
	required := append([]int64(nil), anyOf...)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := RequirePermission(w, r, roles, required...); err != nil {
				return
			}
			next(w, r)
		}
	}
}

// WithAccess returns a wrapper that runs a handler only when RequireAccess
// allows the request for scope and any of the given permissions. It is
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
			if err := RequireAccess(w, r, roles, scope, required...); err != nil {
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
// admin route declares its permission.
type RouteRegistrar interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}
