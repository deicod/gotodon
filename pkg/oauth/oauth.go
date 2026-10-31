// Package oauth provides OAuth 2.0 authentication for Mastodon-compatible API.
// It implements the Doorkeeper-compatible OAuth flows used by Mastodon.
package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Errors
var (
	ErrInvalidGrant       = errors.New("invalid_grant")
	ErrInvalidClient      = errors.New("invalid_client")
	ErrInvalidScope       = errors.New("invalid_scope")
	ErrInvalidRequest     = errors.New("invalid_request")
	ErrUnauthorizedClient = errors.New("unauthorized_client")
	ErrAccessDenied       = errors.New("access_denied")
	ErrUnsupportedGrant   = errors.New("unsupported_grant_type")
	ErrRevokedToken       = errors.New("token_revoked")
	ErrExpiredToken       = errors.New("token_expired")
	ErrInvalidToken       = errors.New("invalid_token")
	ErrInsufficientScope  = errors.New("insufficient_scope")
)

// Mastodon OAuth scopes (from Mastodon documentation)
const (
	ScopeRead   = "read"
	ScopeWrite  = "write"
	ScopeFollow = "follow"
	ScopePush   = "push"

	// Admin scopes. admin:read and admin:write cover their per-resource
	// children (see HasScope).
	ScopeAdminRead  = "admin:read"
	ScopeAdminWrite = "admin:write"

	ScopeAdminReadAccounts             = "admin:read:accounts"
	ScopeAdminReadReports              = "admin:read:reports"
	ScopeAdminReadDomainAllows         = "admin:read:domain_allows"
	ScopeAdminReadDomainBlocks         = "admin:read:domain_blocks"
	ScopeAdminReadIPBlocks             = "admin:read:ip_blocks"
	ScopeAdminReadEmailDomainBlocks    = "admin:read:email_domain_blocks"
	ScopeAdminReadCanonicalEmailBlocks = "admin:read:canonical_email_blocks"

	ScopeAdminWriteAccounts             = "admin:write:accounts"
	ScopeAdminWriteReports              = "admin:write:reports"
	ScopeAdminWriteDomainAllows         = "admin:write:domain_allows"
	ScopeAdminWriteDomainBlocks         = "admin:write:domain_blocks"
	ScopeAdminWriteIPBlocks             = "admin:write:ip_blocks"
	ScopeAdminWriteEmailDomainBlocks    = "admin:write:email_domain_blocks"
	ScopeAdminWriteCanonicalEmailBlocks = "admin:write:canonical_email_blocks"
)

// ValidScopes is the list of all valid Mastodon scopes
var ValidScopes = map[string]bool{
	// Base scopes
	"read":    true,
	"write":   true,
	"follow":  true,
	"push":    true,
	"profile": true,

	// Granular read scopes
	"read:accounts":      true,
	"read:blocks":        true,
	"read:bookmarks":     true,
	"read:favourites":    true,
	"read:filters":       true,
	"read:follows":       true,
	"read:lists":         true,
	"read:mutes":         true,
	"read:notifications": true,
	"read:search":        true,
	"read:statuses":      true,

	// Granular write scopes
	"write:accounts":      true,
	"write:blocks":        true,
	"write:bookmarks":     true,
	"write:conversations": true,
	"write:favourites":    true,
	"write:filters":       true,
	"write:follows":       true,
	"write:lists":         true,
	"write:media":         true,
	"write:mutes":         true,
	"write:notifications": true,
	"write:reports":       true,
	"write:statuses":      true,

	// Admin scopes
	"admin:read":                         true,
	"admin:read:accounts":                true,
	"admin:read:reports":                 true,
	"admin:read:domain_allows":           true,
	"admin:read:domain_blocks":           true,
	"admin:read:ip_blocks":               true,
	"admin:read:email_domain_blocks":     true,
	"admin:read:canonical_email_blocks":  true,
	"admin:write":                        true,
	"admin:write:accounts":               true,
	"admin:write:reports":                true,
	"admin:write:domain_allows":          true,
	"admin:write:domain_blocks":          true,
	"admin:write:ip_blocks":              true,
	"admin:write:email_domain_blocks":    true,
	"admin:write:canonical_email_blocks": true,
}

var orderedScopes = []string{
	"read",
	"profile",
	"write",
	"write:accounts",
	"write:blocks",
	"write:bookmarks",
	"write:conversations",
	"write:favourites",
	"write:filters",
	"write:follows",
	"write:lists",
	"write:media",
	"write:mutes",
	"write:notifications",
	"write:reports",
	"write:statuses",
	"read:accounts",
	"read:blocks",
	"read:bookmarks",
	"read:favourites",
	"read:filters",
	"read:follows",
	"read:lists",
	"read:mutes",
	"read:notifications",
	"read:search",
	"read:statuses",
	"follow",
	"push",
	"admin:read",
	"admin:read:accounts",
	"admin:read:reports",
	"admin:read:domain_allows",
	"admin:read:domain_blocks",
	"admin:read:ip_blocks",
	"admin:read:email_domain_blocks",
	"admin:read:canonical_email_blocks",
	"admin:write",
	"admin:write:accounts",
	"admin:write:reports",
	"admin:write:domain_allows",
	"admin:write:domain_blocks",
	"admin:write:ip_blocks",
	"admin:write:email_domain_blocks",
	"admin:write:canonical_email_blocks",
}

// SupportedScopes returns Mastodon-compatible scope ordering for metadata.
func SupportedScopes() []string {
	seen := make(map[string]struct{}, len(orderedScopes))
	scopes := make([]string, 0, len(orderedScopes))
	for _, scope := range orderedScopes {
		if !ValidScopes[scope] {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}
	return scopes
}

// DefaultScopes is the default scope set when none specified
const DefaultScopes = "read"

// Token represents an OAuth access token
type Token struct {
	AccessToken  string  `json:"access_token"`
	TokenType    string  `json:"token_type"`
	Scope        string  `json:"scope"`
	CreatedAt    int64   `json:"created_at"`
	ExpiresIn    *int    `json:"expires_in,omitempty"`
	RefreshToken *string `json:"refresh_token,omitempty"`
}

// Application represents a registered OAuth application
type Application struct {
	ID           int64   `json:"id,string"`
	Name         string  `json:"name"`
	Website      *string `json:"website"`
	RedirectURI  string  `json:"redirect_uri"`
	ClientID     string  `json:"client_id"`
	ClientSecret string  `json:"client_secret"`
	VapidKey     *string `json:"vapid_key,omitempty"`
	Scopes       string  `json:"scopes,omitempty"`
}

// CredentialApplication is what's returned in verify_credentials
type CredentialApplication struct {
	Name    string  `json:"name"`
	Website *string `json:"website"`
}

// GenerateToken generates a cryptographically secure token
func GenerateToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// GenerateClientCredentials generates a client ID and secret
func GenerateClientCredentials() (clientID, clientSecret string, err error) {
	clientID, err = GenerateToken(32)
	if err != nil {
		return "", "", err
	}
	clientSecret, err = GenerateToken(32)
	if err != nil {
		return "", "", err
	}
	return clientID, clientSecret, nil
}

// ParseScopes parses a space-separated scope string
func ParseScopes(scopeStr string) []string {
	if scopeStr == "" {
		return []string{DefaultScopes}
	}
	return strings.Fields(scopeStr)
}

// ValidateScopes validates that all scopes are valid Mastodon scopes
func ValidateScopes(scopes []string) error {
	for _, scope := range scopes {
		if !ValidScopes[scope] {
			return ErrInvalidScope
		}
	}
	return nil
}

// NormalizeScopes normalizes scopes to canonical form
func NormalizeScopes(scopes []string) string {
	if len(scopes) == 0 {
		return DefaultScopes
	}
	return strings.Join(scopes, " ")
}

// HasScope checks if a scope string includes a required scope
// Handles hierarchical scopes (e.g., "read" includes "read:statuses")
func HasScope(tokenScopes, requiredScope string) bool {
	scopes := ParseScopes(tokenScopes)
	required := requiredScope

	for _, scope := range scopes {
		if scope == required {
			return true
		}
		// Check hierarchical scope (e.g., "read" includes "read:*")
		if strings.HasPrefix(required, scope+":") {
			return true
		}
		// "read" includes all read:* scopes, "write" includes all write:* scopes
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

// TokenExpiresAt returns the expiration time for a token
func TokenExpiresAt(createdAt time.Time, expiresIn *int) *time.Time {
	if expiresIn == nil {
		return nil // Never expires
	}
	expires := createdAt.Add(time.Duration(*expiresIn) * time.Second)
	return &expires
}

// IsTokenExpired checks if a token has expired
func IsTokenExpired(createdAt time.Time, expiresIn *int) bool {
	if expiresIn == nil {
		return false // Never expires
	}
	expires := createdAt.Add(time.Duration(*expiresIn) * time.Second)
	return time.Now().After(expires)
}
