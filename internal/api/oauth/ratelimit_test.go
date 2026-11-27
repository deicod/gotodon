package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// countingOAuthStore counts account lookups, to show that refused attempts
// never reach one.
type countingOAuthStore struct {
	*stubOAuthStore
	emailLookups int
}

func (s *countingOAuthStore) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	s.emailLookups++
	return s.stubOAuthStore.GetUserByEmail(ctx, email)
}

const rateLimitTestPassword = "correct horse battery staple"

// newRateLimitedOAuthHandler returns a handler with an application "client"
// and a user user@example.com, rate limited in memory.
func newRateLimitedOAuthHandler(t *testing.T) (*Handler, *countingOAuthStore) {
	t.Helper()
	store := &countingOAuthStore{stubOAuthStore: newStubOAuthStore()}
	store.addApp(db.OauthApplication{
		ID:          1,
		Uid:         "client",
		Secret:      "secret",
		RedirectUri: "https://app.example/callback",
		Scopes:      "read",
	})
	// The cheapest cost keeps the many failed sign-ins below fast.
	hash, err := bcrypt.GenerateFromPassword([]byte(rateLimitTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store.addUser(db.User{
		ID:                7,
		Email:             "user@example.com",
		EncryptedPassword: string(hash),
		AccountID:         9,
		Approved:          true,
		ConfirmedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	handler := NewHandler(store, nil, nil)
	handler.SetRateLimiter(middleware.NewRateLimiter(middleware.NewMemoryRateLimitStore(0)))
	return handler, store
}

func loginRequest(remoteAddr, email, password string) *http.Request {
	form := url.Values{
		"client_id":     {"client"},
		"redirect_uri":  {"https://app.example/callback"},
		"scope":         {"read"},
		"response_type": {"code"},
		"approve":       {"true"},
		"email":         {email},
		"password":      {password},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	return req
}

func serveLogin(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertLoginThrottled(t *testing.T, rec *httptest.ResponseRecorder, limit middleware.RateLimit) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"Too many requests"}` {
		t.Errorf("body = %s, want Mastodon's 429 error", got)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != fmt.Sprint(limit.Limit) {
		t.Errorf("X-RateLimit-Limit = %q, want %d (%s)", got, limit.Limit, limit.Name)
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "0" || rec.Header().Get("X-RateLimit-Reset") == "" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("rate limit headers = %v", rec.Header())
	}
}

func TestAuthorizePost_RepeatedFailedLoginsAreThrottled(t *testing.T) {
	handler, store := newRateLimitedOAuthHandler(t)
	login := http.HandlerFunc(handler.AuthorizePost)

	for i := 1; i <= int(middleware.LoginAttemptsByIP.Limit); i++ {
		rec := serveLogin(login, loginRequest("203.0.113.5:4000", "user@example.com", "wrong password"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
		}
	}
	lookups := store.emailLookups
	failures := len(store.loginActivities)

	// Over the limit, even the right password is refused without a check.
	rec := serveLogin(login, loginRequest("203.0.113.5:4000", "user@example.com", rateLimitTestPassword))
	assertLoginThrottled(t, rec, middleware.LoginAttemptsByIP)
	if store.emailLookups != lookups || len(store.loginActivities) != failures || len(store.grantsByToken) != 0 {
		t.Fatalf("a refused attempt was authenticated: lookups %d -> %d, activities %d -> %d, grants %d",
			lookups, store.emailLookups, failures, len(store.loginActivities), len(store.grantsByToken))
	}

	// Another network is not affected.
	rec = serveLogin(login, loginRequest("198.51.100.9:4000", "other@example.com", "wrong password"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("other network: status = %d, want 401", rec.Code)
	}
}

func TestAuthorizePost_RepeatedLoginsForUnknownAccountAreThrottled(t *testing.T) {
	handler, _ := newRateLimitedOAuthHandler(t)
	login := http.HandlerFunc(handler.AuthorizePost)

	for i := 1; i <= int(middleware.LoginAttemptsByIP.Limit); i++ {
		rec := serveLogin(login, loginRequest("203.0.113.5:4000", "nobody@example.com", "guess"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
		}
	}
	assertLoginThrottled(t, serveLogin(login, loginRequest("203.0.113.5:4000", "nobody@example.com", "guess")), middleware.LoginAttemptsByIP)
}

func TestAuthorizePost_ThrottlingDoesNotDiscloseAccounts(t *testing.T) {
	handler, _ := newRateLimitedOAuthHandler(t)
	login := http.HandlerFunc(handler.AuthorizePost)

	// Each address is tried from its own network, so both exhaust the same
	// limits at the same pace.
	for i := 1; i <= int(middleware.LoginAttemptsByIP.Limit)+2; i++ {
		existing := serveLogin(login, loginRequest("203.0.113.5:4000", "user@example.com", "wrong password"))
		unknown := serveLogin(login, loginRequest("198.51.100.9:4000", "nobody@example.com", "wrong password"))

		if existing.Code != unknown.Code || existing.Body.String() != unknown.Body.String() {
			t.Fatalf("attempt %d: existing account got %d %s, unknown account got %d %s",
				i, existing.Code, existing.Body.String(), unknown.Code, unknown.Body.String())
		}
		for _, header := range []string{"Content-Type", "X-RateLimit-Limit", "X-RateLimit-Remaining"} {
			if existing.Header().Get(header) != unknown.Header().Get(header) {
				t.Fatalf("attempt %d: %s = %q for the existing account, %q for the unknown one",
					i, header, existing.Header().Get(header), unknown.Header().Get(header))
			}
		}
		want := http.StatusUnauthorized
		if i > int(middleware.LoginAttemptsByIP.Limit) {
			want = http.StatusTooManyRequests
		}
		if existing.Code != want {
			t.Fatalf("attempt %d: status = %d, want %d", i, existing.Code, want)
		}
	}
}

func TestAuthorizePost_LoginLimitPerEmailAcrossNetworks(t *testing.T) {
	for _, email := range []string{"user@example.com", "nobody@example.com"} {
		t.Run(email, func(t *testing.T) {
			handler, _ := newRateLimitedOAuthHandler(t)
			login := http.HandlerFunc(handler.AuthorizePost)

			for i := 1; i <= int(middleware.LoginAttemptsByEmail.Limit); i++ {
				// Spelling variants count against the same address.
				submitted := email
				if i%2 == 0 {
					submitted = " " + strings.ToUpper(email) + " "
				}
				rec := serveLogin(login, loginRequest(fmt.Sprintf("192.0.2.%d:4000", i), submitted, "wrong password"))
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
				}
			}
			rec := serveLogin(login, loginRequest("198.51.100.200:4000", email, "wrong password"))
			assertLoginThrottled(t, rec, middleware.LoginAttemptsByEmail)
		})
	}
}

func TestAuthorizePost_LoginLimitPerNetworkAcrossEmails(t *testing.T) {
	handler, _ := newRateLimitedOAuthHandler(t)
	login := http.HandlerFunc(handler.AuthorizePost)

	for i := 1; i <= int(middleware.LoginAttemptsByIP.Limit); i++ {
		rec := serveLogin(login, loginRequest("203.0.113.5:4000", fmt.Sprintf("guess%d@example.com", i), "guess"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
		}
	}
	assertLoginThrottled(t, serveLogin(login, loginRequest("203.0.113.5:4000", "user@example.com", "guess")), middleware.LoginAttemptsByIP)
}

func TestAuthorizePost_SpoofedForwardingHeadersDoNotBypassLimits(t *testing.T) {
	handler, _ := newRateLimitedOAuthHandler(t)
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	server := middleware.ResolveClientIP(middleware.NewClientIPResolver(trusted))(http.HandlerFunc(handler.AuthorizePost))

	t.Run("direct client", func(t *testing.T) {
		for i := 1; i <= int(middleware.LoginAttemptsByIP.Limit)+1; i++ {
			req := loginRequest("203.0.113.5:4000", fmt.Sprintf("guess%d@example.com", i), "guess")
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
			req.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i))
			rec := serveLogin(server, req)
			if i <= int(middleware.LoginAttemptsByIP.Limit) {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
				}
				continue
			}
			assertLoginThrottled(t, rec, middleware.LoginAttemptsByIP)
		}
	})

	t.Run("behind the trusted proxy", func(t *testing.T) {
		for i := 1; i <= int(middleware.LoginAttemptsByIP.Limit)+1; i++ {
			req := loginRequest("127.0.0.1:4000", fmt.Sprintf("guess%d@example.com", i), "guess")
			// The client prepends addresses; the proxy appends the real one.
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d, 2001:db8:5::%x", i, i))
			rec := serveLogin(server, req)
			if i <= int(middleware.LoginAttemptsByIP.Limit) {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
				}
				continue
			}
			// Every address of the client's /64 shares its limit.
			assertLoginThrottled(t, rec, middleware.LoginAttemptsByIP)
		}
	})
}

type failingStore struct{}

func (failingStore) Hit(context.Context, string, time.Duration) (int64, time.Duration, error) {
	return 0, 0, errors.New("redis down")
}

func TestAuthorizePost_UncountableAttemptsAreRefused(t *testing.T) {
	handler, store := newRateLimitedOAuthHandler(t)
	handler.SetRateLimiter(middleware.NewRateLimiter(failingStore{}))

	rec := serveLogin(http.HandlerFunc(handler.AuthorizePost), loginRequest("203.0.113.5:4000", "user@example.com", rateLimitTestPassword))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status = %d, Retry-After = %q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	if store.emailLookups != 0 || len(store.grantsByToken) != 0 {
		t.Fatalf("an uncounted attempt was authenticated")
	}
}

func TestAuthorizePost_SuccessfulLoginWithinLimits(t *testing.T) {
	handler, _ := newRateLimitedOAuthHandler(t)
	rec := serveLogin(http.HandlerFunc(handler.AuthorizePost), loginRequest("203.0.113.5:4000", "user@example.com", rateLimitTestPassword))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body.String())
	}
}
