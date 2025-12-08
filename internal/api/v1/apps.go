// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/oauth"
)

// AppsHandler handles OAuth application registration
type AppsHandler struct {
	queries *db.Queries
}

// NewAppsHandler creates a new apps handler
func NewAppsHandler(queries *db.Queries) *AppsHandler {
	return &AppsHandler{queries: queries}
}

func (h *AppsHandler) useDB() bool {
	return h.queries != nil
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
func (h *AppsHandler) Create(c echo.Context) error {
	var req CreateAppRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

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

	scopes := oauth.ParseScopes(req.Scopes)
	if err := oauth.ValidateScopes(scopes); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Scopes is not included in the list",
		})
	}

	clientID, clientSecret, err := oauth.GenerateClientCredentials()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
			"error": "Failed to generate credentials",
		})
	}

	var website *string
	if req.Website != "" {
		website = &req.Website
	}

	ctx := context.Background()

	if h.useDB() {
		var websitePG pgtype.Text
		if req.Website != "" {
			websitePG = pgtype.Text{String: req.Website, Valid: true}
		}

		dbApp, err := h.queries.CreateOAuthApplication(ctx, db.CreateOAuthApplicationParams{
			Name:         req.ClientName,
			Uid:          clientID,
			Secret:       clientSecret,
			RedirectUri:  req.RedirectURIs,
			Scopes:       req.Scopes,
			Website:      websitePG,
			Confidential: true,
		})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Failed to create application",
			})
		}

		return c.JSON(http.StatusOK, CreateAppResponse{
			ID:           strconv.FormatInt(dbApp.ID, 10),
			Name:         dbApp.Name,
			Website:      website,
			RedirectURI:  dbApp.RedirectUri,
			ClientID:     dbApp.Uid,
			ClientSecret: dbApp.Secret,
			VapidKey:     "",
		})
	}

	return c.JSON(http.StatusOK, CreateAppResponse{
		ID:           "1",
		Name:         req.ClientName,
		Website:      website,
		RedirectURI:  req.RedirectURIs,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		VapidKey:     "",
	})
}

// VerifyCredentials handles GET /api/v1/apps/verify_credentials
func (h *AppsHandler) VerifyCredentials(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]interface{}{
		"name":    "Test App",
		"website": nil,
	})
}

// Register registers the apps routes
func (h *AppsHandler) Register(g *echo.Group) {
	g.POST("/apps", h.Create)
}
