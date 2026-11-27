package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// testRateLimitStore checks the RateLimitStore contract that does not depend
// on the passage of time.
func testRateLimitStore(t *testing.T, store RateLimitStore) {
	t.Helper()
	ctx := context.Background()
	const period = time.Hour

	for want := int64(1); want <= 3; want++ {
		count, ttl, err := store.Hit(ctx, "a", period)
		if err != nil {
			t.Fatalf("Hit(a) #%d error = %v", want, err)
		}
		if count != want {
			t.Fatalf("Hit(a) #%d count = %d, want %d", want, count, want)
		}
		if ttl <= period-time.Minute || ttl > period {
			t.Fatalf("Hit(a) #%d ttl = %v, want about %v", want, ttl, period)
		}
	}

	count, _, err := store.Hit(ctx, "b", period)
	if err != nil || count != 1 {
		t.Fatalf("Hit(b) = %d, %v, want a separate count of 1", count, err)
	}
}

func TestMemoryRateLimitStore(t *testing.T) {
	testRateLimitStore(t, NewMemoryRateLimitStore(0))
}

func TestMemoryRateLimitStoreWindowEnds(t *testing.T) {
	store := NewMemoryRateLimitStore(0)
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	ctx := context.Background()

	if count, ttl, _ := store.Hit(ctx, "k", time.Minute); count != 1 || ttl != time.Minute {
		t.Fatalf("first Hit = %d, %v, want 1, 1m", count, ttl)
	}
	now = now.Add(40 * time.Second)
	// The window started with the first hit; later hits do not extend it.
	if count, ttl, _ := store.Hit(ctx, "k", time.Minute); count != 2 || ttl != 20*time.Second {
		t.Fatalf("second Hit = %d, %v, want 2, 20s", count, ttl)
	}
	now = now.Add(20 * time.Second)
	if count, ttl, _ := store.Hit(ctx, "k", time.Minute); count != 1 || ttl != time.Minute {
		t.Fatalf("Hit after the window = %d, %v, want a new window", count, ttl)
	}
}

func TestMemoryRateLimitStoreCapacity(t *testing.T) {
	store := NewMemoryRateLimitStore(2)
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	ctx := context.Background()

	for _, key := range []string{"a", "b"} {
		if _, _, err := store.Hit(ctx, key, time.Minute); err != nil {
			t.Fatalf("Hit(%s) error = %v", key, err)
		}
	}
	if _, _, err := store.Hit(ctx, "c", time.Minute); !errors.Is(err, ErrRateLimitStoreFull) {
		t.Fatalf("Hit(c) on a full store error = %v, want ErrRateLimitStoreFull", err)
	}
	// Keys already tracked keep counting.
	if count, _, err := store.Hit(ctx, "a", time.Minute); err != nil || count != 2 {
		t.Fatalf("Hit(a) on a full store = %d, %v, want 2", count, err)
	}

	// Ended windows make room again.
	now = now.Add(time.Minute)
	if count, _, err := store.Hit(ctx, "c", time.Minute); err != nil || count != 1 {
		t.Fatalf("Hit(c) after windows ended = %d, %v, want 1", count, err)
	}
	if len(store.windows) != 1 {
		t.Errorf("tracked windows = %d, want only c's", len(store.windows))
	}
}

type failingRateLimitStore struct{ err error }

func (s failingRateLimitStore) Hit(context.Context, string, time.Duration) (int64, time.Duration, error) {
	return 0, 0, s.err
}

type recordingRateLimitStore struct {
	*MemoryRateLimitStore
	keys []string
}

func (s *recordingRateLimitStore) Hit(ctx context.Context, key string, period time.Duration) (int64, time.Duration, error) {
	s.keys = append(s.keys, key)
	return s.MemoryRateLimitStore.Hit(ctx, key, period)
}

func TestRateLimiterCheck(t *testing.T) {
	perIP := RateLimit{Name: "test/ip", Limit: 3, Period: time.Minute}
	perEmail := RateLimit{Name: "test/email", Limit: 2, Period: time.Hour}
	store := &recordingRateLimitStore{MemoryRateLimitStore: NewMemoryRateLimitStore(0)}
	limiter := NewRateLimiter(store)
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	store.now = limiter.now
	ctx := context.Background()

	check := func(email string) RateLimitDecision {
		t.Helper()
		decision, err := limiter.Check(ctx,
			RateLimitCheck{Limit: perIP, Key: "192.0.2.1"},
			RateLimitCheck{Limit: perEmail, Key: email},
		)
		if err != nil {
			t.Fatalf("Check error = %v", err)
		}
		return decision
	}

	if d := check("a@example.com"); !d.Allowed || d.Limit != perEmail || d.Remaining != 1 || !d.Reset.Equal(now.Add(time.Hour)) {
		t.Fatalf("first attempt = %+v, want allowed with 1 remaining for the email", d)
	}
	if d := check("a@example.com"); !d.Allowed || d.Remaining != 0 {
		t.Fatalf("second attempt = %+v, want allowed with 0 remaining", d)
	}
	if d := check("a@example.com"); d.Allowed || d.Limit != perEmail || d.Remaining != 0 {
		t.Fatalf("third attempt = %+v, want refused by the email limit", d)
	}

	// The network limit refuses before the email is counted, so a client
	// over its limit cannot create counters for more addresses.
	before := len(store.keys)
	if d := check("b@example.com"); d.Allowed || d.Limit != perIP || !d.Reset.Equal(now.Add(time.Minute)) {
		t.Fatalf("fourth attempt = %+v, want refused by the network limit", d)
	}
	if counted := store.keys[before:]; len(counted) != 1 {
		t.Fatalf("refused attempt counted %d keys, want only the network's", len(counted))
	}

	// Empty keys are not counted.
	decision, err := limiter.Check(ctx, RateLimitCheck{Limit: perEmail, Key: ""})
	if err != nil || !decision.Allowed {
		t.Fatalf("Check with an empty key = %+v, %v, want allowed", decision, err)
	}

	// Store keys hold neither the limit's discriminators nor their spelling.
	for _, key := range store.keys {
		if len(key) > len(perEmail.Name)+1+64 {
			t.Errorf("store key %q is longer than a name and a hash", key)
		}
	}
}

func TestRateLimiterCheckStoreError(t *testing.T) {
	boom := errors.New("redis down")
	limiter := NewRateLimiter(failingRateLimitStore{err: boom})
	_, err := limiter.Check(context.Background(), RateLimitCheck{Limit: LoginAttemptsByIP, Key: "192.0.2.1"})
	if !errors.Is(err, boom) {
		t.Fatalf("Check error = %v, want the store's", err)
	}
}

func TestRateLimitClientKey(t *testing.T) {
	tests := []struct {
		name   string
		client string
		want   string
	}{
		{name: "IPv4", client: "198.51.100.1", want: "198.51.100.1"},
		{name: "IPv6 is grouped by /64", client: "2001:db8:1:2:3:4:5:6", want: "2001:db8:1:2::/64"},
		{name: "4in6 is IPv4", client: "::ffff:198.51.100.1", want: "198.51.100.1"},
		{name: "no address", client: "@", want: "unknown:@"},
		{name: "empty", client: "", want: "unknown:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), ContextKeyClientIP, tt.client))
			if tt.client == "" {
				req.RemoteAddr = ""
			}
			if got := RateLimitClientKey(req); got != tt.want {
				t.Errorf("RateLimitClientKey() = %q, want %q", got, tt.want)
			}
		})
	}

	// Two addresses in one /64 share a key; neighbouring networks do not.
	a := RateLimitClientKey(requestFrom("[2001:db8::1]:1"))
	b := RateLimitClientKey(requestFrom("[2001:db8::ffff:2]:1"))
	c := RateLimitClientKey(requestFrom("[2001:db8:0:1::1]:1"))
	if a != b || a == c {
		t.Errorf("keys = %q, %q, %q; want the first two equal and the third different", a, b, c)
	}
}

func requestFrom(remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func TestRateLimitEmailKey(t *testing.T) {
	if got := RateLimitEmailKey("  User@Example.COM "); got != "user@example.com" {
		t.Errorf("RateLimitEmailKey() = %q, want user@example.com", got)
	}
	if got := RateLimitEmailKey("   "); got != "" {
		t.Errorf("RateLimitEmailKey(blank) = %q, want empty", got)
	}
}

func TestWriteRateLimited(t *testing.T) {
	reset := time.Now().Add(90 * time.Second)
	rec := httptest.NewRecorder()
	WriteRateLimited(rec, RateLimitDecision{Limit: LoginAttemptsByIP, Remaining: 0, Reset: reset})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "Too many requests" {
		t.Fatalf("body = %s, want Mastodon's 429 error", rec.Body.String())
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != strconv.FormatInt(LoginAttemptsByIP.Limit, 10) {
		t.Errorf("X-RateLimit-Limit = %q", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, want 0", got)
	}
	if got, err := time.Parse(time.RFC3339Nano, rec.Header().Get("X-RateLimit-Reset")); err != nil || got.Sub(reset).Abs() > time.Microsecond {
		t.Errorf("X-RateLimit-Reset = %q, want %s", rec.Header().Get("X-RateLimit-Reset"), reset.UTC())
	}
	if got, _ := strconv.Atoi(rec.Header().Get("Retry-After")); got < 89 || got > 90 {
		t.Errorf("Retry-After = %q, want 90", rec.Header().Get("Retry-After"))
	}
}

func TestWriteRateLimitUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRateLimitUnavailable(rec)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != fmt.Sprint(int(rateLimitRetryAfter/time.Second)) {
		t.Errorf("Retry-After = %q", got)
	}
}
