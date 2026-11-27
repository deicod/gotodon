package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deicod/gotodon/internal/web"
)

// Rate limits on anonymous credential and mail endpoints.
//
// Each limit allows Limit attempts per Period for one discriminator: the
// client's network or the submitted email address. A window starts with the
// first attempt and lasts Period; attempts beyond Limit in it are refused
// with 429 Too Many Requests until it ends. Email addresses are compared
// case-insensitively, as accounts are looked up, and counted whether or not
// an account has them, before any lookup, so a refusal says nothing about
// which accounts exist. The values follow Mastodon's Rack::Attack
// configuration.
var (
	// LoginAttemptsByIP limits password sign-ins (POST /oauth/authorize) per
	// client network.
	LoginAttemptsByIP = RateLimit{Name: "login_attempts/ip", Limit: 25, Period: 5 * time.Minute}
	// LoginAttemptsByEmail limits password sign-ins per email address.
	LoginAttemptsByEmail = RateLimit{Name: "login_attempts/email", Limit: 25, Period: time.Hour}
	// PasswordResetsByIP limits password reset requests and password changes
	// with a reset token (POST and PUT /auth/password) per client network.
	PasswordResetsByIP = RateLimit{Name: "password_resets/ip", Limit: 25, Period: 5 * time.Minute}
	// PasswordResetsByEmail limits password reset requests per email address.
	PasswordResetsByEmail = RateLimit{Name: "password_resets/email", Limit: 5, Period: 30 * time.Minute}
	// EmailConfirmationsByIP limits requests to resend confirmation
	// instructions (POST /auth/confirmation) per client network.
	EmailConfirmationsByIP = RateLimit{Name: "email_confirmations/ip", Limit: 25, Period: 5 * time.Minute}
	// EmailConfirmationsByEmail limits requests to resend confirmation
	// instructions per email address.
	EmailConfirmationsByEmail = RateLimit{Name: "email_confirmations/email", Limit: 5, Period: 30 * time.Minute}
)

// rateLimitRetryAfter is the Retry-After for attempts that could not be
// counted.
const rateLimitRetryAfter = time.Minute

// RateLimit allows Limit attempts per Period for each discriminator.
type RateLimit struct {
	// Name identifies the limit in store keys.
	Name   string
	Limit  int64
	Period time.Duration
}

// RateLimitCheck applies Limit to the attempts sharing Key.
type RateLimitCheck struct {
	Limit RateLimit
	// Key is the discriminator, such as RateLimitClientKey or
	// RateLimitEmailKey. An empty key is not counted.
	Key string
}

// RateLimitDecision is the outcome of counting an attempt.
type RateLimitDecision struct {
	Allowed bool
	// Limit is the limit that refused the attempt, or the last one checked.
	Limit RateLimit
	// Remaining is how many more attempts that limit allows in its window.
	Remaining int64
	// Reset is when its window ends.
	Reset time.Time
}

// RateLimitStore counts attempts in windows. Implementations must be safe
// for concurrent use, and server instances must share a store for their
// limits to apply across instances.
type RateLimitStore interface {
	// Hit counts an attempt against key and returns the attempts counted in
	// key's current window, this one included, and how long that window
	// lasts from now. A window starts with the first attempt and lasts
	// period.
	Hit(ctx context.Context, key string, period time.Duration) (count int64, ttl time.Duration, err error)
}

// RateLimiter applies rate limits with a store.
type RateLimiter struct {
	store RateLimitStore
	now   func() time.Time
}

// NewRateLimiter creates a rate limiter counting attempts in store.
func NewRateLimiter(store RateLimitStore) *RateLimiter {
	return &RateLimiter{store: store, now: time.Now}
}

// Check counts an attempt against each check in order. It stops at the
// first limit the attempt exceeds, so a refused attempt is not counted
// against the later checks, and returns that limit's decision.
func (l *RateLimiter) Check(ctx context.Context, checks ...RateLimitCheck) (RateLimitDecision, error) {
	decision := RateLimitDecision{Allowed: true}
	for _, check := range checks {
		if check.Key == "" {
			continue
		}
		count, ttl, err := l.store.Hit(ctx, rateLimitStoreKey(check), check.Limit.Period)
		if err != nil {
			return RateLimitDecision{}, err
		}
		decision = RateLimitDecision{
			Allowed:   count <= check.Limit.Limit,
			Limit:     check.Limit,
			Remaining: max(check.Limit.Limit-count, 0),
			Reset:     l.now().Add(ttl),
		}
		if !decision.Allowed {
			return decision, nil
		}
	}
	return decision, nil
}

// rateLimitStoreKey hashes the discriminator, so that store keys have a
// bounded length and do not hold email addresses.
func rateLimitStoreKey(check RateLimitCheck) string {
	sum := sha256.Sum256([]byte(check.Key))
	return check.Limit.Name + ":" + hex.EncodeToString(sum[:])
}

// RateLimitClientKey returns the discriminator of r's client network: its
// address as ClientIP reports it, or for IPv6 its /64, which a single
// subscriber typically holds whole.
func RateLimitClientKey(r *http.Request) string {
	ip := ClientIP(r)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		// Requests without a usable address share one discriminator.
		return "unknown:" + ip
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

// RateLimitEmailKey returns the discriminator of a submitted email address:
// accounts are looked up by email case-insensitively, so all spellings of
// an address share one. An empty address yields an empty key.
func RateLimitEmailKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// SetRateLimitHeaders describes a refusal in the X-RateLimit headers
// Mastodon sends, and in Retry-After.
func SetRateLimitHeaders(w http.ResponseWriter, decision RateLimitDecision, now time.Time) {
	retryAfter := int64(decision.Reset.Sub(now).Round(time.Second) / time.Second)
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(decision.Limit.Limit, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(decision.Remaining, 10))
	w.Header().Set("X-RateLimit-Reset", decision.Reset.UTC().Format("2006-01-02T15:04:05.000000Z"))
	w.Header().Set("Retry-After", strconv.FormatInt(max(retryAfter, 1), 10))
}

// WriteRateLimited answers a refused attempt with 429 and Mastodon's JSON
// error body.
func WriteRateLimited(w http.ResponseWriter, decision RateLimitDecision) {
	SetRateLimitHeaders(w, decision, time.Now())
	web.JSONError(w, http.StatusTooManyRequests, "Too many requests")
}

// SetRateLimitUnavailableHeaders sets Retry-After on the 503 answering an
// attempt that could not be counted. Such attempts are refused rather than
// let through uncounted.
func SetRateLimitUnavailableHeaders(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(rateLimitRetryAfter/time.Second)))
}

// WriteRateLimitUnavailable answers an attempt that could not be counted
// with 503 and Retry-After.
func WriteRateLimitUnavailable(w http.ResponseWriter) {
	SetRateLimitUnavailableHeaders(w)
	web.JSONError(w, http.StatusServiceUnavailable, "Rate limiting unavailable")
}

// ErrRateLimitStoreFull is returned by MemoryRateLimitStore when it cannot
// track another key.
var ErrRateLimitStoreFull = errors.New("rate limit store is full")

// DefaultMemoryRateLimitCapacity is the number of keys a
// MemoryRateLimitStore tracks by default.
const DefaultMemoryRateLimitCapacity = 1 << 16

// Expired windows are dropped once per memoryRateLimitSweepInterval, and at
// most once per memoryRateLimitFullSweepInterval while the store is full, so
// a flood of new keys against a full store cannot turn every hit into a scan.
const (
	memoryRateLimitSweepInterval     = time.Minute
	memoryRateLimitFullSweepInterval = time.Second
)

// MemoryRateLimitStore is a RateLimitStore held in process memory. It limits
// a single server instance; instances behind one load balancer need a
// shared store such as RedisRateLimitStore. Once capacity live windows are
// tracked it refuses new keys with ErrRateLimitStoreFull rather than
// forgetting windows that still limit attempts.
type MemoryRateLimitStore struct {
	mu        sync.Mutex
	windows   map[string]memoryRateLimitWindow
	capacity  int
	now       func() time.Time
	lastSweep time.Time
}

type memoryRateLimitWindow struct {
	count int64
	ends  time.Time
}

// NewMemoryRateLimitStore creates an in-memory store tracking at most
// capacity keys; capacity <= 0 selects DefaultMemoryRateLimitCapacity.
func NewMemoryRateLimitStore(capacity int) *MemoryRateLimitStore {
	if capacity <= 0 {
		capacity = DefaultMemoryRateLimitCapacity
	}
	return &MemoryRateLimitStore{
		windows:  make(map[string]memoryRateLimitWindow),
		capacity: capacity,
		now:      time.Now,
	}
}

// Hit implements RateLimitStore.
func (s *MemoryRateLimitStore) Hit(_ context.Context, key string, period time.Duration) (int64, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepLocked(now, memoryRateLimitSweepInterval)

	window, ok := s.windows[key]
	if ok && now.Before(window.ends) {
		window.count++
		s.windows[key] = window
		return window.count, window.ends.Sub(now), nil
	}
	if !ok && len(s.windows) >= s.capacity {
		s.sweepLocked(now, memoryRateLimitFullSweepInterval)
		if len(s.windows) >= s.capacity {
			return 0, 0, ErrRateLimitStoreFull
		}
	}
	s.windows[key] = memoryRateLimitWindow{count: 1, ends: now.Add(period)}
	return 1, period, nil
}

// sweepLocked drops ended windows unless the last sweep ran less than
// interval ago.
func (s *MemoryRateLimitStore) sweepLocked(now time.Time, interval time.Duration) {
	if now.Sub(s.lastSweep) < interval {
		return
	}
	for key, window := range s.windows {
		if !now.Before(window.ends) {
			delete(s.windows, key)
		}
	}
	s.lastSweep = now
}
