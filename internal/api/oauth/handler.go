// Package oauth provides OAuth 2.0 endpoints for Mastodon-compatible authentication.
package oauth

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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
func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	var req TokenRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error":             "invalid_request",
			"error_description": "Invalid request body",
		})
		return
	}

	ctx := context.Background()

	switch req.GrantType {
	case "authorization_code":
		h.handleAuthorizationCode(w, r, ctx, req)
	case "client_credentials":
		h.handleClientCredentials(w, r, ctx, req)
	case "password":
		h.handlePassword(w, r, ctx, req)
	case "refresh_token":
		h.handleRefreshToken(w, r, ctx, req)
	default:
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error":             "unsupported_grant_type",
			"error_description": "The grant type is not supported",
		})
	}
}

func (h *Handler) handleClientCredentials(w http.ResponseWriter, r *http.Request, ctx context.Context, req TokenRequest) {
	// Validate client
	app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil || app.Secret != req.ClientSecret {
		web.JSON(w, http.StatusUnauthorized, map[string]string{
			"error":             "invalid_client",
			"error_description": "Invalid client credentials",
		})
		return
	}

	// Validate scopes
	scopes := oauth.ParseScopes(req.Scope)
	if err := oauth.ValidateScopes(scopes); err != nil {
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error":             "invalid_scope",
			"error_description": "The requested scope is invalid",
		})
		return
	}

	// Generate token
	token, err := oauth.GenerateToken(32)
	if err != nil {
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error":             "server_error",
			"error_description": "Failed to generate token",
		})
		return
	}

	// Save token
	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:         token,
		Scopes:        pgtype.Text{String: oauth.NormalizeScopes(scopes), Valid: true},
		ApplicationID: pgtype.Int8{Int64: app.ID, Valid: true},
	})
	if err != nil {
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error":             "server_error",
			"error_description": "Failed to save token",
		})
		return
	}

	web.JSON(w, http.StatusOK, TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		Scope:       oauth.NormalizeScopes(scopes),
		CreatedAt:   time.Now().Unix(),
	})
}

func (h *Handler) handleAuthorizationCode(w http.ResponseWriter, r *http.Request, ctx context.Context, req TokenRequest) {
	// For now, return not implemented - requires session/login flow
	web.JSON(w, http.StatusBadRequest, map[string]string{
		"error":             "invalid_grant",
		"error_description": "Authorization code flow requires user session",
	})
}

func (h *Handler) handlePassword(w http.ResponseWriter, r *http.Request, ctx context.Context, req TokenRequest) {
	// Validate client
	app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil || app.Secret != req.ClientSecret {
		web.JSON(w, http.StatusUnauthorized, map[string]string{
			"error":             "invalid_client",
			"error_description": "Invalid client credentials",
		})
		return
	}

	// Lookup user
	user, err := h.queries.GetUserByEmail(ctx, req.Username)
	if err != nil {
		web.JSON(w, http.StatusUnauthorized, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid credentials",
		})
		return
	}

	// Verify password (simplified - real implementation needs bcrypt)
	if user.EncryptedPassword != req.Password {
		web.JSON(w, http.StatusUnauthorized, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid credentials",
		})
		return
	}

	// Generate tokens
	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
		return
	}

	refreshToken, err := oauth.GenerateToken(32)
	if err != nil {
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
		return
	}

	scopes := oauth.ParseScopes(req.Scope)
	if err := oauth.ValidateScopes(scopes); err != nil {
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_scope",
		})
		return
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
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
		return
	}

	expiresInInt := int(expiresIn)
	web.JSON(w, http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		Scope:        oauth.NormalizeScopes(scopes),
		CreatedAt:    time.Now().Unix(),
		ExpiresIn:    &expiresInInt,
		RefreshToken: refreshToken,
	})
}

func (h *Handler) handleRefreshToken(w http.ResponseWriter, r *http.Request, ctx context.Context, req TokenRequest) {
	// Lookup token
	oldToken, err := h.queries.GetOAuthAccessTokenByRefresh(ctx, pgtype.Text{String: req.RefreshToken, Valid: true})
	if err != nil {
		web.JSON(w, http.StatusUnauthorized, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid refresh token",
		})
		return
	}

	// Revoke old token
	_ = h.queries.RevokeOAuthAccessToken(ctx, oldToken.Token)

	// Generate new tokens
	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
		return
	}

	refreshToken, err := oauth.GenerateToken(32)
	if err != nil {
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
		return
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
		web.JSON(w, http.StatusInternalServerError, map[string]string{
			"error": "server_error",
		})
		return
	}

	scopes := ""
	if oldToken.Scopes.Valid {
		scopes = oldToken.Scopes.String
	}

	expiresInInt := int(expiresIn)
	web.JSON(w, http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		Scope:        scopes,
		CreatedAt:    time.Now().Unix(),
		ExpiresIn:    &expiresInInt,
		RefreshToken: refreshToken,
	})
}

// Revoke handles POST /oauth/revoke
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token        string `json:"token" form:"token"`
		ClientID     string `json:"client_id" form:"client_id"`
		ClientSecret string `json:"client_secret" form:"client_secret"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_request",
		})
		return
	}

	ctx := context.Background()

	// Verify client if provided
	if req.ClientID != "" {
		app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
		if err != nil || app.Secret != req.ClientSecret {
			web.JSON(w, http.StatusUnauthorized, map[string]string{
				"error": "invalid_client",
			})
			return
		}
	}

	// Revoke the token
	_ = h.queries.RevokeOAuthAccessToken(ctx, req.Token)

	web.JSON(w, http.StatusOK, map[string]any{})
}

// Authorize handles GET /oauth/authorize
func (h *Handler) Authorize(w http.ResponseWriter, r *http.Request) {
	var req AuthorizeRequest
	req.ResponseType = web.QueryParam(r, "response_type")
	req.ClientID = web.QueryParam(r, "client_id")
	req.RedirectURI = web.QueryParam(r, "redirect_uri")
	req.Scope = web.QueryParam(r, "scope")
	req.State = web.QueryParam(r, "state")

	// c.Bind mostly works for JSON/Form. For query params in struct, web.Bind might not support it directly if designed for Body.
	// `web.Bind` implemented before uses JSON/Form.
	// For GET, we parse query params manually as above.

	ctx := context.Background()

	// Validate client
	_, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil {
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error":             "invalid_client",
			"error_description": "Unknown application",
		})
		return
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

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

// AuthorizePost handles POST /oauth/authorize
func (h *Handler) AuthorizePost(w http.ResponseWriter, r *http.Request) {
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	scope := r.FormValue("scope")
	state := r.FormValue("state")
	email := r.FormValue("email")
	password := r.FormValue("password")

	ctx := context.Background()

	// Validate client
	app, err := h.queries.GetOAuthApplicationByUID(ctx, clientID)
	if err != nil {
		web.JSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_client",
		})
		return
	}

	// Authenticate user
	user, err := h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid credentials"))
		return
	}

	// Verify password (simplified)
	if user.EncryptedPassword != password {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Invalid credentials"))
		return
	}

	// Generate authorization code
	code, err := oauth.GenerateToken(32)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Server error"))
		return
	}

	// Store the code temporarily (in production, use a proper store)
	// For now, generate a token directly
	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Server error"))
		return
	}

	scopes := oauth.ParseScopes(scope)
	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:           accessToken,
		Scopes:          pgtype.Text{String: oauth.NormalizeScopes(scopes), Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: user.ID, Valid: true},
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Server error"))
		return
	}

	// Redirect with code (in production, this would be a proper code exchange)
	_ = code
	redirect := redirectURI + "?code=" + accessToken
	if state != "" {
		redirect += "&state=" + state
	}

	http.Redirect(w, r, redirect, http.StatusFound)
}

// VerifyCredentials validates the current token and returns app info
func (h *Handler) VerifyCredentials(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// Strip "Bearer " prefix
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	ctx := context.Background()

	dbToken, err := h.queries.GetOAuthAccessToken(ctx, token)
	if err != nil {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if !dbToken.ApplicationID.Valid {
		web.JSON(w, http.StatusOK, map[string]any{
			"name":    "Unknown",
			"website": nil,
		})
		return
	}

	app, err := h.queries.GetOAuthApplication(ctx, dbToken.ApplicationID.Int64)
	if err != nil {
		web.JSON(w, http.StatusOK, map[string]any{
			"name":    "Unknown",
			"website": nil,
		})
		return
	}

	var website *string
	if app.Website.Valid {
		website = &app.Website.String
	}

	web.JSON(w, http.StatusOK, map[string]any{
		"id":      strconv.FormatInt(app.ID, 10),
		"name":    app.Name,
		"website": website,
	})
}

// RegisterRoutes registers OAuth routes
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /oauth/token", h.Token)
	mux.HandleFunc("POST /oauth/revoke", h.Revoke)
	mux.HandleFunc("GET /oauth/authorize", h.Authorize)
	mux.HandleFunc("POST /oauth/authorize", h.AuthorizePost)
	// VerifyCredentials is usually unused in standard OAuth flow but kept for API
}
