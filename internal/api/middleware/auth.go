// Package middleware provides HTTP middleware for the API.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
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
func RequireAuth(validator TokenValidator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			token := extractBearerToken(c.Request())
			if token == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
					"error": "The access token is invalid",
				})
			}

			info, err := validator.ValidateToken(c.Request().Context(), token)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
					"error": "The access token is invalid",
				})
			}

			// Store in echo context
			c.Set(string(ContextKeyTokenID), info.TokenID)
			c.Set(string(ContextKeyAppID), info.AppID)
			c.Set(string(ContextKeyScopes), info.Scopes)

			if info.UserID != nil {
				c.Set(string(ContextKeyUserID), *info.UserID)
			}
			if info.AccountID != nil {
				c.Set(string(ContextKeyAccountID), *info.AccountID)
			}

			return next(c)
		}
	}
}

// RequireScope returns middleware that checks for required scopes
func RequireScope(requiredScopes ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			scopes, ok := c.Get(string(ContextKeyScopes)).(string)
			if !ok || scopes == "" {
				return echo.NewHTTPError(http.StatusForbidden, map[string]string{
					"error": "This action is outside the authorized scopes",
				})
			}

			for _, required := range requiredScopes {
				if !hasScope(scopes, required) {
					return echo.NewHTTPError(http.StatusForbidden, map[string]string{
						"error": "This action is outside the authorized scopes",
					})
				}
			}

			return next(c)
		}
	}
}

// OptionalAuth returns middleware that optionally authenticates
func OptionalAuth(validator TokenValidator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			token := extractBearerToken(c.Request())
			if token == "" {
				return next(c)
			}

			info, err := validator.ValidateToken(c.Request().Context(), token)
			if err != nil {
				// Invalid token is ignored for optional auth
				return next(c)
			}

			c.Set(string(ContextKeyTokenID), info.TokenID)
			c.Set(string(ContextKeyAppID), info.AppID)
			c.Set(string(ContextKeyScopes), info.Scopes)

			if info.UserID != nil {
				c.Set(string(ContextKeyUserID), *info.UserID)
			}
			if info.AccountID != nil {
				c.Set(string(ContextKeyAccountID), *info.AccountID)
			}

			return next(c)
		}
	}
}

// GetAccountID gets the account ID from the context
func GetAccountID(c echo.Context) (int64, bool) {
	id, ok := c.Get(string(ContextKeyAccountID)).(int64)
	return id, ok
}

// GetUserID gets the user ID from the context
func GetUserID(c echo.Context) (int64, bool) {
	id, ok := c.Get(string(ContextKeyUserID)).(int64)
	return id, ok
}

// GetScopes gets the scopes from the context
func GetScopes(c echo.Context) string {
	scopes, _ := c.Get(string(ContextKeyScopes)).(string)
	return scopes
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
