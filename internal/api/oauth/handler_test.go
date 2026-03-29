package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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

type publishedKillEvent struct {
	eventType string
	payload   any
	streams   []string
	ctxErr    error
}

type stubTokenKillPublisher struct {
	events []publishedKillEvent
	err    error
}

func (s *stubTokenKillPublisher) PublishEventToRedis(ctx context.Context, eventType string, payload any, streams []string) error {
	s.events = append(s.events, publishedKillEvent{eventType: eventType, payload: payload, streams: streams, ctxErr: ctx.Err()})
	return s.err
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

func (s *stubOAuthStore) GetLatestActiveOAuthAccessTokenByGrant(ctx context.Context, arg db.GetLatestActiveOAuthAccessTokenByGrantParams) (db.OauthAccessToken, error) {
	var (
		found  db.OauthAccessToken
		hasAny bool
	)
	for _, token := range s.tokensByToken {
		if token.RevokedAt.Valid {
			continue
		}
		if token.ExpiresIn.Valid && token.CreatedAt.Valid {
			expiresAt := token.CreatedAt.Time.Add(time.Duration(token.ExpiresIn.Int32) * time.Second)
			if time.Now().After(expiresAt) {
				continue
			}
		}
		if !token.ApplicationID.Valid || token.ApplicationID.Int64 != arg.ApplicationID.Int64 {
			continue
		}
		if token.ResourceOwnerID.Valid != arg.ResourceOwnerID.Valid {
			continue
		}
		if token.ResourceOwnerID.Valid && token.ResourceOwnerID.Int64 != arg.ResourceOwnerID.Int64 {
			continue
		}
		if token.Scopes.String != arg.Scopes.String {
			continue
		}
		if !hasAny || token.CreatedAt.Time.After(found.CreatedAt.Time) || (token.CreatedAt.Time.Equal(found.CreatedAt.Time) && token.ID > found.ID) {
			found = token
			hasAny = true
		}
	}
	if !hasAny {
		return db.OauthAccessToken{}, pgx.ErrNoRows
	}
	return found, nil
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

	handler := NewHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
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
	if resp.RefreshToken == "" {
		t.Fatalf("expected refresh token in authorization_code response")
	}
	if !store.grantsByToken["grant123"].RevokedAt.Valid {
		t.Fatalf("expected grant to be revoked")
	}
}

func TestTokenHandler_AuthorizationCodeRedirectURIMatch(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "https://app.example/callback",
		Scopes:       "read write",
		Confidential: true,
	}
	store.addApp(app)
	store.grantsByToken["grant-redirect-match"] = db.OauthAccessGrant{
		Token:           "grant-redirect-match",
		ExpiresIn:       600,
		RedirectUri:     encodeGrantRedirectBinding("https://app.example/callback"),
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{"grant-redirect-match"},
		"redirect_uri":  []string{"https://app.example/callback"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !store.grantsByToken["grant-redirect-match"].RevokedAt.Valid {
		t.Fatalf("expected grant to be revoked")
	}
}

func TestTokenHandler_AuthorizationCodeRedirectURIRequiredWhenGrantExplicitlyBound(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "https://app.example/callback",
		Scopes:       "read write",
		Confidential: true,
	}
	store.addApp(app)
	store.grantsByToken["grant-redirect-required"] = db.OauthAccessGrant{
		Token:           "grant-redirect-required",
		ExpiresIn:       600,
		RedirectUri:     encodeGrantRedirectBinding("https://app.example/callback"),
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{"grant-redirect-required"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "invalid_grant" {
		t.Fatalf("error = %q, want %q", resp["error"], "invalid_grant")
	}
	if resp["error_description"] != "Redirect URI mismatch" {
		t.Fatalf("error_description = %q, want %q", resp["error_description"], "Redirect URI mismatch")
	}
	if store.grantsByToken["grant-redirect-required"].RevokedAt.Valid {
		t.Fatalf("expected grant to remain active on mismatch")
	}
}

func TestTokenHandler_AuthorizationCodeRedirectURIMismatch(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "https://app.example/callback",
		Scopes:       "read write",
		Confidential: true,
	}
	store.addApp(app)
	store.grantsByToken["grant-redirect-mismatch"] = db.OauthAccessGrant{
		Token:           "grant-redirect-mismatch",
		ExpiresIn:       600,
		RedirectUri:     encodeGrantRedirectBinding("https://app.example/callback"),
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{"grant-redirect-mismatch"},
		"redirect_uri":  []string{"https://evil.example/callback"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "invalid_grant" {
		t.Fatalf("error = %q, want %q", resp["error"], "invalid_grant")
	}
	if resp["error_description"] != "Redirect URI mismatch" {
		t.Fatalf("error_description = %q, want %q", resp["error_description"], "Redirect URI mismatch")
	}
	if store.grantsByToken["grant-redirect-mismatch"].RevokedAt.Valid {
		t.Fatalf("expected grant to remain active on mismatch")
	}
}

func TestTokenHandler_AuthorizationCodePrePatchSingleRedirectGrantAllowsOmittedRedirect(t *testing.T) {
	store := newStubOAuthStore()
	app := db.OauthApplication{
		ID:           1,
		Uid:          "client",
		Secret:       "secret",
		RedirectUri:  "https://app.example/callback",
		Scopes:       "read write",
		Confidential: true,
	}
	store.addApp(app)
	// Pre-patch behavior stored resolved redirect URI in grant even when authorize
	// omitted redirect_uri for a single-URI app.
	store.grantsByToken["grant-prepatch-single-uri"] = db.OauthAccessGrant{
		Token:           "grant-prepatch-single-uri",
		ExpiresIn:       600,
		RedirectUri:     "https://app.example/callback",
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{"grant-prepatch-single-uri"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !store.grantsByToken["grant-prepatch-single-uri"].RevokedAt.Valid {
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

	handler := NewHandler(store, nil, nil)
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

func TestTokenHandler_ClientCredentialsReusesMatchingToken(t *testing.T) {
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
	store.updateToken(db.OauthAccessToken{
		ID:              45,
		Token:           "existing-client-token",
		Scopes:          pgtype.Text{String: "read write", Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"scope":         []string{"read write"},
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
	if resp.AccessToken != "existing-client-token" {
		t.Fatalf("access_token = %q, want existing token", resp.AccessToken)
	}
	if len(store.tokensByToken) != 1 {
		t.Fatalf("token rows = %d, want %d", len(store.tokensByToken), 1)
	}
}

func TestTokenHandler_AuthorizationCodeReusesMatchingTokenAndRefresh(t *testing.T) {
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
	store.grantsByToken["grant-reuse"] = db.OauthAccessGrant{
		ID:              10,
		Token:           "grant-reuse",
		ExpiresIn:       600,
		RedirectUri:     "urn:ietf:wg:oauth:2.0:oob",
		Scopes:          pgtype.Text{String: "read write", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	store.updateToken(db.OauthAccessToken{
		ID:              46,
		Token:           "existing-auth-token",
		RefreshToken:    pgtype.Text{String: "existing-refresh-token", Valid: true},
		Scopes:          pgtype.Text{String: "read write", Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: 99, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"redirect_uri":  []string{"urn:ietf:wg:oauth:2.0:oob"},
		"code":          []string{"grant-reuse"},
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
	if resp.AccessToken != "existing-auth-token" {
		t.Fatalf("access_token = %q, want existing token", resp.AccessToken)
	}
	if resp.RefreshToken != "existing-refresh-token" {
		t.Fatalf("refresh_token = %q, want existing refresh token", resp.RefreshToken)
	}
	if !store.grantsByToken["grant-reuse"].RevokedAt.Valid {
		t.Fatalf("expected grant to be revoked")
	}
	if len(store.tokensByToken) != 1 {
		t.Fatalf("token rows = %d, want %d", len(store.tokensByToken), 1)
	}
}

func TestTokenHandler_AuthorizationCodeReuseFallbackWhenExistingLacksRefreshToken(t *testing.T) {
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
	store.grantsByToken["grant-fallback"] = db.OauthAccessGrant{
		ID:              10,
		Token:           "grant-fallback",
		ExpiresIn:       600,
		RedirectUri:     "urn:ietf:wg:oauth:2.0:oob",
		Scopes:          pgtype.Text{String: "read write", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	store.updateToken(db.OauthAccessToken{
		ID:              47,
		Token:           "existing-without-refresh",
		Scopes:          pgtype.Text{String: "read write", Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: 99, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"redirect_uri":  []string{"urn:ietf:wg:oauth:2.0:oob"},
		"code":          []string{"grant-fallback"},
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
	if resp.AccessToken == "existing-without-refresh" {
		t.Fatalf("expected fallback mint instead of reusing token without refresh")
	}
	if resp.RefreshToken == "" {
		t.Fatalf("expected minted token to include refresh token")
	}
	if len(store.tokensByToken) != 2 {
		t.Fatalf("token rows = %d, want %d", len(store.tokensByToken), 2)
	}
}

func TestTokenHandler_ClientCredentialsDoesNotReuseExpiredToken(t *testing.T) {
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
	store.updateToken(db.OauthAccessToken{
		ID:            48,
		Token:         "expired-client-token",
		ExpiresIn:     pgtype.Int4{Int32: 60, Valid: true},
		Scopes:        pgtype.Text{String: "read write", Valid: true},
		ApplicationID: pgtype.Int8{Int64: app.ID, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Minute), Valid: true},
	})

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"scope":         []string{"read write"},
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
	if resp.AccessToken == "expired-client-token" {
		t.Fatalf("expected new token, got expired reused token")
	}
	if len(store.tokensByToken) != 2 {
		t.Fatalf("token rows = %d, want %d", len(store.tokensByToken), 2)
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

	handler := NewHandler(store, nil, nil)
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

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "unsupported_grant_type" {
		t.Fatalf("error = %q, want %q", resp["error"], "unsupported_grant_type")
	}
	if resp["error_description"] != "The grant type is not supported" {
		t.Fatalf("error_description = %q, want %q", resp["error_description"], "The grant type is not supported")
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

	killPublisher := &stubTokenKillPublisher{}
	handler := NewHandler(store, nil, killPublisher)
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
	var resp TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == oldToken.Token {
		t.Fatalf("expected refresh grant to rotate access token")
	}
	if resp.RefreshToken == oldToken.RefreshToken.String {
		t.Fatalf("expected refresh grant to rotate refresh token")
	}
	if !store.tokensByToken["oldtoken"].RevokedAt.Valid {
		t.Fatalf("expected old token to be revoked")
	}
	if len(store.deletedTokens) == 0 || store.deletedTokens[0].Int64 != oldToken.ID {
		t.Fatalf("expected push subscription deletion for old token")
	}
	if len(killPublisher.events) != 1 {
		t.Fatalf("kill events = %d, want 1", len(killPublisher.events))
	}
	if killPublisher.events[0].streams[0] != "access_token:9" {
		t.Fatalf("kill stream = %q, want %q", killPublisher.events[0].streams[0], "access_token:9")
	}
}

func TestRevoke_SameClientRevokesAndPublishesKill(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           44,
		Uid:          "client44",
		Secret:       "secret44",
		Confidential: true,
	})
	store.updateToken(db.OauthAccessToken{
		ID:            44,
		Token:         "token44",
		RefreshToken:  pgtype.Text{String: "refresh44", Valid: true},
		ApplicationID: pgtype.Int8{Int64: 44, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	killPublisher := &stubTokenKillPublisher{}
	handler := NewHandler(store, nil, killPublisher)

	form := url.Values{
		"token":         []string{"token44"},
		"client_id":     []string{"client44"},
		"client_secret": []string{"secret44"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Revoke(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !store.tokensByToken["token44"].RevokedAt.Valid {
		t.Fatalf("expected token to be revoked")
	}
	if len(killPublisher.events) != 1 {
		t.Fatalf("kill events = %d, want 1", len(killPublisher.events))
	}
	if killPublisher.events[0].streams[0] != "access_token:44" {
		t.Fatalf("kill stream = %q, want %q", killPublisher.events[0].streams[0], "access_token:44")
	}
	if len(store.deletedTokens) != 1 || store.deletedTokens[0].Int64 != 44 {
		t.Fatalf("expected push subscription deletion for token 44")
	}
}

func TestRevoke_MismatchedClientDoesNotRevoke(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           77,
		Uid:          "client77",
		Secret:       "secret77",
		Confidential: true,
	})
	store.addApp(db.OauthApplication{
		ID:           88,
		Uid:          "client88",
		Secret:       "secret88",
		Confidential: true,
	})
	store.updateToken(db.OauthAccessToken{
		ID:            55,
		Token:         "token55",
		ApplicationID: pgtype.Int8{Int64: 88, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	killPublisher := &stubTokenKillPublisher{}
	handler := NewHandler(store, nil, killPublisher)

	form := url.Values{
		"token":         []string{"token55"},
		"client_id":     []string{"client77"},
		"client_secret": []string{"secret77"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Revoke(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if store.tokensByToken["token55"].RevokedAt.Valid {
		t.Fatalf("expected token to remain active for mismatched client")
	}
	if len(killPublisher.events) != 0 {
		t.Fatalf("kill events = %d, want 0", len(killPublisher.events))
	}
	if len(store.deletedTokens) != 0 {
		t.Fatalf("deleted push subscriptions = %d, want 0", len(store.deletedTokens))
	}
}

func TestRevoke_MissingClientContextDoesNotRevoke(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           99,
		Uid:          "client99",
		Secret:       "secret99",
		Confidential: true,
	})
	store.updateToken(db.OauthAccessToken{
		ID:            66,
		Token:         "token66",
		ApplicationID: pgtype.Int8{Int64: 99, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	killPublisher := &stubTokenKillPublisher{}
	handler := NewHandler(store, nil, killPublisher)

	form := url.Values{"token": []string{"token66"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Revoke(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if store.tokensByToken["token66"].RevokedAt.Valid {
		t.Fatalf("expected token to remain active without client context")
	}
	if len(killPublisher.events) != 0 {
		t.Fatalf("kill events = %d, want 0", len(killPublisher.events))
	}
	if len(store.deletedTokens) != 0 {
		t.Fatalf("deleted push subscriptions = %d, want 0", len(store.deletedTokens))
	}
}

func TestRevoke_KillPublishFailureDoesNotBreakSuccess(t *testing.T) {
	store := newStubOAuthStore()
	store.addApp(db.OauthApplication{
		ID:           55,
		Uid:          "client55",
		Secret:       "secret55",
		Confidential: true,
	})
	store.updateToken(db.OauthAccessToken{
		ID:            55,
		Token:         "token55",
		ApplicationID: pgtype.Int8{Int64: 55, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	killPublisher := &stubTokenKillPublisher{err: errors.New("redis down")}
	handler := NewHandler(store, nil, killPublisher)

	form := url.Values{
		"token":         []string{"token55"},
		"client_id":     []string{"client55"},
		"client_secret": []string{"secret55"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Revoke(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !store.tokensByToken["token55"].RevokedAt.Valid {
		t.Fatalf("expected token to be revoked despite kill publish failure")
	}
	if len(store.deletedTokens) != 1 || store.deletedTokens[0].Int64 != 55 {
		t.Fatalf("expected push subscription deletion for token 55")
	}
}

func TestTokenHandler_RefreshTokenKillPublishFailureDoesNotBreakSuccess(t *testing.T) {
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
	store.updateToken(db.OauthAccessToken{
		ID:              9,
		Token:           "oldtoken",
		RefreshToken:    pgtype.Text{String: "refresh123", Valid: true},
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   pgtype.Int8{Int64: app.ID, Valid: true},
		ResourceOwnerID: pgtype.Int8{Int64: 5, Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})

	killPublisher := &stubTokenKillPublisher{err: errors.New("redis down")}
	handler := NewHandler(store, nil, killPublisher)
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
		t.Fatalf("expected old token to be revoked despite kill publish failure")
	}
}

func TestPublishTokenKillUsesLiveContext(t *testing.T) {
	killPublisher := &stubTokenKillPublisher{}
	handler := NewHandler(newStubOAuthStore(), nil, killPublisher)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler.publishTokenKill(ctx, 88)

	if len(killPublisher.events) != 1 {
		t.Fatalf("kill events = %d, want 1", len(killPublisher.events))
	}
	if killPublisher.events[0].ctxErr != nil {
		t.Fatalf("publish context err = %v, want nil", killPublisher.events[0].ctxErr)
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

	handler := NewHandler(store, nil, nil)
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

	handler := NewHandler(store, nil, nil)
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
	if grant.RedirectUri != encodeGrantRedirectBinding("https://app.example/callback") {
		t.Fatalf("grant redirect_uri = %q, want encoded explicit redirect", grant.RedirectUri)
	}
}

func TestAuthorizePost_OmittedRedirectBindsNoGrantRedirectAndTokenWithoutRedirectSucceeds(t *testing.T) {
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

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"client_id":     []string{"client"},
		"scope":         []string{"read"},
		"response_type": []string{"code"},
		"approve":       []string{"true"},
		"email":         []string{"user@example.com"},
		"password":      []string{"hunter2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.AuthorizePost(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d, want %d", rec.Code, http.StatusFound)
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
	grant := store.grantsByToken[code]
	if grant.RedirectUri != "" {
		t.Fatalf("grant redirect_uri = %q, want empty when request omitted redirect_uri", grant.RedirectUri)
	}

	tokenForm := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{code},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	handler.Token(tokenRec, tokenReq)

	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want %d", tokenRec.Code, http.StatusOK)
	}
}

func TestAuthorizePost_WhitespaceRedirectBindsNoGrantRedirectAndTokenWithoutRedirectSucceeds(t *testing.T) {
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

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"client_id":     []string{"client"},
		"redirect_uri":  []string{"   "},
		"scope":         []string{"read"},
		"response_type": []string{"code"},
		"approve":       []string{"true"},
		"email":         []string{"user@example.com"},
		"password":      []string{"hunter2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.AuthorizePost(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d, want %d", rec.Code, http.StatusFound)
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
	grant := store.grantsByToken[code]
	if grant.RedirectUri != "" {
		t.Fatalf("grant redirect_uri = %q, want empty when request redirect_uri is whitespace", grant.RedirectUri)
	}

	tokenForm := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{code},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	handler.Token(tokenRec, tokenReq)

	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want %d", tokenRec.Code, http.StatusOK)
	}
}

func TestTokenHandler_AuthorizationCodeRejectsMismatchingRedirectWhenGrantNotExplicitlyBound(t *testing.T) {
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
	store.grantsByToken["grant-unbound-redirect"] = db.OauthAccessGrant{
		Token:           "grant-unbound-redirect",
		ExpiresIn:       600,
		RedirectUri:     "",
		Scopes:          pgtype.Text{String: "read", Valid: true},
		ApplicationID:   app.ID,
		ResourceOwnerID: 99,
		CreatedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	handler := NewHandler(store, nil, nil)
	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"client_id":     []string{"client"},
		"client_secret": []string{"secret"},
		"code":          []string{"grant-unbound-redirect"},
		"redirect_uri":  []string{"https://evil.example/callback"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Token(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "invalid_grant" {
		t.Fatalf("error = %q, want %q", resp["error"], "invalid_grant")
	}
	if resp["error_description"] != "Redirect URI mismatch" {
		t.Fatalf("error_description = %q, want %q", resp["error_description"], "Redirect URI mismatch")
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

	handler := NewHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
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

	handler := NewHandler(store, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
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
	handler := NewHandler(newStubOAuthStore(), &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
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
