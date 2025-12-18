// Package middleware provides HTTP middleware for the API.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/web"
)

// ContextKey type for context keys
type ContextKey string

const (
	// ContextKeyUserID is the context key for the authenticated user ID
	ContextKeyUserID ContextKey = "user_id"
	// ContextKeyAccountID is the context key for the authenticated account ID
	ContextKeyAccountID ContextKey = "account_id"
	// ContextKeyTokenID is the context key for the OAuth token ID
	ContextKeyTokenID ContextKey = "token_id"
	// ContextKeyScopes is the context key for the token scopes
	ContextKeyScopes ContextKey = "scopes"
	// ContextKeyAppID is the context key for the OAuth application ID
	ContextKeyAppID ContextKey = "app_id"
)

// TokenValidator is the interface for validating OAuth tokens
type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (*TokenInfo, error)
}

// TokenInfo contains information about a validated token
type TokenInfo struct {
	TokenID   int64
	UserID    *int64 // nil for app-only tokens
	AccountID *int64 // nil for app-only tokens
	AppID     int64
	Scopes    string
}

// RequireAuth returns middleware that requires authentication
func RequireAuth(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
				return
			}

			info, err := validator.ValidateToken(r.Context(), token)
			if err != nil {
				web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
				return
			}

			// Store in context
			ctx := context.WithValue(r.Context(), ContextKeyTokenID, info.TokenID)
			ctx = context.WithValue(ctx, ContextKeyAppID, info.AppID)
			ctx = context.WithValue(ctx, ContextKeyScopes, info.Scopes)

			if info.UserID != nil {
				ctx = context.WithValue(ctx, ContextKeyUserID, *info.UserID)
			}
			if info.AccountID != nil {
				ctx = context.WithValue(ctx, ContextKeyAccountID, *info.AccountID)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope returns middleware that checks for required scopes
func RequireScope(requiredScopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopes, ok := r.Context().Value(ContextKeyScopes).(string)
			if !ok || scopes == "" {
				web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
				return
			}

			for _, required := range requiredScopes {
				if !hasScope(scopes, required) {
					web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuth returns middleware that optionally authenticates
func OptionalAuth(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			info, err := validator.ValidateToken(r.Context(), token)
			if err != nil {
				// Invalid token is ignored for optional auth
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), ContextKeyTokenID, info.TokenID)
			ctx = context.WithValue(ctx, ContextKeyAppID, info.AppID)
			ctx = context.WithValue(ctx, ContextKeyScopes, info.Scopes)

			if info.UserID != nil {
				ctx = context.WithValue(ctx, ContextKeyUserID, *info.UserID)
			}
			if info.AccountID != nil {
				ctx = context.WithValue(ctx, ContextKeyAccountID, *info.AccountID)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAccountID gets the account ID from the context
func GetAccountID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ContextKeyAccountID).(int64)
	return id, ok
}

// GetUserID gets the user ID from the context
func GetUserID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ContextKeyUserID).(int64)
	return id, ok
}

// GetScopes gets the scopes from the context
func GetScopes(ctx context.Context) string {
	scopes, _ := ctx.Value(ContextKeyScopes).(string)
	return scopes
}

// GetAppID gets the app ID from the context
func GetAppID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ContextKeyAppID).(int64)
	return id, ok
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func hasScope(tokenScopes, required string) bool {
	scopes := strings.Fields(tokenScopes)
	for _, scope := range scopes {
		if scope == required {
			return true
		}
		// Check hierarchical scope
		if strings.HasPrefix(required, scope+":") {
			return true
		}
		if scope == "read" && strings.HasPrefix(required, "read:") {
			return true
		}
		if scope == "write" && strings.HasPrefix(required, "write:") {
			return true
		}
		if scope == "admin:read" && strings.HasPrefix(required, "admin:read:") {
			return true
		}
		if scope == "admin:write" && strings.HasPrefix(required, "admin:write:") {
			return true
		}
	}
	return false
}
