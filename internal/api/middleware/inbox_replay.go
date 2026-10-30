package middleware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/deicod/gotodon/internal/web"
)

// Inbox replay protection.
//
// A signed inbox request keeps verifying until its signature expires, so a
// captured request can be replayed until then. RejectInboxReplays processes
// each signed request at most once: a request is identified by its verified
// actor, its path and its body digest, all of which the signature binds, and
// is remembered until its signature stops verifying. A legitimate redelivery
// signs again and so verifies independently, but carries the same body and
// therefore the same identity:
//   - a delivery that was processed (a 2xx response) is answered with
//     202 Accepted and not processed again;
//   - a delivery whose processing failed is forgotten, so a retry is processed;
//   - a delivery arriving while the same request is being processed is answered
//     with 503 and Retry-After, so the sender retries once the outcome is known.
const (
	// inboxReplayLease bounds how long a claim blocks duplicates of an
	// activity whose processing never finished, for instance after a crash.
	inboxReplayLease = 5 * time.Minute
	// inboxReplayClockMargin extends retention past the signature's validity
	// to cover clock drift between server instances sharing the store.
	inboxReplayClockMargin = 5 * time.Minute
	// inboxReplayRetryAfter is the Retry-After for deliveries that cannot be
	// processed yet.
	inboxReplayRetryAfter = time.Minute
)

// ReplayState is the outcome of claiming an inbox delivery.
type ReplayState int

const (
	// ReplayClaimed means the caller holds the claim and must process the delivery.
	ReplayClaimed ReplayState = iota
	// ReplayInFlight means the same delivery is being processed under another claim.
	ReplayInFlight
	// ReplayProcessed means the delivery has already been processed.
	ReplayProcessed
)

// ErrReplayStoreFull is returned by MemoryInboxReplayStore when it cannot
// track another delivery.
var ErrReplayStoreFull = errors.New("inbox replay store is full")

// InboxReplayStore tracks which inbox deliveries are being or have been
// processed. Implementations must be safe for concurrent use, and server
// instances sharing an inbox must share the store.
type InboxReplayStore interface {
	// Claim atomically claims key under token for at most lease when key is
	// unknown. For a processed key it extends retention to at least retain
	// and reports ReplayProcessed.
	Claim(ctx context.Context, key, token string, lease, retain time.Duration) (ReplayState, error)
	// Complete records key as processed for at least retain.
	Complete(ctx context.Context, key string, retain time.Duration) error
	// Release drops the claim held under token so that a later delivery can
	// process key. Claims under other tokens and processed keys are kept.
	Release(ctx context.Context, key, token string) error
}

// RejectInboxReplays ensures each signed inbox request is processed at most
// once while its signature can still verify. It must run after
// VerifyHTTPSignature, whose SignedActivity identifies the request.
func RejectInboxReplays(store InboxReplayStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			activity, ok := SignedActivityFromContext(r.Context())
			if !ok || store == nil {
				web.JSONError(w, http.StatusInternalServerError, "Replay protection unavailable")
				return
			}

			key := inboxReplayKey(activity, r.URL.Path)
			token := rand.Text()
			state, err := store.Claim(r.Context(), key, token, inboxReplayLease, inboxReplayRetention(activity))
			if err != nil {
				writeRetryLater(w, "Replay protection unavailable")
				return
			}
			switch state {
			case ReplayProcessed:
				w.WriteHeader(http.StatusAccepted)
				return
			case ReplayInFlight:
				writeRetryLater(w, "Activity is already being processed")
				return
			}

			// The outcome must be recorded even when the client hung up.
			storeCtx := context.WithoutCancel(r.Context())
			completed := false
			defer func() {
				if !completed {
					_ = store.Release(storeCtx, key, token)
				}
			}()

			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			if rw.status >= 200 && rw.status < 300 {
				completed = store.Complete(storeCtx, key, inboxReplayRetention(activity)) == nil
			}
		})
	}
}

// inboxReplayKey identifies a signed request by what its signature binds: the
// signer's actor, the request path and the body.
func inboxReplayKey(activity SignedActivity, path string) string {
	h := sha256.New()
	h.Write([]byte(activity.ActorURI))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(activity.BodyDigest[:])
	return hex.EncodeToString(h.Sum(nil))
}

// inboxReplayRetention is how long a delivery must be remembered from now:
// until its signature stops verifying, plus the clock margin.
func inboxReplayRetention(activity SignedActivity) time.Duration {
	return time.Until(activity.ValidUntil) + inboxReplayClockMargin
}

func writeRetryLater(w http.ResponseWriter, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(int(inboxReplayRetryAfter/time.Second)))
	web.JSONError(w, http.StatusServiceUnavailable, message)
}

// DefaultMemoryInboxReplayCapacity is the number of deliveries a
// MemoryInboxReplayStore tracks by default.
const DefaultMemoryInboxReplayCapacity = 1 << 19

// Expired entries are dropped once per memoryReplaySweepInterval, and at
// most once per memoryReplayFullSweepInterval while the store is full, so a
// flood of claims against a full store cannot turn every claim into a scan.
const (
	memoryReplaySweepInterval     = time.Minute
	memoryReplayFullSweepInterval = time.Second
)

// MemoryInboxReplayStore is an InboxReplayStore held in process memory. It
// protects a single server instance; instances sharing an inbox need a shared
// store such as RedisInboxReplayStore. Once capacity live entries are tracked
// it refuses new claims with ErrReplayStoreFull rather than forgetting
// deliveries whose signatures are still valid.
type MemoryInboxReplayStore struct {
	mu        sync.Mutex
	entries   map[string]memoryReplayEntry
	capacity  int
	now       func() time.Time
	lastSweep time.Time
}

type memoryReplayEntry struct {
	// token holds the claim token, or is empty once the delivery is processed.
	token   string
	expires time.Time
}

// NewMemoryInboxReplayStore creates an in-memory store tracking at most
// capacity deliveries; capacity <= 0 selects DefaultMemoryInboxReplayCapacity.
func NewMemoryInboxReplayStore(capacity int) *MemoryInboxReplayStore {
	if capacity <= 0 {
		capacity = DefaultMemoryInboxReplayCapacity
	}
	return &MemoryInboxReplayStore{
		entries:  make(map[string]memoryReplayEntry),
		capacity: capacity,
		now:      time.Now,
	}
}

// Claim implements InboxReplayStore.
func (s *MemoryInboxReplayStore) Claim(ctx context.Context, key, token string, lease, retain time.Duration) (ReplayState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepLocked(now, memoryReplaySweepInterval)

	if entry, ok := s.entries[key]; ok && now.Before(entry.expires) {
		if entry.token != "" {
			return ReplayInFlight, nil
		}
		if until := now.Add(retain); until.After(entry.expires) {
			entry.expires = until
			s.entries[key] = entry
		}
		return ReplayProcessed, nil
	}

	delete(s.entries, key)
	if len(s.entries) >= s.capacity {
		s.sweepLocked(now, memoryReplayFullSweepInterval)
		if len(s.entries) >= s.capacity {
			return 0, ErrReplayStoreFull
		}
	}
	s.entries[key] = memoryReplayEntry{token: token, expires: now.Add(lease)}
	return ReplayClaimed, nil
}

// Complete implements InboxReplayStore.
func (s *MemoryInboxReplayStore) Complete(ctx context.Context, key string, retain time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	expires := now.Add(retain)
	if entry, ok := s.entries[key]; ok && entry.expires.After(expires) {
		expires = entry.expires
	}
	s.entries[key] = memoryReplayEntry{expires: expires}
	return nil
}

// Release implements InboxReplayStore.
func (s *MemoryInboxReplayStore) Release(ctx context.Context, key, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.entries[key]; ok && entry.token != "" && entry.token == token {
		delete(s.entries, key)
	}
	return nil
}

// sweepLocked drops expired entries unless the last sweep ran less than
// interval ago.
func (s *MemoryInboxReplayStore) sweepLocked(now time.Time, interval time.Duration) {
	if now.Sub(s.lastSweep) < interval {
		return
	}
	for key, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, key)
		}
	}
	s.lastSweep = now
}
