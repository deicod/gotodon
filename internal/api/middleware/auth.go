// Package middleware provides HTTP middleware for the API.
package middleware

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
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
	// ContextKeyClientIP is the context key for the originating client IP
	ContextKeyClientIP ContextKey = "client_ip"
)

// TokenValidator is the interface for validating OAuth tokens
type TokenValidator interface {
	ValidateToken(ctx context.Context, token string) (*TokenInfo, error)
}

// Pending reasons for tokens whose user cannot use the full API yet.
const (
	// PendingUnconfirmed marks users that have not confirmed their email.
	PendingUnconfirmed = "unconfirmed"
	// PendingApproval marks users awaiting staff approval.
	PendingApproval = "pending_approval"
)

// TokenInfo contains information about a validated token
type TokenInfo struct {
	TokenID   int64
	UserID    *int64 // nil for app-only tokens
	AccountID *int64 // nil for app-only tokens
	AppID     int64
	Scopes    string
	// PendingReason is set when the token's user is not functional yet
	// (unconfirmed email or pending approval). Such requests may only reach
	// the endpoints needed to finish onboarding.
	PendingReason string
}

// pendingAllowedRoutes are the endpoints a non-functional (unconfirmed or
// unapproved) user may still call, mirroring Mastodon's skip_require_functional
// controllers: checking/resending the confirmation email, inspecting their own
// credentials, and logging out.
var pendingAllowedRoutes = map[string]bool{
	"POST /api/v1/emails/confirmations":       true,
	"GET /api/v1/emails/check_confirmation":   true,
	"GET /api/v1/accounts/verify_credentials": true,
	"POST /oauth/revoke":                      true,
}

func pendingUserAllowed(r *http.Request) bool {
	return pendingAllowedRoutes[r.Method+" "+r.URL.Path]
}

func pendingErrorMessage(reason string) string {
	if reason == PendingApproval {
		return "Your login is currently pending approval"
	}
	return "Your login is missing a confirmed e-mail address"
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
				web.JSONError(w, http.StatusUnauthorized, authErrorMessage(err))
				return
			}
			if info.PendingReason != "" && !pendingUserAllowed(r) {
				web.JSONError(w, http.StatusForbidden, pendingErrorMessage(info.PendingReason))
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

// Authenticate optionally authenticates when a bearer or access_token is present.
// Invalid tokens will return 401 to match Mastodon behavior.
func Authenticate(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), ContextKeyClientIP, clientIP(r))
			info, err := validator.ValidateToken(ctx, token)
			if err != nil {
				web.JSONError(w, http.StatusUnauthorized, authErrorMessage(err))
				return
			}
			if info.PendingReason != "" && !pendingUserAllowed(r) {
				web.JSONError(w, http.StatusForbidden, pendingErrorMessage(info.PendingReason))
				return
			}

			ctx = context.WithValue(ctx, ContextKeyTokenID, info.TokenID)
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
		return queryAccessToken(r)
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		return queryAccessToken(r)
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func queryAccessToken(r *http.Request) string {
	if token := r.URL.Query().Get("access_token"); token != "" {
		return token
	}
	if token := r.URL.Query().Get("bearer_token"); token != "" {
		return token
	}
	return ""
}

// ClientIP resolves the originating client IP, honoring proxy headers.
func ClientIP(r *http.Request) string {
	return clientIP(r)
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
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

func authErrorMessage(err error) string {
	switch {
	case errors.Is(err, oauth.ErrRevokedToken):
		return "The access token was revoked"
	case errors.Is(err, oauth.ErrExpiredToken):
		return "The access token expired"
	default:
		return "The access token is invalid"
	}
}
