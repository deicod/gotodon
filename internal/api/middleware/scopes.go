package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// Error messages of the scope checks, as Mastodon's Api::BaseController and
// Doorkeeper render them.
const (
	// InvalidTokenMessage answers a request that needs a token and has none.
	InvalidTokenMessage = "The access token is invalid"
	// OutsideScopesMessage answers a token that holds none of the scopes a
	// route accepts.
	OutsideScopesMessage = "This action is outside the authorized scopes"
)

// RouteRegistrar is the part of *http.ServeMux that the API handlers'
// RegisterRoutes use. Tests pass a recorder to check the scopes every
// registered route declares.
type RouteRegistrar interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// HasAnyScope reports whether a token's space-separated scopes grant any of
// the given scopes, as Doorkeeper's AccessToken#includes_scope? does for
// doorkeeper_authorize!. Each scope is matched with oauth.HasScope, so read
// and write also cover their read:* and write:* children. Mastodon always
// lists the parent next to a child scope (doorkeeper_authorize! :read,
// :'read:statuses'), so the result is what Doorkeeper's exact matching gives.
//
// A token without scopes grants nothing, as in Doorkeeper. oauth.HasScope
// reads an empty string as the default "read", which suits an application's
// registered scopes but not a token's.
func HasAnyScope(tokenScopes string, anyOf ...string) bool {
	if strings.TrimSpace(tokenScopes) == "" {
		return false
	}
	for _, scope := range anyOf {
		if oauth.HasScope(tokenScopes, scope) {
			return true
		}
	}
	return false
}

// HasToken reports whether the request carries an access token that
// Authenticate accepted. Authenticate stores the token's ID, application,
// scopes and, for a user token, its user and account together; any of them
// counts, so that a context carrying an identity is never treated as
// anonymous.
func HasToken(ctx context.Context) bool {
	for _, key := range []ContextKey{ContextKeyTokenID, ContextKeyAppID, ContextKeyScopes, ContextKeyUserID, ContextKeyAccountID} {
		if ctx.Value(key) != nil {
			return true
		}
	}
	return false
}

// RequireScope returns a wrapper that runs a handler only for a request whose
// token holds any of the given scopes, as Mastodon's
// doorkeeper_authorize!(*scopes) does:
//
//   - no token: 401 The access token is invalid
//   - a token holding none of the scopes, or no scopes at all: 403 This
//     action is outside the authorized scopes
//
// The token needn't belong to a user: an application token with the scope
// passes, and the handler decides whether it needs an account. The wrapper
// is applied at route registration, so the scopes of every route are
// declared in its handler's RegisterRoutes and are checked before the
// handler reads the request body.
//
// It panics when no scope is given or a scope isn't one of oauth.ValidScopes,
// so that the mistake shows at startup instead of locking a route.
func RequireScope(anyOf ...string) func(http.HandlerFunc) http.HandlerFunc {
	required := validatedScopes("RequireScope", anyOf)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !HasToken(r.Context()) {
				web.JSONError(w, http.StatusUnauthorized, InvalidTokenMessage)
				return
			}
			if !HasAnyScope(GetScopes(r.Context()), required...) {
				web.JSONError(w, http.StatusForbidden, OutsideScopesMessage)
				return
			}
			next(w, r)
		}
	}
}

// RequireScopeIfToken returns a wrapper that checks the scopes as
// RequireScope does when the request carries a token, and runs the handler
// without one, as Mastodon's authorize_if_got_token!(*scopes) does for the
// routes that also serve anonymous clients. A token holding none of the
// scopes, or no scopes at all, gets 403 This action is outside the
// authorized scopes; it isn't treated as anonymous.
//
// It panics as RequireScope does.
func RequireScopeIfToken(anyOf ...string) func(http.HandlerFunc) http.HandlerFunc {
	required := validatedScopes("RequireScopeIfToken", anyOf)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if HasToken(r.Context()) && !HasAnyScope(GetScopes(r.Context()), required...) {
				web.JSONError(w, http.StatusForbidden, OutsideScopesMessage)
				return
			}
			next(w, r)
		}
	}
}

// validatedScopes returns a copy of scopes, or panics when it is empty or
// holds a scope that isn't valid.
func validatedScopes(caller string, scopes []string) []string {
	if len(scopes) == 0 {
		panic(fmt.Sprintf("middleware: %s needs at least one scope", caller))
	}
	for _, scope := range scopes {
		if !oauth.ValidScopes[scope] {
			panic(fmt.Sprintf("middleware: %s got unknown scope %q", caller, scope))
		}
	}
	return append([]string(nil), scopes...)
}
