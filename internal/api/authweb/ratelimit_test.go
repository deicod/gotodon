package authweb

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

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// countingQuerier counts account lookups, to show that refused requests
// never reach one.
type countingQuerier struct {
	*stubQuerier
	emailLookups int
	tokenLookups int
}

func (q *countingQuerier) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	q.emailLookups++
	return q.stubQuerier.GetUserByEmail(ctx, email)
}

func (q *countingQuerier) GetUserByResetPasswordToken(ctx context.Context, token pgtype.Text) (db.User, error) {
	q.tokenLookups++
	return q.stubQuerier.GetUserByResetPasswordToken(ctx, token)
}

// newRateLimitedHandler returns a handler knowing an unconfirmed user
// user@example.org, rate limited in memory, and its routes.
func newRateLimitedHandler() (*Handler, *countingQuerier, *captureDeliverer, *http.ServeMux) {
	queries := &countingQuerier{stubQuerier: newStubQuerier()}
	queries.usersByEmail["user@example.org"] = db.User{ID: 7, Email: "user@example.org"}
	deliverer := &captureDeliverer{}
	h := newTestHandler(queries, deliverer)
	h.SetRateLimiter(middleware.NewRateLimiter(middleware.NewMemoryRateLimitStore(0)))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, queries, deliverer, mux
}

func postForm(path, remoteAddr string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	return req
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertThrottledPage(t *testing.T, rec *httptest.ResponseRecorder, limit middleware.RateLimit) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Too many requests") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("response = %s %s, want the HTML 429 page", rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != fmt.Sprint(limit.Limit) {
		t.Errorf("X-RateLimit-Limit = %q, want %d (%s)", got, limit.Limit, limit.Name)
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "0" || rec.Header().Get("X-RateLimit-Reset") == "" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("rate limit headers = %v", rec.Header())
	}
}

func TestRequestPasswordResetRepeatedRequestsAreThrottled(t *testing.T) {
	for _, tt := range []struct {
		email     string
		wantMails int
	}{
		{email: "user@example.org", wantMails: int(middleware.PasswordResetsByEmail.Limit)},
		{email: "nobody@example.org", wantMails: 0},
	} {
		t.Run(tt.email, func(t *testing.T) {
			_, queries, deliverer, mux := newRateLimitedHandler()

			// Each request comes from another network, so only the
			// address's own limit applies.
			for i := 1; i <= int(middleware.PasswordResetsByEmail.Limit); i++ {
				rec := serve(mux, postForm("/auth/password", fmt.Sprintf("192.0.2.%d:4000", i), url.Values{"email": {tt.email}}))
				if rec.Code != http.StatusOK {
					t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
				}
			}
			lookups := queries.emailLookups
			rec := serve(mux, postForm("/auth/password", "198.51.100.200:4000", url.Values{"email": {strings.ToUpper(tt.email)}}))
			assertThrottledPage(t, rec, middleware.PasswordResetsByEmail)

			if queries.emailLookups != lookups {
				t.Errorf("a refused request looked the account up")
			}
			if len(deliverer.mails) != tt.wantMails || len(queries.resetTokenSets) != tt.wantMails {
				t.Errorf("mails = %d, reset tokens = %d, want %d of each", len(deliverer.mails), len(queries.resetTokenSets), tt.wantMails)
			}
		})
	}
}

func TestPasswordResetThrottlingDoesNotDiscloseAccounts(t *testing.T) {
	_, _, _, mux := newRateLimitedHandler()

	for i := 1; i <= int(middleware.PasswordResetsByEmail.Limit)+2; i++ {
		existing := serve(mux, postForm("/auth/password", "203.0.113.5:4000", url.Values{"email": {"user@example.org"}}))
		unknown := serve(mux, postForm("/auth/password", "198.51.100.9:4000", url.Values{"email": {"nobody@example.org"}}))

		if existing.Code != unknown.Code || existing.Body.String() != unknown.Body.String() {
			t.Fatalf("request %d: existing account got %d %s, unknown account got %d %s",
				i, existing.Code, existing.Body.String(), unknown.Code, unknown.Body.String())
		}
		for _, header := range []string{"Content-Type", "X-RateLimit-Limit", "X-RateLimit-Remaining"} {
			if existing.Header().Get(header) != unknown.Header().Get(header) {
				t.Fatalf("request %d: %s = %q for the existing account, %q for the unknown one",
					i, header, existing.Header().Get(header), unknown.Header().Get(header))
			}
		}
		want := http.StatusOK
		if i > int(middleware.PasswordResetsByEmail.Limit) {
			want = http.StatusTooManyRequests
		}
		if existing.Code != want {
			t.Fatalf("request %d: status = %d, want %d", i, existing.Code, want)
		}
	}
}

func TestPasswordResetLimitPerNetwork(t *testing.T) {
	_, queries, _, mux := newRateLimitedHandler()

	for i := 1; i <= int(middleware.PasswordResetsByIP.Limit); i++ {
		rec := serve(mux, postForm("/auth/password", "203.0.113.5:4000", url.Values{"email": {fmt.Sprintf("guess%d@example.org", i)}}))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
	assertThrottledPage(t, serve(mux, postForm("/auth/password", "203.0.113.5:4000", url.Values{"email": {"user@example.org"}})), middleware.PasswordResetsByIP)

	// Setting a password with a reset token shares the network's limit,
	// through the form's method override and through PUT alike.
	queries.usersByResetToken["tok"] = db.User{ID: 7, ResetPasswordSentAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
	override := resetForm("tok", "a-new-password", "a-new-password")
	override.RemoteAddr = "203.0.113.5:4000"
	assertThrottledPage(t, serve(mux, override), middleware.PasswordResetsByIP)

	put := resetForm("tok", "a-new-password", "a-new-password")
	put.Method = http.MethodPut
	put.RemoteAddr = "203.0.113.5:4000"
	assertThrottledPage(t, serve(mux, put), middleware.PasswordResetsByIP)

	if queries.tokenLookups != 0 || len(queries.passwordSets) != 0 {
		t.Fatalf("a refused password change looked up its token or set the password")
	}

	// Another network is not affected.
	other := resetForm("tok", "a-new-password", "a-new-password")
	other.RemoteAddr = "198.51.100.9:4000"
	if rec := serve(mux, other); rec.Code != http.StatusOK || len(queries.passwordSets) != 1 {
		t.Fatalf("other network: status = %d, password sets = %d, want 200 and 1", rec.Code, len(queries.passwordSets))
	}
}

func TestPasswordResetForwardingHeadersDoNotBypassLimits(t *testing.T) {
	_, _, _, mux := newRateLimitedHandler()
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	server := middleware.ResolveClientIP(middleware.NewClientIPResolver(trusted))(mux)

	for i := 1; i <= int(middleware.PasswordResetsByIP.Limit)+1; i++ {
		req := postForm("/auth/password", "203.0.113.5:4000", url.Values{"email": {fmt.Sprintf("guess%d@example.org", i)}})
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		rec := serve(server, req)
		if i <= int(middleware.PasswordResetsByIP.Limit) {
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
			}
			continue
		}
		assertThrottledPage(t, rec, middleware.PasswordResetsByIP)
	}
}

func TestResendConfirmationRepeatedRequestsAreThrottled(t *testing.T) {
	for _, tt := range []struct {
		email     string
		wantMails int
	}{
		{email: "user@example.org", wantMails: int(middleware.EmailConfirmationsByEmail.Limit)},
		{email: "nobody@example.org", wantMails: 0},
	} {
		t.Run(tt.email, func(t *testing.T) {
			_, queries, deliverer, mux := newRateLimitedHandler()

			for i := 1; i <= int(middleware.EmailConfirmationsByEmail.Limit); i++ {
				rec := serve(mux, postForm("/auth/confirmation", fmt.Sprintf("192.0.2.%d:4000", i), url.Values{"email": {tt.email}}))
				if rec.Code != http.StatusOK {
					t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
				}
			}
			lookups := queries.emailLookups
			assertThrottledPage(t, serve(mux, postForm("/auth/confirmation", "198.51.100.200:4000", url.Values{"email": {tt.email}})), middleware.EmailConfirmationsByEmail)
			if queries.emailLookups != lookups {
				t.Errorf("a refused request looked the account up")
			}
			if got := len(deliverer.mails); got != tt.wantMails {
				t.Errorf("mails = %d, want %d", got, tt.wantMails)
			}
		})
	}
}

func TestResendConfirmationLimitPerNetwork(t *testing.T) {
	_, _, _, mux := newRateLimitedHandler()

	for i := 1; i <= int(middleware.EmailConfirmationsByIP.Limit); i++ {
		rec := serve(mux, postForm("/auth/confirmation", "203.0.113.5:4000", url.Values{"email": {fmt.Sprintf("guess%d@example.org", i)}}))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
	assertThrottledPage(t, serve(mux, postForm("/auth/confirmation", "203.0.113.5:4000", url.Values{"email": {"user@example.org"}})), middleware.EmailConfirmationsByIP)
}

type failingRateLimitStore struct{}

func (failingRateLimitStore) Hit(context.Context, string, time.Duration) (int64, time.Duration, error) {
	return 0, 0, errors.New("redis down")
}

func TestUncountableRequestsAreRefused(t *testing.T) {
	h, queries, deliverer, mux := newRateLimitedHandler()
	h.SetRateLimiter(middleware.NewRateLimiter(failingRateLimitStore{}))

	for _, path := range []string{"/auth/password", "/auth/confirmation"} {
		rec := serve(mux, postForm(path, "203.0.113.5:4000", url.Values{"email": {"user@example.org"}}))
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: status = %d, Retry-After = %q, want 503 with Retry-After", path, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	if queries.emailLookups != 0 || len(deliverer.mails) != 0 {
		t.Errorf("uncounted requests looked up %d accounts and sent %d mails", queries.emailLookups, len(deliverer.mails))
	}
}
