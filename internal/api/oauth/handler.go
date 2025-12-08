// Package oauth provides OAuth 2.0 endpoints for Mastodon-compatible authentication.
package oauth

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/oauth"
)

// Handler handles OAuth endpoints
type Handler struct {
	queries *db.Queries
}

// NewHandler creates a new OAuth handler
func NewHandler(queries *db.Queries) *Handler {
	return &Handler{queries: queries}
}

// TokenRequest is the request body for POST /oauth/token
type TokenRequest struct {
	GrantType    string `json:"grant_type" form:"grant_type"`
	Code         string `json:"code" form:"code"`
	ClientID     string `json:"client_id" form:"client_id"`
	ClientSecret string `json:"client_secret" form:"client_secret"`
	RedirectURI  string `json:"redirect_uri" form:"redirect_uri"`
	Scope        string `json:"scope" form:"scope"`
	Username     string `json:"username" form:"username"`
	Password     string `json:"password" form:"password"`
	RefreshToken string `json:"refresh_token" form:"refresh_token"`
}

// TokenResponse is the response for POST /oauth/token
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
	ExpiresIn    *int   `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// AuthorizeRequest is the request for GET /oauth/authorize
type AuthorizeRequest struct {
	ResponseType string `query:"response_type"`
	ClientID     string `query:"client_id"`
	RedirectURI  string `query:"redirect_uri"`
	Scope        string `query:"scope"`
	State        string `query:"state"`
	ForceLogin   bool   `query:"force_login"`
	Lang         string `query:"lang"`
}

// Token handles POST /oauth/token
func (h *Handler) Token(c echo.Context) error {
	var req TokenRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error":             "invalid_request",
			"error_description": "Invalid request body",
		})
	}

	ctx := context.Background()

	switch req.GrantType {
	case "authorization_code":
		return h.handleAuthorizationCode(c, ctx, req)
	case "client_credentials":
		return h.handleClientCredentials(c, ctx, req)
	case "password":
		return h.handlePassword(c, ctx, req)
	case "refresh_token":
		return h.handleRefreshToken(c, ctx, req)
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error":             "unsupported_grant_type",
			"error_description": "The grant type is not supported",
		})
	}
}

func (h *Handler) handleClientCredentials(c echo.Context, ctx context.Context, req TokenRequest) error {
	// Validate client
	app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil || app.Secret != req.ClientSecret {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":             "invalid_client",
			"error_description": "Invalid client credentials",
		})
	}

	// Validate scopes
	scopes := oauth.ParseScopes(req.Scope)
	if err := oauth.ValidateScopes(scopes); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error":             "invalid_scope",
			"error_description": "The requested scope is invalid",
		})
	}

	// Generate token
	token, err := oauth.GenerateToken(32)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error":             "server_error",
			"error_description": "Failed to generate token",
		})
	}

	// Save token
	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:         token,
		Scopes:        pgtype.Text{String: oauth.NormalizeScopes(scopes), Valid: true},
		ApplicationID: pgtype.Int8{Int64: app.ID, Valid: true},
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error":             "server_error",
			"error_description": "Failed to save token",
		})
	}

	return c.JSON(http.StatusOK, TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		Scope:       oauth.NormalizeScopes(scopes),
		CreatedAt:   time.Now().Unix(),
	})
}

func (h *Handler) handleAuthorizationCode(c echo.Context, ctx context.Context, req TokenRequest) error {
	// For now, return not implemented - requires session/login flow
	return c.JSON(http.StatusBadRequest, map[string]string{
		"error":             "invalid_grant",
		"error_description": "Authorization code flow requires user session",
	})
}

func (h *Handler) handlePassword(c echo.Context, ctx context.Context, req TokenRequest) error {
	// Validate client
	app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil || app.Secret != req.ClientSecret {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":             "invalid_client",
			"error_description": "Invalid client credentials",
		})
	}

	// Lookup user
	user, err := h.queries.GetUserByEmail(ctx, req.Username)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid credentials",
		})
	}

	// Verify password (simplified - real implementation needs bcrypt)
	if user.EncryptedPassword != req.Password {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid credentials",
		})
	}

	// Generate tokens
	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
	}

	refreshToken, err := oauth.GenerateToken(32)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
	}

	scopes := oauth.ParseScopes(req.Scope)
	if err := oauth.ValidateScopes(scopes); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid_scope",
		})
	}

	// Save token
	expiresIn := int32(7200) // 2 hours
	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:           accessToken,
		RefreshToken:    pgtype.Text{String: refreshToken, Valid: true},
		ExpiresIn:       pgtype.Int4{Int32: expiresIn, Valid: true},
		Scopes:          pgtype.Text{String: oauth.NormalizeScopes(scopes), Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: user.ID, Valid: true},
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
	}

	expiresInInt := int(expiresIn)
	return c.JSON(http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		Scope:        oauth.NormalizeScopes(scopes),
		CreatedAt:    time.Now().Unix(),
		ExpiresIn:    &expiresInInt,
		RefreshToken: refreshToken,
	})
}

func (h *Handler) handleRefreshToken(c echo.Context, ctx context.Context, req TokenRequest) error {
	// Lookup token
	oldToken, err := h.queries.GetOAuthAccessTokenByRefresh(ctx, pgtype.Text{String: req.RefreshToken, Valid: true})
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid refresh token",
		})
	}

	// Revoke old token
	_ = h.queries.RevokeOAuthAccessToken(ctx, oldToken.Token)

	// Generate new tokens
	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
	}

	refreshToken, err := oauth.GenerateToken(32)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
	}

	expiresIn := int32(7200)
	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:           accessToken,
		RefreshToken:    pgtype.Text{String: refreshToken, Valid: true},
		ExpiresIn:       pgtype.Int4{Int32: expiresIn, Valid: true},
		Scopes:          oldToken.Scopes,
		ApplicationID:   oldToken.ApplicationID,
		ResourceOwnerID: oldToken.ResourceOwnerID,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
	}

	scopes := ""
	if oldToken.Scopes.Valid {
		scopes = oldToken.Scopes.String
	}

	expiresInInt := int(expiresIn)
	return c.JSON(http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		Scope:        scopes,
		CreatedAt:    time.Now().Unix(),
		ExpiresIn:    &expiresInInt,
		RefreshToken: refreshToken,
	})
}

// Revoke handles POST /oauth/revoke
func (h *Handler) Revoke(c echo.Context) error {
	var req struct {
		Token        string `json:"token" form:"token"`
		ClientID     string `json:"client_id" form:"client_id"`
		ClientSecret string `json:"client_secret" form:"client_secret"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid_request",
		})
	}

	ctx := context.Background()

	// Verify client if provided
	if req.ClientID != "" {
		app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
		if err != nil || app.Secret != req.ClientSecret {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "invalid_client",
			})
		}
	}

	// Revoke the token
	_ = h.queries.RevokeOAuthAccessToken(ctx, req.Token)

	return c.JSON(http.StatusOK, map[string]any{})
}

// Authorize handles GET /oauth/authorize
func (h *Handler) Authorize(c echo.Context) error {
	var req AuthorizeRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid_request",
		})
	}

	ctx := context.Background()

	// Validate client
	_, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error":             "invalid_client",
			"error_description": "Unknown application",
		})
	}

	// For now, return a simple HTML form (real implementation needs session handling)
	html := `<!DOCTYPE html>
<html>
<head><title>Authorize</title></head>
<body>
<h1>Authorization Required</h1>
<p>Application is requesting access to your account.</p>
<form method="POST" action="/oauth/authorize">
  <input type="hidden" name="client_id" value="` + req.ClientID + `">
  <input type="hidden" name="redirect_uri" value="` + req.RedirectURI + `">
  <input type="hidden" name="scope" value="` + req.Scope + `">
  <input type="hidden" name="state" value="` + req.State + `">
  <input type="hidden" name="response_type" value="` + req.ResponseType + `">
  <input type="email" name="email" placeholder="Email" required>
  <input type="password" name="password" placeholder="Password" required>
  <button type="submit">Authorize</button>
</form>
</body>
</html>`

	return c.HTML(http.StatusOK, html)
}

// AuthorizePost handles POST /oauth/authorize
func (h *Handler) AuthorizePost(c echo.Context) error {
	clientID := c.FormValue("client_id")
	redirectURI := c.FormValue("redirect_uri")
	scope := c.FormValue("scope")
	state := c.FormValue("state")
	email := c.FormValue("email")
	password := c.FormValue("password")

	ctx := context.Background()

	// Validate client
	app, err := h.queries.GetOAuthApplicationByUID(ctx, clientID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid_client",
		})
	}

	// Authenticate user
	user, err := h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		return c.HTML(http.StatusUnauthorized, "Invalid credentials")
	}

	// Verify password (simplified)
	if user.EncryptedPassword != password {
		return c.HTML(http.StatusUnauthorized, "Invalid credentials")
	}

	// Generate authorization code
	code, err := oauth.GenerateToken(32)
	if err != nil {
		return c.HTML(http.StatusInternalServerError, "Server error")
	}

	// Store the code temporarily (in production, use a proper store)
	// For now, generate a token directly
	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		return c.HTML(http.StatusInternalServerError, "Server error")
	}

	scopes := oauth.ParseScopes(scope)
	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:           accessToken,
		Scopes:          pgtype.Text{String: oauth.NormalizeScopes(scopes), Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: user.ID, Valid: true},
	})
	if err != nil {
		return c.HTML(http.StatusInternalServerError, "Server error")
	}

	// Redirect with code (in production, this would be a proper code exchange)
	_ = code
	redirect := redirectURI + "?code=" + accessToken
	if state != "" {
		redirect += "&state=" + state
	}

	return c.Redirect(http.StatusFound, redirect)
}

// VerifyCredentials validates the current token and returns app info
func (h *Handler) VerifyCredentials(c echo.Context) error {
	token := c.Request().Header.Get("Authorization")
	if token == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Strip "Bearer " prefix
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	ctx := context.Background()

	dbToken, err := h.queries.GetOAuthAccessToken(ctx, token)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	if !dbToken.ApplicationID.Valid {
		return c.JSON(http.StatusOK, map[string]any{
			"name":    "Unknown",
			"website": nil,
		})
	}

	app, err := h.queries.GetOAuthApplication(ctx, dbToken.ApplicationID.Int64)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"name":    "Unknown",
			"website": nil,
		})
	}

	var website *string
	if app.Website.Valid {
		website = &app.Website.String
	}

	return c.JSON(http.StatusOK, map[string]any{
		"id":      strconv.FormatInt(app.ID, 10),
		"name":    app.Name,
		"website": website,
	})
}

// RegisterRoutes registers OAuth routes
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	oauth := e.Group("/oauth")
	oauth.POST("/token", h.Token)
	oauth.POST("/revoke", h.Revoke)
	oauth.GET("/authorize", h.Authorize)
	oauth.POST("/authorize", h.AuthorizePost)
}
