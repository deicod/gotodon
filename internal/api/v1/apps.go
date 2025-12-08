// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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
func (h *AppsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateAppRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ClientName == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Name can't be blank")
		return
	}
	if req.RedirectURIs == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Redirect URI can't be blank")
		return
	}

	scopes := oauth.ParseScopes(req.Scopes)
	if err := oauth.ValidateScopes(scopes); err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Scopes is not included in the list")
		return
	}

	clientID, clientSecret, err := oauth.GenerateClientCredentials()
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to generate credentials")
		return
	}

	var website *string
	if req.Website != "" {
		website = &req.Website
	}

	ctx := r.Context()

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
			web.JSONError(w, http.StatusInternalServerError, "Failed to create application")
			return
		}

		web.JSON(w, http.StatusOK, CreateAppResponse{
			ID:           strconv.FormatInt(dbApp.ID, 10),
			Name:         dbApp.Name,
			Website:      website,
			RedirectURI:  dbApp.RedirectUri,
			ClientID:     dbApp.Uid,
			ClientSecret: dbApp.Secret,
			VapidKey:     "",
		})
		return
	}

	web.JSON(w, http.StatusOK, CreateAppResponse{
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
func (h *AppsHandler) VerifyCredentials(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, map[string]interface{}{
		"name":    "Test App",
		"website": nil,
	})
}

// RegisterRoutes registers the apps routes
func (h *AppsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/apps", h.Create)
	mux.HandleFunc("GET /api/v1/apps/verify_credentials", h.VerifyCredentials)
}
