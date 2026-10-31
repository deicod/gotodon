// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// AppsHandler handles OAuth application registration
type AppsHandler struct {
	queries appsQueries
	cfg     *config.Config
}

const (
	appNameLimit        = 60
	appRedirectURILimit = 2000
	appWebsiteLimit     = 2000
)

type appsQueries interface {
	CreateOAuthApplication(ctx context.Context, arg db.CreateOAuthApplicationParams) (db.OauthApplication, error)
	GetOAuthApplication(ctx context.Context, id int64) (db.OauthApplication, error)
}

// NewAppsHandler creates a new apps handler
func NewAppsHandler(queries appsQueries, cfg *config.Config) *AppsHandler {
	return &AppsHandler{queries: queries, cfg: cfg}
}

func (h *AppsHandler) useDB() bool {
	return h.queries != nil
}

func (h *AppsHandler) vapidKey() string {
	if h.cfg == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.Secrets.VAPIDPublic)
}

// CreateAppRequest is the request body for POST /api/v1/apps
type CreateAppRequest struct {
	ClientName   string                `json:"client_name" form:"client_name"`
	RedirectURIs createAppRedirectURIs `json:"redirect_uris" form:"redirect_uris"`
	Scopes       createAppScopes       `json:"scopes" form:"scopes"`
	Website      string                `json:"website" form:"website"`
}

type createAppRedirectURIs []string

type createAppScopes string

func (s *createAppScopes) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*s = ""
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = createAppScopes(single)
		return nil
	}

	// Mastodon bug-compat: if scopes is sent as a JSON array, treat it like omitted.
	var array []json.RawMessage
	if err := json.Unmarshal(data, &array); err == nil {
		*s = ""
		return nil
	}

	return errors.New("scopes must be a string")
}

func (s *createAppScopes) UnmarshalText(text []byte) error {
	*s = createAppScopes(string(text))
	return nil
}

func (r *createAppRedirectURIs) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*r = nil
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*r = createAppRedirectURIs{single}
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*r = createAppRedirectURIs(many)
		return nil
	}

	return errors.New("redirect_uris must be a string or array of strings")
}

// CreateAppResponse is the response for POST /api/v1/apps
type CreateAppResponse struct {
	ID                    string   `json:"id"`
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret"`
	ClientSecretExpiresAt int64    `json:"client_secret_expires_at"`
	Name                  string   `json:"name"`
	Website               *string  `json:"website"`
	Scopes                []string `json:"scopes"`
	RedirectURIs          []string `json:"redirect_uris"`
	RedirectURI           string   `json:"redirect_uri"`
	VapidKey              string   `json:"vapid_key,omitempty"`
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
	if utf8.RuneCountInString(req.ClientName) > appNameLimit {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Name is too long")
		return
	}

	rawRedirectURI, redirectURIs := normalizeCreateAppRedirectURIs(req.RedirectURIs)
	if len(redirectURIs) == 0 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Redirect URI is required")
		return
	}
	if utf8.RuneCountInString(rawRedirectURI) > appRedirectURILimit {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Redirect URI is too long")
		return
	}

	// Validate Redirect URIs
	for _, uri := range redirectURIs {
		if _, err := url.ParseRequestURI(uri); err != nil {
			web.JSONError(w, http.StatusUnprocessableEntity, "Invalid redirect URI: "+uri)
			return
		}
	}

	if utf8.RuneCountInString(req.Website) > appWebsiteLimit {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Website is too long")
		return
	}
	if req.Website != "" && !isValidWebsiteURL(req.Website) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Website is invalid")
		return
	}

	scopes := oauth.ParseScopes(string(req.Scopes))
	if err := oauth.ValidateScopes(scopes); err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Scopes is not included in the list")
		return
	}
	normalizedScopes := oauth.NormalizeScopes(scopes)

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
			RedirectUri:  rawRedirectURI,
			Scopes:       normalizedScopes,
			Website:      websitePG,
			OwnerType:    pgtype.Text{Valid: false},
			OwnerID:      pgtype.Int8{Valid: false},
			Confidential: true,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create application")
			return
		}

		normalizedScopeList := oauth.ParseScopes(dbApp.Scopes)
		redirectURIs := splitAppRedirectURIs(dbApp.RedirectUri)

		web.JSON(w, http.StatusOK, CreateAppResponse{
			ID:                    strconv.FormatInt(dbApp.ID, 10),
			ClientID:              dbApp.Uid,
			ClientSecret:          dbApp.Secret,
			ClientSecretExpiresAt: 0,
			Name:                  dbApp.Name,
			Website:               website,
			Scopes:                normalizedScopeList,
			RedirectURIs:          redirectURIs,
			RedirectURI:           dbApp.RedirectUri,
			VapidKey:              h.vapidKey(),
		})
		return
	}

	normalizedScopeList := oauth.ParseScopes(normalizedScopes)

	web.JSON(w, http.StatusOK, CreateAppResponse{
		ID:                    "1",
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		ClientSecretExpiresAt: 0,
		Name:                  req.ClientName,
		Website:               website,
		Scopes:                normalizedScopeList,
		RedirectURIs:          redirectURIs,
		RedirectURI:           rawRedirectURI,
		VapidKey:              h.vapidKey(),
	})
}

func normalizeCreateAppRedirectURIs(rawInput createAppRedirectURIs) (string, []string) {
	redirectURIs := make([]string, 0, len(rawInput))
	for _, value := range rawInput {
		redirectURIs = append(redirectURIs, splitAppRedirectURIs(value)...)
	}

	return strings.Join(redirectURIs, "\n"), redirectURIs
}

func isValidWebsiteURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

type appCredentialsResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Website      *string  `json:"website"`
	Scopes       []string `json:"scopes"`
	RedirectURIs []string `json:"redirect_uris"`
	RedirectURI  string   `json:"redirect_uri"`
	VapidKey     string   `json:"vapid_key,omitempty"`
}

// VerifyCredentials handles GET /api/v1/apps/verify_credentials
func (h *AppsHandler) VerifyCredentials(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	appID, ok := middleware.GetAppID(ctx)
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if appID == 0 {
		web.JSON(w, http.StatusOK, appCredentialsResponse{
			ID:           "0",
			Name:         "Test App",
			Website:      nil,
			Scopes:       oauth.ParseScopes(middleware.GetScopes(ctx)),
			RedirectURIs: []string{},
			RedirectURI:  "",
			VapidKey:     h.vapidKey(),
		})
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Database not available")
		return
	}

	app, err := h.queries.GetOAuthApplication(ctx, appID)
	if err != nil {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	redirectURIs := splitAppRedirectURIs(app.RedirectUri)
	firstRedirectURI := ""
	if len(redirectURIs) > 0 {
		firstRedirectURI = redirectURIs[0]
	}

	var website *string
	if app.Website.Valid {
		trimmedWebsite := strings.TrimSpace(app.Website.String)
		if trimmedWebsite != "" {
			website = &trimmedWebsite
		}
	}

	web.JSON(w, http.StatusOK, appCredentialsResponse{
		ID:           strconv.FormatInt(app.ID, 10),
		Name:         app.Name,
		Website:      website,
		Scopes:       oauth.ParseScopes(app.Scopes),
		RedirectURIs: redirectURIs,
		RedirectURI:  firstRedirectURI,
		VapidKey:     h.vapidKey(),
	})
}

func splitAppRedirectURIs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})

	uris := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed != "" {
			uris = append(uris, trimmed)
		}
	}
	return uris
}

// RegisterRoutes registers the apps routes
func (h *AppsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/apps", h.Create)
	mux.HandleFunc("GET /api/v1/apps/verify_credentials", h.VerifyCredentials)
}
