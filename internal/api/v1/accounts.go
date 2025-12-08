// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
)

// AccountsHandler handles account-related endpoints
type AccountsHandler struct {
	// TODO: Add database queries dependency
}

// NewAccountsHandler creates a new accounts handler
func NewAccountsHandler() *AccountsHandler {
	return &AccountsHandler{}
}

// Account represents a Mastodon account in API responses
type Account struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Acct           string    `json:"acct"`
	DisplayName    string    `json:"display_name"`
	Locked         bool      `json:"locked"`
	Bot            bool      `json:"bot"`
	Discoverable   *bool     `json:"discoverable"`
	Indexable      bool      `json:"indexable"`
	Group          bool      `json:"group"`
	CreatedAt      time.Time `json:"created_at"`
	Note           string    `json:"note"`
	URL            string    `json:"url"`
	URI            string    `json:"uri"`
	Avatar         string    `json:"avatar"`
	AvatarStatic   string    `json:"avatar_static"`
	Header         string    `json:"header"`
	HeaderStatic   string    `json:"header_static"`
	FollowersCount int64     `json:"followers_count"`
	FollowingCount int64     `json:"following_count"`
	StatusesCount  int64     `json:"statuses_count"`
	LastStatusAt   *string   `json:"last_status_at"`
	Emojis         []any     `json:"emojis"`
	Fields         []Field   `json:"fields"`
}

// Field represents a profile field
type Field struct {
	Name       string     `json:"name"`
	Value      string     `json:"value"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// CredentialAccount is the account response for verify_credentials
type CredentialAccount struct {
	Account
	Source AccountSource `json:"source"`
	Role   *AccountRole  `json:"role,omitempty"`
}

// AccountSource contains the profile source data
type AccountSource struct {
	Privacy             string  `json:"privacy"`
	Sensitive           bool    `json:"sensitive"`
	Language            string  `json:"language"`
	Note                string  `json:"note"`
	Fields              []Field `json:"fields"`
	FollowRequestsCount int     `json:"follow_requests_count"`
}

// AccountRole represents a user role
type AccountRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Permissions string `json:"permissions"`
	Highlighted bool   `json:"highlighted"`
}

// VerifyCredentials handles GET /api/v1/accounts/verify_credentials
// Returns the authenticated user's account
func (h *AccountsHandler) VerifyCredentials(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Fetch account from database using accountID
	_ = accountID

	// Mock response for now
	discoverable := true
	return c.JSON(http.StatusOK, CredentialAccount{
		Account: Account{
			ID:             "1",
			Username:       "user",
			Acct:           "user",
			DisplayName:    "Test User",
			Locked:         false,
			Bot:            false,
			Discoverable:   &discoverable,
			Indexable:      true,
			Group:          false,
			CreatedAt:      time.Now().Add(-24 * time.Hour),
			Note:           "",
			URL:            "https://example.com/@user",
			URI:            "https://example.com/users/user",
			Avatar:         "https://example.com/avatars/original/missing.png",
			AvatarStatic:   "https://example.com/avatars/original/missing.png",
			Header:         "https://example.com/headers/original/missing.png",
			HeaderStatic:   "https://example.com/headers/original/missing.png",
			FollowersCount: 0,
			FollowingCount: 0,
			StatusesCount:  0,
			LastStatusAt:   nil,
			Emojis:         []any{},
			Fields:         []Field{},
		},
		Source: AccountSource{
			Privacy:             "public",
			Sensitive:           false,
			Language:            "en",
			Note:                "",
			Fields:              []Field{},
			FollowRequestsCount: 0,
		},
	})
}

// UpdateCredentialsRequest is the request for PATCH /api/v1/accounts/update_credentials
type UpdateCredentialsRequest struct {
	DisplayName     *string `json:"display_name" form:"display_name"`
	Note            *string `json:"note" form:"note"`
	Avatar          *string `json:"avatar" form:"avatar"` // Base64 or file
	Header          *string `json:"header" form:"header"` // Base64 or file
	Locked          *bool   `json:"locked" form:"locked"`
	Bot             *bool   `json:"bot" form:"bot"`
	Discoverable    *bool   `json:"discoverable" form:"discoverable"`
	HideCollections *bool   `json:"hide_collections" form:"hide_collections"`
	Indexable       *bool   `json:"indexable" form:"indexable"`
	SourcePrivacy   *string `json:"source[privacy]" form:"source[privacy]"`
	SourceSensitive *bool   `json:"source[sensitive]" form:"source[sensitive]"`
	SourceLanguage  *string `json:"source[language]" form:"source[language]"`
}

// UpdateCredentials handles PATCH /api/v1/accounts/update_credentials
func (h *AccountsHandler) UpdateCredentials(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	var req UpdateCredentialsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// TODO: Update account in database

	// Return updated account (mock for now)
	return h.VerifyCredentials(c)
}

// Get handles GET /api/v1/accounts/:id
func (h *AccountsHandler) Get(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// TODO: Fetch account from database
	discoverable := true
	return c.JSON(http.StatusOK, Account{
		ID:             id,
		Username:       "user",
		Acct:           "user",
		DisplayName:    "Test User",
		Locked:         false,
		Bot:            false,
		Discoverable:   &discoverable,
		Indexable:      true,
		Group:          false,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		Note:           "",
		URL:            "https://example.com/@user",
		URI:            "https://example.com/users/user",
		Avatar:         "https://example.com/avatars/original/missing.png",
		AvatarStatic:   "https://example.com/avatars/original/missing.png",
		Header:         "https://example.com/headers/original/missing.png",
		HeaderStatic:   "https://example.com/headers/original/missing.png",
		FollowersCount: 0,
		FollowingCount: 0,
		StatusesCount:  0,
		LastStatusAt:   nil,
		Emojis:         []any{},
		Fields:         []Field{},
	})
}

// RegisterAccountRequest is the request for POST /api/v1/accounts
type RegisterAccountRequest struct {
	Username  string  `json:"username" form:"username"`
	Email     string  `json:"email" form:"email"`
	Password  string  `json:"password" form:"password"`
	Agreement bool    `json:"agreement" form:"agreement"`
	Locale    string  `json:"locale" form:"locale"`
	Reason    *string `json:"reason" form:"reason"`
}

// Register handles POST /api/v1/accounts
// Creates a new user account
func (h *AccountsHandler) Register(c echo.Context) error {
	var req RegisterAccountRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate required fields
	if req.Username == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Username can't be blank",
		})
	}
	if req.Email == "" || !auth.ValidateEmail(req.Email) {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Email is invalid",
		})
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Password " + err.Error(),
		})
	}
	if !req.Agreement {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Agreement must be accepted",
		})
	}

	// TODO: Create account in database

	// Return a token response (Mastodon returns a token on successful registration)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"access_token": "placeholder_token",
		"token_type":   "Bearer",
		"scope":        "read write follow push",
		"created_at":   time.Now().Unix(),
	})
}

// RegisterRoutes registers the accounts routes
func (h *AccountsHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/accounts/:id", h.Get)
	// Note: authenticated routes are registered separately with middleware
}
