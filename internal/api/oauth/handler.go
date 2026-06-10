// Package oauth provides OAuth 2.0 endpoints for Mastodon-compatible authentication.
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	apiv1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

const (
	authorizationCodeTTL        = 10 * time.Minute
	pkceMethodS256              = "S256"
	explicitGrantRedirectPrefix = "\x1egotodon-explicit-redirect\x1e"
	grantTypeAuthorizationCode  = "authorization_code"
	grantTypeClientCredentials  = "client_credentials"
	grantTypeRefreshToken       = "refresh_token"
	responseTypeCode            = "code"
	responseModeQuery           = "query"
)

type oauthQuerier interface {
	GetOAuthApplication(ctx context.Context, id int64) (db.OauthApplication, error)
	GetOAuthApplicationByUID(ctx context.Context, uid string) (db.OauthApplication, error)
	GetLatestActiveOAuthAccessTokenByGrant(ctx context.Context, arg db.GetLatestActiveOAuthAccessTokenByGrantParams) (db.OauthAccessToken, error)
	CreateOAuthAccessToken(ctx context.Context, arg db.CreateOAuthAccessTokenParams) (db.OauthAccessToken, error)
	GetOAuthAccessToken(ctx context.Context, token string) (db.OauthAccessToken, error)
	GetOAuthAccessTokenByTokenIncludingRevoked(ctx context.Context, token string) (db.OauthAccessToken, error)
	GetOAuthAccessTokenByRefresh(ctx context.Context, refresh pgtype.Text) (db.OauthAccessToken, error)
	RevokeOAuthAccessToken(ctx context.Context, token string) error
	CreateOAuthAccessGrant(ctx context.Context, arg db.CreateOAuthAccessGrantParams) (db.OauthAccessGrant, error)
	GetOAuthAccessGrantByToken(ctx context.Context, token string) (db.OauthAccessGrant, error)
	RevokeOAuthAccessGrant(ctx context.Context, token string) error
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	DeletePushSubscription(ctx context.Context, accessTokenID pgtype.Int8) error
	CreateLoginActivity(ctx context.Context, arg db.CreateLoginActivityParams) (db.LoginActivity, error)
	CountSuccessfulLoginActivitiesByMaskedIP(ctx context.Context, arg db.CountSuccessfulLoginActivitiesByMaskedIPParams) (int64, error)
	UpdateUserSignIn(ctx context.Context, id int64) error
}

// Handler handles OAuth endpoints
type Handler struct {
	queries     oauthQuerier
	cfg         *config.Config
	killEmitter tokenKillPublisher
	mail        mailer.Deliverer
}

type tokenKillPublisher interface {
	PublishEventToRedis(ctx context.Context, eventType string, payload any, streams []string) error
}

// NewHandler creates a new OAuth handler
func NewHandler(queries oauthQuerier, cfg *config.Config, killEmitter tokenKillPublisher) *Handler {
	return &Handler{queries: queries, cfg: cfg, killEmitter: killEmitter}
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
	CodeVerifier string `json:"code_verifier" form:"code_verifier"`
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
	ResponseType        string `query:"response_type"`
	ClientID            string `query:"client_id"`
	RedirectURI         string `query:"redirect_uri"`
	Scope               string `query:"scope"`
	State               string `query:"state"`
	ForceLogin          bool   `query:"force_login"`
	Lang                string `query:"lang"`
	CodeChallenge       string `query:"code_challenge"`
	CodeChallengeMethod string `query:"code_challenge_method"`
}

type oauthMetadataResponse struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	ResponseModesSupported            []string `json:"response_modes_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	ServiceDocumentation              string   `json:"service_documentation"`
	AppRegistrationEndpoint           string   `json:"app_registration_endpoint"`
}

type authorizeTemplateData struct {
	AppName              string
	ClientID             string
	RequestedRedirectURI string
	RedirectURI          string
	ResponseType         string
	Scope                string
	ScopeLabel           string
	State                string
	CodeChallenge        string
	CodeChallengeMethod  string
}

var authorizeTemplate = template.Must(template.New("authorize").Parse(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Authorize</title>
</head>
<body>
  <h1>Authorization Required</h1>
  <p>{{.AppName}} is requesting access with scopes: {{.ScopeLabel}}</p>
  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="client_id" value="{{.ClientID}}">
    <input type="hidden" name="redirect_uri" value="{{.RequestedRedirectURI}}">
    <input type="hidden" name="scope" value="{{.Scope}}">
    <input type="hidden" name="state" value="{{.State}}">
    <input type="hidden" name="response_type" value="{{.ResponseType}}">
    <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
    <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
    <div>
      <label>Email <input type="email" name="email" required></label>
    </div>
    <div>
      <label>Password <input type="password" name="password" required></label>
    </div>
    <div>
      <button type="submit" name="approve" value="true">Authorize</button>
      <button type="submit" name="approve" value="false">Deny</button>
    </div>
  </form>
</body>
</html>`))

// Token handles POST /oauth/token
func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	req, err := bindTokenRequest(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	switch req.GrantType {
	case grantTypeAuthorizationCode:
		h.handleAuthorizationCode(w, r, req)
	case grantTypeClientCredentials:
		h.handleClientCredentials(w, r, req)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "The grant type is not supported")
	}
}

func (h *Handler) handleClientCredentials(w http.ResponseWriter, r *http.Request, req TokenRequest) {
	ctx := r.Context()

	app, err := h.validateClient(ctx, req.ClientID, req.ClientSecret)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "Invalid client credentials")
		return
	}

	scopes, normalized, err := normalizeScopes(req.Scope)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "The requested scope is invalid")
		return
	}
	if err := h.ensureAppScopes(app, scopes); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "The requested scope is invalid")
		return
	}

	resp, err := h.issueAccessToken(ctx, app, nil, normalized, true)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to generate token")
		return
	}

	web.JSON(w, http.StatusOK, resp)
}

func (h *Handler) handleAuthorizationCode(w http.ResponseWriter, r *http.Request, req TokenRequest) {
	if req.Code == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Missing authorization code")
		return
	}

	ctx := r.Context()
	app, err := h.validateClient(ctx, req.ClientID, req.ClientSecret)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "Invalid client credentials")
		return
	}

	grant, err := h.queries.GetOAuthAccessGrantByToken(ctx, req.Code)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Invalid authorization code")
		return
	}
	if grant.ApplicationID != app.ID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Invalid authorization code")
		return
	}
	grantRedirectURI, grantRedirectExplicit := decodeGrantRedirectBinding(grant.RedirectUri)
	if grantRedirectExplicit {
		if grantRedirectURI != req.RedirectURI {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Redirect URI mismatch")
			return
		}
	} else if grantRedirectURI != "" {
		if req.RedirectURI == "" {
			allowed := splitRedirectURIs(app.RedirectUri)
			if len(allowed) == 1 && allowed[0] == grantRedirectURI {
				// Compatibility for pre-patch grants where a single app redirect URI
				// was persisted even when authorize omitted redirect_uri.
			} else {
				writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Redirect URI mismatch")
				return
			}
		} else if grantRedirectURI != req.RedirectURI {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Redirect URI mismatch")
			return
		}
	} else if strings.TrimSpace(req.RedirectURI) != "" {
		if _, err := selectRedirectURI(app.RedirectUri, req.RedirectURI); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Redirect URI mismatch")
			return
		}
	}
	if grant.CreatedAt.Valid {
		expiresAt := grant.CreatedAt.Time.Add(time.Duration(grant.ExpiresIn) * time.Second)
		if time.Now().After(expiresAt) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Authorization code expired")
			return
		}
	}
	if err := verifyPKCE(grant, req.CodeVerifier); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "Invalid code verifier")
		return
	}

	scope := strings.TrimSpace(grant.Scopes.String)
	if scope == "" {
		scope = oauth.DefaultScopes
	}

	resp, err := h.issueAccessToken(ctx, app, &grant.ResourceOwnerID, scope, true)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to generate token")
		return
	}

	_ = h.queries.RevokeOAuthAccessGrant(ctx, grant.Token)
	web.JSON(w, http.StatusOK, resp)
}

// Revoke handles POST /oauth/revoke
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token        string `json:"token" form:"token"`
		ClientID     string `json:"client_id" form:"client_id"`
		ClientSecret string `json:"client_secret" form:"client_secret"`
	}
	if err := web.Bind(r, &req); err != nil {
		_ = r.ParseForm()
		req.Token = r.FormValue("token")
		req.ClientID = r.FormValue("client_id")
		req.ClientSecret = r.FormValue("client_secret")
	}

	ctx := r.Context()

	app, err := h.validateClient(ctx, req.ClientID, req.ClientSecret)
	if err != nil {
		writeOAuthError(w, http.StatusForbidden, "unauthorized_client", "You are not authorized to revoke this token")
		return
	}

	if req.Token == "" {
		web.JSON(w, http.StatusOK, map[string]any{})
		return
	}

	// Try access token, then refresh token.
	if token, err := h.queries.GetOAuthAccessToken(ctx, req.Token); err == nil {
		if token.ApplicationID.Valid && token.ApplicationID.Int64 != app.ID {
			writeOAuthError(w, http.StatusForbidden, "unauthorized_client", "You are not authorized to revoke this token")
			return
		}
		if token.ApplicationID.Valid && token.ApplicationID.Int64 == app.ID && h.queries.RevokeOAuthAccessToken(ctx, token.Token) == nil {
			h.publishTokenKill(ctx, token.ID)
			if token.ID != 0 {
				_ = h.queries.DeletePushSubscription(ctx, pgtype.Int8{Int64: token.ID, Valid: true})
			}
		}
	} else if token, err := h.queries.GetOAuthAccessTokenByRefresh(ctx, pgtype.Text{String: req.Token, Valid: true}); err == nil {
		if token.ApplicationID.Valid && token.ApplicationID.Int64 != app.ID {
			writeOAuthError(w, http.StatusForbidden, "unauthorized_client", "You are not authorized to revoke this token")
			return
		}
		if token.ApplicationID.Valid && token.ApplicationID.Int64 == app.ID && h.queries.RevokeOAuthAccessToken(ctx, token.Token) == nil {
			h.publishTokenKill(ctx, token.ID)
			if token.ID != 0 {
				_ = h.queries.DeletePushSubscription(ctx, pgtype.Int8{Int64: token.ID, Valid: true})
			}
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) publishTokenKill(_ context.Context, tokenID int64) {
	if h.killEmitter == nil || tokenID <= 0 {
		return
	}
	publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.killEmitter.PublishEventToRedis(publishCtx, streaming.EventKill, "", []string{streaming.AccessTokenStreamKey(tokenID)})
}

// Authorize handles GET /oauth/authorize
func (h *Handler) Authorize(w http.ResponseWriter, r *http.Request) {
	req := AuthorizeRequest{
		ResponseType:        web.QueryParam(r, "response_type"),
		ClientID:            web.QueryParam(r, "client_id"),
		RedirectURI:         web.QueryParam(r, "redirect_uri"),
		Scope:               web.QueryParam(r, "scope"),
		State:               web.QueryParam(r, "state"),
		CodeChallenge:       web.QueryParam(r, "code_challenge"),
		CodeChallengeMethod: web.QueryParam(r, "code_challenge_method"),
	}

	ctx := r.Context()
	app, err := h.queries.GetOAuthApplicationByUID(ctx, req.ClientID)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "Unknown application")
		return
	}

	redirectURI, err := selectRedirectURI(app.RedirectUri, req.RedirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid redirect URI")
		return
	}

	if req.ResponseType != responseTypeCode {
		redirectWithError(w, r, redirectURI, "unsupported_response_type", "Unsupported response type", req.State)
		return
	}

	scopes, normalized, err := normalizeScopes(req.Scope)
	if err != nil {
		redirectWithError(w, r, redirectURI, "invalid_scope", "The requested scope is invalid", req.State)
		return
	}
	if err := h.ensureAppScopes(app, scopes); err != nil {
		redirectWithError(w, r, redirectURI, "invalid_scope", "The requested scope is invalid", req.State)
		return
	}

	if err := validateCodeChallenge(req.CodeChallenge, req.CodeChallengeMethod); err != nil {
		redirectWithError(w, r, redirectURI, "invalid_request", "Unsupported code challenge method", req.State)
		return
	}

	data := authorizeTemplateData{
		AppName:              app.Name,
		ClientID:             req.ClientID,
		RequestedRedirectURI: req.RedirectURI,
		RedirectURI:          redirectURI,
		ResponseType:         req.ResponseType,
		Scope:                normalized,
		ScopeLabel:           normalized,
		State:                req.State,
		CodeChallenge:        req.CodeChallenge,
		CodeChallengeMethod:  normalizedCodeChallengeMethod(req.CodeChallengeMethod),
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_ = authorizeTemplate.Execute(w, data)
}

// AuthorizePost handles POST /oauth/authorize
func (h *Handler) AuthorizePost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	clientID := r.FormValue("client_id")
	requestedRedirectURI := r.FormValue("redirect_uri")
	if strings.TrimSpace(requestedRedirectURI) == "" {
		requestedRedirectURI = ""
	}
	scope := r.FormValue("scope")
	state := r.FormValue("state")
	responseType := r.FormValue("response_type")
	email := r.FormValue("email")
	password := r.FormValue("password")
	approve := r.FormValue("approve")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")

	ctx := r.Context()
	app, err := h.queries.GetOAuthApplicationByUID(ctx, clientID)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "Unknown application")
		return
	}

	redirectURI, err := selectRedirectURI(app.RedirectUri, requestedRedirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid redirect URI")
		return
	}

	if responseType != responseTypeCode {
		redirectWithError(w, r, redirectURI, "unsupported_response_type", "Unsupported response type", state)
		return
	}

	scopes, normalized, err := normalizeScopes(scope)
	if err != nil {
		redirectWithError(w, r, redirectURI, "invalid_scope", "The requested scope is invalid", state)
		return
	}
	if err := h.ensureAppScopes(app, scopes); err != nil {
		redirectWithError(w, r, redirectURI, "invalid_scope", "The requested scope is invalid", state)
		return
	}

	if err := validateCodeChallenge(codeChallenge, codeChallengeMethod); err != nil {
		redirectWithError(w, r, redirectURI, "invalid_request", "Unsupported code challenge method", state)
		return
	}

	if approve != "true" {
		redirectWithError(w, r, redirectURI, "access_denied", "Authorization denied", state)
		return
	}

	user, failureReason, err := h.authenticateUser(ctx, email, password)
	if err != nil {
		h.recordLoginFailure(ctx, r, user, failureReason)
		writeOAuthError(w, http.StatusUnauthorized, "invalid_grant", "Invalid credentials")
		return
	}
	h.recordLoginSuccess(ctx, r, user)

	code, err := oauth.GenerateToken(32)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to generate authorization code")
		return
	}

	expiresIn := int32(authorizationCodeTTL.Seconds())
	scopeValue := pgtype.Text{String: normalized, Valid: true}
	codeChallengeValue := pgtype.Text{Valid: false}
	codeChallengeMethodValue := pgtype.Text{Valid: false}
	if strings.TrimSpace(codeChallenge) != "" {
		codeChallengeValue = pgtype.Text{String: codeChallenge, Valid: true}
		codeChallengeMethodValue = pgtype.Text{String: normalizedCodeChallengeMethod(codeChallengeMethod), Valid: true}
	}

	_, err = h.queries.CreateOAuthAccessGrant(ctx, db.CreateOAuthAccessGrantParams{
		Token:               code,
		ExpiresIn:           expiresIn,
		RedirectUri:         encodeGrantRedirectBinding(requestedRedirectURI),
		Scopes:              scopeValue,
		ApplicationID:       app.ID,
		ResourceOwnerID:     user.ID,
		CodeChallenge:       codeChallengeValue,
		CodeChallengeMethod: codeChallengeMethodValue,
	})
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Failed to store authorization code")
		return
	}

	redirectWithCode(w, r, redirectURI, code, state)
}

// Userinfo handles GET/POST /oauth/userinfo
func (h *Handler) Userinfo(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}
	scopes := middleware.GetScopes(r.Context())
	if scopes == "" || !oauth.HasScope(scopes, "profile") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	ctx := r.Context()
	dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	acc := apiv1.SerializeAccount(h.cfg, dbAcc)
	web.JSON(w, http.StatusOK, map[string]any{
		"iss":                h.issuer(),
		"sub":                acc.URI,
		"name":               acc.DisplayName,
		"preferred_username": acc.Username,
		"profile":            acc.URL,
		"picture":            acc.Avatar,
	})
}

// Metadata handles GET /.well-known/oauth-authorization-server
func (h *Handler) Metadata(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL()
	responseTypesSupported := h.metadataResponseTypesSupported()
	resp := oauthMetadataResponse{
		Issuer:                            h.issuer(),
		AuthorizationEndpoint:             base + "/oauth/authorize",
		TokenEndpoint:                     base + "/oauth/token",
		RevocationEndpoint:                base + "/oauth/revoke",
		UserinfoEndpoint:                  base + "/oauth/userinfo",
		ScopesSupported:                   oauth.SupportedScopes(),
		ResponseTypesSupported:            responseTypesSupported,
		ResponseModesSupported:            metadataResponseModesSupported(responseTypesSupported),
		GrantTypesSupported:               h.metadataGrantTypesSupported(),
		TokenEndpointAuthMethodsSupported: []string{"client_secret_basic", "client_secret_post"},
		CodeChallengeMethodsSupported:     []string{pkceMethodS256},
		ServiceDocumentation:              "https://docs.joinmastodon.org/",
		AppRegistrationEndpoint:           base + "/api/v1/apps",
	}
	web.JSON(w, http.StatusOK, resp)
}

func (h *Handler) metadataResponseTypesSupported() []string {
	return []string{responseTypeCode}
}

func metadataResponseModesSupported(responseTypes []string) []string {
	for _, responseType := range responseTypes {
		if responseType == responseTypeCode {
			return []string{responseModeQuery}
		}
	}
	return nil
}

func (h *Handler) metadataGrantTypesSupported() []string {
	supported := []string{
		grantTypeAuthorizationCode,
		grantTypeClientCredentials,
	}
	if h.refreshTokenGrantSupported() {
		supported = append(supported, grantTypeRefreshToken)
	}
	return supported
}

func (h *Handler) refreshTokenGrantSupported() bool {
	return false
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

	ctx := r.Context()

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
	mux.HandleFunc("GET /oauth/userinfo", h.Userinfo)
	mux.HandleFunc("POST /oauth/userinfo", h.Userinfo)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.Metadata)
	// VerifyCredentials is usually unused in standard OAuth flow but kept for API
}

func bindTokenRequest(r *http.Request) (TokenRequest, error) {
	var req TokenRequest
	contentType := strings.ToLower(r.Header.Get("Content-Type"))

	if strings.HasPrefix(contentType, "application/json") {
		if err := web.Bind(r, &req); err != nil {
			return req, err
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.GrantType = r.FormValue("grant_type")
		req.Code = r.FormValue("code")
		req.ClientID = r.FormValue("client_id")
		req.ClientSecret = r.FormValue("client_secret")
		req.RedirectURI = r.FormValue("redirect_uri")
		req.Scope = r.FormValue("scope")
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
		req.CodeVerifier = r.FormValue("code_verifier")
	}

	if req.ClientID == "" || req.ClientSecret == "" {
		if id, secret, ok := r.BasicAuth(); ok {
			if req.ClientID == "" {
				req.ClientID = id
			}
			if req.ClientSecret == "" {
				req.ClientSecret = secret
			}
		}
	}

	return req, nil
}

func (h *Handler) validateClient(ctx context.Context, clientID, clientSecret string) (db.OauthApplication, error) {
	if strings.TrimSpace(clientID) == "" {
		return db.OauthApplication{}, errors.New("missing client_id")
	}

	app, err := h.queries.GetOAuthApplicationByUID(ctx, clientID)
	if err != nil {
		return db.OauthApplication{}, err
	}
	if app.Confidential && !auth.SecureCompare(clientSecret, app.Secret) {
		return db.OauthApplication{}, errors.New("invalid client_secret")
	}
	return app, nil
}

func (h *Handler) ensureAppScopes(app db.OauthApplication, scopes []string) error {
	appScopes := strings.TrimSpace(app.Scopes)
	if appScopes == "" {
		return nil
	}
	for _, scope := range scopes {
		if !oauth.HasScope(appScopes, scope) {
			return oauth.ErrInvalidScope
		}
	}
	return nil
}

func normalizeScopes(scopeStr string) ([]string, string, error) {
	scopes := oauth.ParseScopes(scopeStr)
	if err := oauth.ValidateScopes(scopes); err != nil {
		return nil, "", err
	}
	return scopes, oauth.NormalizeScopes(scopes), nil
}

func (h *Handler) authenticateUser(ctx context.Context, email, password string) (db.User, string, error) {
	user, err := h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		return db.User{}, "", err
	}
	if user.Disabled || !user.Approved || !user.ConfirmedAt.Valid {
		return user, "account_inactive", errors.New("user not eligible")
	}
	if !auth.CheckPassword(password, user.EncryptedPassword) {
		return user, "invalid_password", errors.New("invalid password")
	}
	return user, "", nil
}

func (h *Handler) issueAccessToken(ctx context.Context, app db.OauthApplication, ownerID *int64, scopes string, allowReuse bool) (TokenResponse, error) {
	var resourceOwner pgtype.Int8
	if ownerID != nil {
		resourceOwner = pgtype.Int8{Int64: *ownerID, Valid: true}
	}
	scopeValue := pgtype.Text{String: scopes, Valid: true}
	applicationID := pgtype.Int8{Int64: app.ID, Valid: true}

	if allowReuse {
		existing, err := h.queries.GetLatestActiveOAuthAccessTokenByGrant(ctx, db.GetLatestActiveOAuthAccessTokenByGrantParams{
			ApplicationID:   applicationID,
			ResourceOwnerID: resourceOwner,
			Scopes:          scopeValue,
		})
		if err == nil {
			createdAt := time.Now().Unix()
			if existing.CreatedAt.Valid {
				createdAt = existing.CreatedAt.Time.Unix()
			}
			resp := TokenResponse{
				AccessToken: existing.Token,
				TokenType:   "Bearer",
				Scope:       scopes,
				CreatedAt:   createdAt,
			}
			return resp, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return TokenResponse{}, err
		}
	}

	accessToken, err := oauth.GenerateToken(32)
	if err != nil {
		return TokenResponse{}, err
	}

	expiresIn := pgtype.Int4{Valid: false}

	_, err = h.queries.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
		Token:           accessToken,
		RefreshToken:    pgtype.Text{Valid: false},
		ExpiresIn:       expiresIn,
		Scopes:          scopeValue,
		ApplicationID:   applicationID,
		ResourceOwnerID: resourceOwner,
	})
	if err != nil {
		return TokenResponse{}, err
	}

	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		Scope:       scopes,
		CreatedAt:   time.Now().Unix(),
	}
	return resp, nil
}

func selectRedirectURI(registered, requested string) (string, error) {
	allowed := splitRedirectURIs(registered)
	if len(allowed) == 0 {
		return "", errors.New("no redirect URIs registered")
	}

	if strings.TrimSpace(requested) == "" {
		if len(allowed) == 1 {
			return allowed[0], nil
		}
		return "", errors.New("redirect_uri required")
	}

	for _, uri := range allowed {
		if uri == requested {
			return uri, nil
		}
	}
	return "", errors.New("redirect_uri mismatch")
}

func splitRedirectURIs(raw string) []string {
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

func encodeGrantRedirectBinding(requestedRedirectURI string) string {
	if requestedRedirectURI == "" {
		return ""
	}
	return explicitGrantRedirectPrefix + requestedRedirectURI
}

func decodeGrantRedirectBinding(stored string) (redirectURI string, explicit bool) {
	if strings.HasPrefix(stored, explicitGrantRedirectPrefix) {
		return strings.TrimPrefix(stored, explicitGrantRedirectPrefix), true
	}
	return stored, false
}

func redirectWithCode(w http.ResponseWriter, r *http.Request, redirectURI, code, state string) {
	redirectWithQuery(w, r, redirectURI, map[string]string{
		"code":  code,
		"state": state,
	})
}

func redirectWithError(w http.ResponseWriter, r *http.Request, redirectURI, code, description, state string) {
	values := map[string]string{
		"error":             code,
		"error_description": description,
		"state":             state,
	}
	redirectWithQuery(w, r, redirectURI, values)
}

func redirectWithQuery(w http.ResponseWriter, r *http.Request, redirectURI string, values map[string]string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Invalid redirect URI")
		return
	}
	q := target.Query()
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		q.Set(key, value)
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	resp := map[string]string{
		"error": code,
	}
	if strings.TrimSpace(description) != "" {
		resp["error_description"] = description
	}
	web.JSON(w, status, resp)
}

func validateCodeChallenge(challenge, method string) error {
	if strings.TrimSpace(challenge) == "" {
		return nil
	}
	if strings.TrimSpace(method) == "" {
		return errors.New("code_challenge_method required")
	}
	if normalizedCodeChallengeMethod(method) != pkceMethodS256 {
		return errors.New("unsupported code_challenge_method")
	}
	return nil
}

func normalizedCodeChallengeMethod(method string) string {
	return strings.TrimSpace(method)
}

func verifyPKCE(grant db.OauthAccessGrant, codeVerifier string) error {
	if !grant.CodeChallenge.Valid || strings.TrimSpace(grant.CodeChallenge.String) == "" {
		return nil
	}

	if !validCodeVerifier(codeVerifier) {
		return errors.New("invalid code verifier")
	}

	method := normalizedCodeChallengeMethod(grant.CodeChallengeMethod.String)
	if method != pkceMethodS256 {
		return errors.New("unsupported code challenge method")
	}

	hash := sha256.Sum256([]byte(codeVerifier))
	expected := base64.RawURLEncoding.EncodeToString(hash[:])
	if !auth.SecureCompare(expected, grant.CodeChallenge.String) {
		return errors.New("invalid code verifier")
	}
	return nil
}

func validCodeVerifier(codeVerifier string) bool {
	if len(codeVerifier) < 43 || len(codeVerifier) > 128 {
		return false
	}
	for _, r := range codeVerifier {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '.' || r == '_' || r == '~':
		default:
			return false
		}
	}
	return true
}

func (h *Handler) issuer() string {
	domain := h.publicDomain()
	if domain == "" {
		return ""
	}
	return "https://" + domain + "/"
}

func (h *Handler) baseURL() string {
	domain := h.publicDomain()
	if domain == "" {
		return ""
	}
	return "https://" + domain
}

func (h *Handler) publicDomain() string {
	if h.cfg == nil {
		return "localhost:3000"
	}
	if strings.TrimSpace(h.cfg.Instance.WebDomain) != "" {
		return h.cfg.Instance.WebDomain
	}
	if strings.TrimSpace(h.cfg.Instance.LocalDomain) != "" {
		return h.cfg.Instance.LocalDomain
	}
	return "localhost:3000"
}
