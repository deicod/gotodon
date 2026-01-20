package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubOAuthStore struct {
	appsByUID       map[string]db.OauthApplication
	appsByID        map[int64]db.OauthApplication
	usersByEmail    map[string]db.User
	usersByID       map[int64]db.User
	accountsByID    map[int64]db.Account
	tokensByToken   map[string]db.OauthAccessToken
	tokensByRefresh map[string]db.OauthAccessToken
	grantsByToken   map[string]db.OauthAccessGrant
	nextTokenID     int64
	lastUsed        []db.UpdateOAuthTokenLastUsedParams
	deletedTokens   []pgtype.Int8
}

func newStubOAuthStore() *stubOAuthStore {
	return &stubOAuthStore{
		appsByUID:       map[string]db.OauthApplication{},
		appsByID:        map[int64]db.OauthApplication{},
		usersByEmail:    map[string]db.User{},
		usersByID:       map[int64]db.User{},
		accountsByID:    map[int64]db.Account{},
		tokensByToken:   map[string]db.OauthAccessToken{},
		tokensByRefresh: map[string]db.OauthAccessToken{},
		grantsByToken:   map[string]db.OauthAccessGrant{},
		nextTokenID:     100,
	}
}

func (s *stubOAuthStore) addApp(app db.OauthApplication) {
	s.appsByUID[app.Uid] = app
	s.appsByID[app.ID] = app
}

func (s *stubOAuthStore) addUser(user db.User) {
	s.usersByEmail[strings.ToLower(user.Email)] = user
	s.usersByID[user.ID] = user
}

func (s *stubOAuthStore) addAccount(account db.Account) {
	s.accountsByID[account.ID] = account
}

func (s *stubOAuthStore) updateToken(token db.OauthAccessToken) {
	s.tokensByToken[token.Token] = token
	if token.RefreshToken.Valid && strings.TrimSpace(token.RefreshToken.String) != "" {
		s.tokensByRefresh[token.RefreshToken.String] = token
	}
}

func (s *stubOAuthStore) GetOAuthApplication(ctx context.Context, id int64) (db.OauthApplication, error) {
	app, ok := s.appsByID[id]
	if !ok {
		return db.OauthApplication{}, pgx.ErrNoRows
	}
	return app, nil
}

func (s *stubOAuthStore) GetOAuthApplicationByUID(ctx context.Context, uid string) (db.OauthApplication, error) {
	app, ok := s.appsByUID[uid]
	if !ok {
		return db.OauthApplication{}, pgx.ErrNoRows
	}
	return app, nil
}

func (s *stubOAuthStore) CreateOAuthAccessToken(ctx context.Context, arg db.CreateOAuthAccessTokenParams) (db.OauthAccessToken, error) {
	s.nextTokenID++
	token := db.OauthAccessToken{
		ID:              s.nextTokenID,
		Token:           arg.Token,
		RefreshToken:    arg.RefreshToken,
		ExpiresIn:       arg.ExpiresIn,
		Scopes:          arg.Scopes,
		ApplicationID:   arg.ApplicationID,
		ResourceOwnerID: arg.ResourceOwnerID,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	s.updateToken(token)
	return token, nil
}

func (s *stubOAuthStore) GetOAuthAccessToken(ctx context.Context, token string) (db.OauthAccessToken, error) {
	found, ok := s.tokensByToken[token]
	if !ok || found.RevokedAt.Valid {
		return db.OauthAccessToken{}, pgx.ErrNoRows
	}
	return found, nil
}

func (s *stubOAuthStore) GetOAuthAccessTokenByRefresh(ctx context.Context, refresh pgtype.Text) (db.OauthAccessToken, error) {
	if !refresh.Valid {
		return db.OauthAccessToken{}, pgx.ErrNoRows
	}
	found, ok := s.tokensByRefresh[refresh.String]
	if !ok || found.RevokedAt.Valid {
		return db.OauthAccessToken{}, pgx.ErrNoRows
	}
	return found, nil
}

func (s *stubOAuthStore) RevokeOAuthAccessToken(ctx context.Context, token string) error {
	found, ok := s.tokensByToken[token]
	if !ok {
		return nil
	}
	found.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	s.updateToken(found)
	return nil
}

func (s *stubOAuthStore) CreateOAuthAccessGrant(ctx context.Context, arg db.CreateOAuthAccessGrantParams) (db.OauthAccessGrant, error) {
	grant := db.OauthAccessGrant{
		ID:                  int64(len(s.grantsByToken) + 1),
		Token:               arg.Token,
		ExpiresIn:           arg.ExpiresIn,
		RedirectUri:         arg.RedirectUri,
		Scopes:              arg.Scopes,
		ApplicationID:       arg.ApplicationID,
		ResourceOwnerID:     arg.ResourceOwnerID,
		CodeChallenge:       arg.CodeChallenge,
		CodeChallengeMethod: arg.CodeChallengeMethod,
		CreatedAt:           pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	s.grantsByToken[grant.Token] = grant
	return grant, nil
}

func (s *stubOAuthStore) GetOAuthAccessGrantByToken(ctx context.Context, token string) (db.OauthAccessGrant, error) {
	grant, ok := s.grantsByToken[token]
	if !ok || grant.RevokedAt.Valid {
		return db.OauthAccessGrant{}, pgx.ErrNoRows
	}
	return grant, nil
}

func (s *stubOAuthStore) RevokeOAuthAccessGrant(ctx context.Context, token string) error {
	grant, ok := s.grantsByToken[token]
	if !ok {
		return nil
	}
	grant.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	s.grantsByToken[token] = grant
	return nil
}

func (s *stubOAuthStore) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	user, ok := s.usersByEmail[strings.ToLower(email)]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *stubOAuthStore) GetUserByID(ctx context.Context, id int64) (db.User, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *stubOAuthStore) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	account, ok := s.accountsByID[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *stubOAuthStore) DeletePushSubscription(ctx context.Context, accessTokenID pgtype.Int8) error {
	s.deletedTokens = append(s.deletedTokens, accessTokenID)
	return nil
}

func (s *stubOAuthStore) UpdateOAuthTokenLastUsed(ctx context.Context, arg db.UpdateOAuthTokenLastUsedParams) error {
	s.lastUsed = append(s.lastUsed, arg)
	return nil
}

func TestTokenHandler_AuthorizationCodeScopesPreserved(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "urn:ietf:wg:oauth:2.0:oob",
		Scopes:       "read write follow",
		Confidential: true,
	}
	store.addApp(app)

	store.grantsByToken["grant123"] = db.OauthAccessGrant{
		ID:              10,
		Token:           "grant123",
		ExpiresIn:       600,
		RedirectUri:     "urn:ietf:wg:oauth:2.0:oob",
		Scopes:          pgtype.Text{String: "read write", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	handler := NewHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"redirect_uri":  []string{"urn:ietf:wg:oauth:2.0:oob"},
		"code":          []string{"grant123"},
		"scope":         []string{"push"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Scope != "read write" {
		t.Fatalf("scope = %q, want %q", resp.Scope, "read write")
	}
	if !store.grantsByToken["grant123"].RevokedAt.Valid {
		t.Fatalf("expected grant to be revoked")
	}
}

func TestTokenHandler_ClientCredentialsInvalidScope(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "urn:ietf:wg:oauth:2.0:oob",
		Scopes:       "read write follow",
		Confidential: true,
	})

	handler := NewHandler(store, nil)
	form := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"scope":         []string{"read write push"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestTokenHandler_PasswordGrant(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		Scopes:       "read write",
		Confidential: true,
	})

	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store.addUser(db.User{
		ID:                7,
		Email:             "user@example.com",
		EncryptedPassword: hash,
		AccountID:         42,
		Approved:          true,
		ConfirmedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	handler := NewHandler(store, nil)
	form := url.Values{
		"grant_type":    []string{"password"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"username":      []string{"user@example.com"},
		"password":      []string{"hunter2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RefreshToken == "" {
		t.Fatalf("expected refresh token")
	}
	if resp.Scope != "read" {
		t.Fatalf("scope = %q, want %q", resp.Scope, "read")
	}
}

func TestTokenHandler_RefreshToken(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           2,
		Uid:          "client",
		Secret:       "secret",
		Scopes:       "read",
		Confidential: true,
	}
	store.addApp(app)

	store.addUser(db.User{
		ID:          5,
		Email:       "user@example.com",
		AccountID:   77,
		Approved:    true,
		ConfirmedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	oldToken := db.OauthAccessToken{
		ID:              9,
		Token:           "oldtoken",
		RefreshToken:    pgtype.Text{String: "refresh123", Valid: true},
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: 5, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	store.updateToken(oldToken)

	handler := NewHandler(store, nil)
	form := url.Values{
		"grant_type":    []string{"refresh_token"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"refresh_token": []string{"refresh123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !store.tokensByToken["oldtoken"].RevokedAt.Valid {
		t.Fatalf("expected old token to be revoked")
	}
	if len(store.deletedTokens) == 0 || store.deletedTokens[0].Int64 != oldToken.ID {
		t.Fatalf("expected push subscription deletion for old token")
	}
}

func TestAuthorizePost_Deny(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "https://app.example/callback",
		Scopes:       "read",
		Confidential: true,
	})

	handler := NewHandler(store, nil)
	form := url.Values{
		"client_id":     []string{"client"},
		"redirect_uri":  []string{"https://app.example/callback"},
		"scope":         []string{"read"},
		"response_type": []string{"code"},
		"approve":       []string{"false"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.AuthorizePost(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "error=access_denied") {
		t.Fatalf("location = %q, want access_denied", location)
	}
}

func TestAuthorizePost_PKCEGrant(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "https://app.example/callback",
		Scopes:       "read",
		Confidential: true,
	}
	store.addApp(app)

	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store.addUser(db.User{
		ID:                7,
		Email:             "user@example.com",
		EncryptedPassword: hash,
		AccountID:         9,
		Approved:          true,
		ConfirmedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	handler := NewHandler(store, nil)
	form := url.Values{
		"client_id":             []string{"client"},
		"redirect_uri":          []string{"https://app.example/callback"},
		"scope":                 []string{"read"},
		"response_type":         []string{"code"},
		"approve":               []string{"true"},
		"email":                 []string{"user@example.com"},
		"password":              []string{"hunter2"},
		"code_challenge":        []string{challenge},
		"code_challenge_method": []string{"S256"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.AuthorizePost(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}

	location := rec.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	code := parsed.Query().Get("code")
	if code == "" {
		t.Fatalf("expected code in redirect")
	}
	grant, ok := store.grantsByToken[code]
	if !ok || !grant.CodeChallenge.Valid || grant.CodeChallenge.String != challenge {
		t.Fatalf("grant not stored with code challenge")
	}
}

func TestUserinfoRequiresProfileScope(t *testing.T) {
	store := newStubOAuthStore()
	store.addAccount(db.Account{
		ID:              1,
		Username:        "alice",
		DisplayName:     "Alice",
		AvatarRemoteUrl: pgtype.Text{String: "https://cdn.example/avatar.png", Valid: true},
	})

	handler := NewHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})
	req := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read:accounts")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.Userinfo(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUserinfoSuccess(t *testing.T) {
	store := newStubOAuthStore()
	store.addAccount(db.Account{
		ID:              1,
		Username:        "alice",
		DisplayName:     "Alice",
		AvatarRemoteUrl: pgtype.Text{String: "https://cdn.example/avatar.png", Valid: true},
	})

	handler := NewHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})
	req := httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "profile")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.Userinfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["iss"] != "https://example.com/" {
		t.Fatalf("iss = %q, want %q", resp["iss"], "https://example.com/")
	}
	if resp["preferred_username"] != "alice" {
		t.Fatalf("preferred_username = %q, want %q", resp["preferred_username"], "alice")
	}
}

func TestMetadata(t *testing.T) {
	handler := NewHandler(newStubOAuthStore(), &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}})
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	handler.Metadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["issuer"] != "https://example.com/" {
		t.Fatalf("issuer = %v, want %v", resp["issuer"], "https://example.com/")
	}
	if resp["authorization_endpoint"] != "https://example.com/oauth/authorize" {
		t.Fatalf("authorization_endpoint = %v", resp["authorization_endpoint"])
	}
}

func TestTokenValidator(t *testing.T) {
	store := newStubOAuthStore()
	store.addUser(db.User{
		ID:          3,
		Email:       "user@example.com",
		AccountID:   12,
		Approved:    true,
		ConfirmedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	store.tokensByToken["token"] = db.OauthAccessToken{
		ID:              1,
		Token:           "token",
		Scopes:          pgtype.Text{String: "profile", Valid: true},
		ApplicationID:   pgtype.Int8{Int64: 5, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: 3, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	validator := NewTokenValidator(store)
	ctx := context.WithValue(context.Background(), middleware.ContextKeyClientIP, "203.0.113.10")
	info, err := validator.ValidateToken(ctx, "token")
	if err != nil {
		t.Fatalf("ValidateToken error: %v", err)
	}
	if info.AccountID == nil || *info.AccountID != 12 {
		t.Fatalf("account id = %v, want 12", info.AccountID)
	}
	if len(store.lastUsed) == 0 {
		t.Fatalf("expected token usage to be recorded")
	}
}
