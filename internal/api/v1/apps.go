// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/pkg/oauth"
)

// AppsHandler handles OAuth application registration
type AppsHandler struct {
	// TODO: Add database queries dependency
}

// NewAppsHandler creates a new apps handler
func NewAppsHandler() *AppsHandler {
	return &AppsHandler{}
}

// CreateAppRequest is the request body for POST /api/v1/apps
type CreateAppRequest struct {
	ClientName   string `json:"client_name" form:"client_name"`
	RedirectURIs string `json:"redirect_uris" form:"redirect_uris"`
	Scopes       string `json:"scopes" form:"scopes"`
	Website      string `json:"website" form:"website"`
}

// CreateAppResponse is the response for POST /api/v1/apps
type CreateAppResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Website      *string `json:"website"`
	RedirectURI  string  `json:"redirect_uri"`
	ClientID     string  `json:"client_id"`
	ClientSecret string  `json:"client_secret"`
	VapidKey     string  `json:"vapid_key,omitempty"`
}

// Create handles POST /api/v1/apps
// Creates a new OAuth application
func (h *AppsHandler) Create(c echo.Context) error {
	var req CreateAppRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate required fields
	if req.ClientName == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Name can't be blank",
		})
	}
	if req.RedirectURIs == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Redirect URI can't be blank",
		})
	}

	// Validate scopes
	scopes := oauth.ParseScopes(req.Scopes)
	if err := oauth.ValidateScopes(scopes); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Scopes is not included in the list",
		})
	}

	// Generate credentials
	clientID, clientSecret, err := oauth.GenerateClientCredentials()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
			"error": "Failed to generate credentials",
		})
	}

	// TODO: Save to database
	// For now, return mock response
	var website *string
	if req.Website != "" {
		website = &req.Website
	}

	return c.JSON(http.StatusOK, CreateAppResponse{
		ID:           "1", // TODO: Use actual ID from database
		Name:         req.ClientName,
		Website:      website,
		RedirectURI:  req.RedirectURIs,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		VapidKey:     "", // TODO: Implement VAPID key generation
	})
}

// VerifyCredentials handles GET /api/v1/apps/verify_credentials
// Confirms that the app's OAuth2 credentials work
func (h *AppsHandler) VerifyCredentials(c echo.Context) error {
	// TODO: Get app from context (set by auth middleware)
	// For now, return mock response
	return c.JSON(http.StatusOK, map[string]interface{}{
		"name":    "Test App",
		"website": nil,
	})
}

// Register registers the apps routes
func (h *AppsHandler) Register(g *echo.Group) {
	g.POST("/apps", h.Create)
	// Note: verify_credentials requires authentication, added separately
}
