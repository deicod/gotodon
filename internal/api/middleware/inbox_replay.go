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
//
// A claim is renewed while its delivery is processed. Processing is cancelled
// when the claim is lost or cannot be renewed before it lapses, so a duplicate
// never runs alongside it.
const (
	// inboxReplayLease bounds how long a claim outlives its last renewal, and
	// so how long it blocks duplicates of a delivery whose processing never
	// finished, for instance after a crash.
	inboxReplayLease = 2 * time.Minute
	// inboxReplayRenewInterval is how often a held claim is renewed.
	inboxReplayRenewInterval = inboxReplayLease / 4
	// inboxReplayClockMargin extends retention past the signature's validity
	// to cover clock drift between the instances verifying signatures and the
	// store forgetting deliveries.
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

// errReplayClaimLost is the cause with which processing is cancelled once its
// claim is lost or about to lapse.
var errReplayClaimLost = errors.New("inbox replay claim lost")

// InboxReplayStore tracks which inbox deliveries are being or have been
// processed. Implementations must be safe for concurrent use, and server
// instances sharing an inbox must share the store.
//
// Every delivery of a key states until when a copy of it could be replayed.
// A key is remembered until the latest deadline of all its deliveries,
// whether they were processed, refused while the key was in flight, or seen
// by a claim that was released or lapsed, so that a completed key cannot be
// forgotten while any copy of it still verifies.
//
// Leases are durations measured by the store. A store shared by several
// instances measures them on one clock, so that instances whose clocks
// disagree still agree on when a claim lapses.
type InboxReplayStore interface {
	// Claim records retainUntil as a deadline of key and claims key under
	// token for at most lease, unless key is in flight under another live
	// claim or already processed.
	Claim(ctx context.Context, key, token string, lease time.Duration, retainUntil time.Time) (ReplayState, error)
	// Renew extends the claim held under token to lease from now and reports
	// whether that claim was still held.
	Renew(ctx context.Context, key, token string, lease time.Duration) (bool, error)
	// Complete records key as processed until at least retainUntil and every
	// deadline recorded for it.
	Complete(ctx context.Context, key string, retainUntil time.Time) error
	// Release drops the claim held under token so that a later delivery can
	// process key, keeping its deadlines. Claims under other tokens and
	// processed keys are kept.
	Release(ctx context.Context, key, token string) error
}

// replayTimings sets how claims are held; tests shorten them.
type replayTimings struct {
	lease      time.Duration
	renewEvery time.Duration
}

var defaultReplayTimings = replayTimings{lease: inboxReplayLease, renewEvery: inboxReplayRenewInterval}

// RejectInboxReplays ensures each signed inbox request is processed at most
// once while its signature can still verify. It must run after
// VerifyHTTPSignature, whose SignedActivity identifies the request.
func RejectInboxReplays(store InboxReplayStore) func(http.Handler) http.Handler {
	return rejectInboxReplays(store, defaultReplayTimings)
}

func rejectInboxReplays(store InboxReplayStore, timings replayTimings) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			activity, ok := SignedActivityFromContext(r.Context())
			if !ok || store == nil {
				web.JSONError(w, http.StatusInternalServerError, "Replay protection unavailable")
				return
			}

			key := inboxReplayKey(activity, r.URL.Path)
			token := rand.Text()
			claimedAt := time.Now()
			retainUntil := activity.ValidUntil.Add(inboxReplayClockMargin)
			state, err := store.Claim(r.Context(), key, token, timings.lease, retainUntil)
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

			ctx, cancel := context.WithCancelCause(r.Context())
			defer cancel(nil)
			stopRenewal := holdReplayClaim(storeCtx, store, key, token, claimedAt.Add(timings.lease), timings, cancel)

			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			func() {
				defer stopRenewal()
				next.ServeHTTP(rw, r.WithContext(ctx))
			}()

			if rw.status >= 200 && rw.status < 300 {
				completed = store.Complete(storeCtx, key, retainUntil) == nil
			}
		})
	}
}

// holdReplayClaim renews the claim on key until the returned function is
// called; heldUntil is when the claim lapses without renewal. When the claim
// turns out to be lost, or a failed renewal leaves it to lapse before the next
// attempt, cancel stops processing so a duplicate cannot run alongside it.
func holdReplayClaim(ctx context.Context, store InboxReplayStore, key, token string, heldUntil time.Time, timings replayTimings, cancel context.CancelCauseFunc) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(timings.renewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}

			attempt := time.Now()
			renewCtx, cancelRenew := context.WithTimeout(ctx, timings.renewEvery)
			held, err := store.Renew(renewCtx, key, token, timings.lease)
			cancelRenew()
			switch {
			case err == nil && held:
				heldUntil = attempt.Add(timings.lease)
			case err == nil, time.Until(heldUntil) <= timings.renewEvery:
				cancel(errReplayClaimLost)
				return
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
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
	// token holds the claim token while the delivery is claimed.
	token string
	// leaseUntil ends the claim.
	leaseUntil time.Time
	// processed reports that the delivery has been processed.
	processed bool
	// retainUntil is the latest retention deadline recorded for the delivery.
	retainUntil time.Time
}

// expires is when the entry can be forgotten: its claim has ended and no
// recorded delivery can verify any more.
func (e memoryReplayEntry) expires() time.Time {
	return laterTime(e.leaseUntil, e.retainUntil)
}

func (e memoryReplayEntry) inFlight(now time.Time) bool {
	return !e.processed && e.token != "" && now.Before(e.leaseUntil)
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
func (s *MemoryInboxReplayStore) Claim(ctx context.Context, key, token string, lease time.Duration, retainUntil time.Time) (ReplayState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweepLocked(now, memoryReplaySweepInterval)

	entry, ok := s.entries[key]
	if ok && !now.Before(entry.expires()) {
		delete(s.entries, key)
		ok = false
	}
	if ok {
		entry.retainUntil = laterTime(entry.retainUntil, retainUntil)
		state := ReplayClaimed
		switch {
		case entry.processed:
			state = ReplayProcessed
		case entry.inFlight(now):
			state = ReplayInFlight
		default:
			// Released, or a claim that lapsed: take it over, keeping the
			// deadlines recorded so far.
			entry.token, entry.leaseUntil = token, now.Add(lease)
		}
		s.entries[key] = entry
		return state, nil
	}

	if len(s.entries) >= s.capacity {
		s.sweepLocked(now, memoryReplayFullSweepInterval)
		if len(s.entries) >= s.capacity {
			return 0, ErrReplayStoreFull
		}
	}
	s.entries[key] = memoryReplayEntry{token: token, leaseUntil: now.Add(lease), retainUntil: retainUntil}
	return ReplayClaimed, nil
}

// Renew implements InboxReplayStore.
func (s *MemoryInboxReplayStore) Renew(ctx context.Context, key, token string, lease time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	entry, ok := s.entries[key]
	if !ok || entry.token != token || !entry.inFlight(now) {
		return false, nil
	}
	entry.leaseUntil = laterTime(entry.leaseUntil, now.Add(lease))
	s.entries[key] = entry
	return true, nil
}

// Complete implements InboxReplayStore.
func (s *MemoryInboxReplayStore) Complete(ctx context.Context, key string, retainUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.entries[key]
	entry.token = ""
	entry.processed = true
	entry.retainUntil = laterTime(entry.retainUntil, retainUntil)
	s.entries[key] = entry
	return nil
}

// Release implements InboxReplayStore.
func (s *MemoryInboxReplayStore) Release(ctx context.Context, key, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[key]
	if !ok || entry.processed || entry.token == "" || entry.token != token {
		return nil
	}
	// The deadlines stay recorded for a later delivery that completes.
	entry.token, entry.leaseUntil = "", time.Time{}
	if s.now().Before(entry.expires()) {
		s.entries[key] = entry
	} else {
		delete(s.entries, key)
	}
	return nil
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// sweepLocked drops expired entries unless the last sweep ran less than
// interval ago.
func (s *MemoryInboxReplayStore) sweepLocked(now time.Time, interval time.Duration) {
	if now.Sub(s.lastSweep) < interval {
		return
	}
	for key, entry := range s.entries {
		if !now.Before(entry.expires()) {
			delete(s.entries, key)
		}
	}
	s.lastSweep = now
}
