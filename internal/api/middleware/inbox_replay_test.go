package middleware

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/deicod/gotodon/pkg/activitypub"
)

// replayOf returns a byte-for-byte copy of a signed inbox request, as sent
// again by an attacker who captured it.
func replayOf(req *http.Request, body []byte) *http.Request {
	replay := httptest.NewRequest(req.Method, req.URL.String(), bytes.NewReader(body))
	replay.Host = req.Host
	replay.Header = req.Header.Clone()
	return replay
}

// signedPostTo signs body for an inbox at target with HTTPSigner, the way a
// redelivery re-signs an activity.
func signedPostTo(t *testing.T, key *rsa.PrivateKey, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	signer, err := activitypub.NewHTTPSigner(sigKeyID, activitypub.EncodePrivateKey(key))
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return req
}

// signedPostDated signs body for target with HTTPSigner under the given Date.
func signedPostDated(t *testing.T, key *rsa.PrivateKey, target string, body []byte, date time.Time) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	req.Header.Set("Date", date.UTC().Format(http.TimeFormat))
	signer, err := activitypub.NewHTTPSigner(sigKeyID, activitypub.EncodePrivateKey(key))
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return req
}

// countingInbox stands in for the inbox handler: it counts the deliveries it
// processes and answers each with the next scripted status, then 202.
type countingInbox struct {
	mu       sync.Mutex
	calls    int
	statuses []int
}

func (c *countingInbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.calls++
	status := http.StatusAccepted
	if len(c.statuses) > 0 {
		status, c.statuses = c.statuses[0], c.statuses[1:]
	}
	c.mu.Unlock()
	w.WriteHeader(status)
}

func (c *countingInbox) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type replayFixture struct {
	key     *rsa.PrivateKey
	handler http.Handler
}

func newReplayFixture(t *testing.T, store InboxReplayStore, next http.Handler) replayFixture {
	t.Helper()
	return newTimedReplayFixture(t, store, next, defaultReplayTimings)
}

func newTimedReplayFixture(t *testing.T, store InboxReplayStore, next http.Handler, timings replayTimings) replayFixture {
	t.Helper()
	key, publicPEM := generateSigningKey(t)
	fetcher := &recordingActorFetcher{actors: map[string]*activitypub.Actor{
		sigActorURI: {
			Object:    activitypub.Object{ID: sigActorURI},
			PublicKey: &activitypub.PublicKey{ID: sigKeyID, Owner: sigActorURI, PublicKeyPem: publicPEM},
		},
	}}
	return replayFixture{
		key:     key,
		handler: VerifyHTTPSignature(fetcher)(rejectInboxReplays(store, timings)(next)),
	}
}

func (f replayFixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, want, rec.Body.String())
	}
}

func TestRejectInboxReplays_ProcessesEachRequestOnce(t *testing.T) {
	inbox := &countingInbox{}
	f := newReplayFixture(t, NewMemoryInboxReplayStore(0), inbox)
	body := followActivity(sigActorURI)

	req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)
	wantStatus(t, f.serve(req), http.StatusAccepted)
	if inbox.count() != 1 {
		t.Fatalf("deliveries processed = %d, want 1", inbox.count())
	}

	// A captured request replayed verbatim is acknowledged but not processed.
	for range 3 {
		wantStatus(t, f.serve(replayOf(req, body)), http.StatusAccepted)
	}
	if inbox.count() != 1 {
		t.Fatalf("deliveries processed after replays = %d, want 1", inbox.count())
	}

	// A redelivery of the same activity with a fresh signature is acknowledged
	// without being processed again.
	wantStatus(t, f.serve(signedPostTo(t, f.key, sigInboxURL, body)), http.StatusAccepted)
	if inbox.count() != 1 {
		t.Fatalf("deliveries processed after redelivery = %d, want 1", inbox.count())
	}

	// Other activities, and the same activity delivered to another inbox,
	// are processed.
	like := []byte(`{"type":"Like","actor":"` + sigActorURI + `","object":"https://local.example/statuses/1"}`)
	wantStatus(t, f.serve(newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, like)), http.StatusAccepted)
	wantStatus(t, f.serve(signedPostTo(t, f.key, "https://local.example/inbox", body)), http.StatusAccepted)
	if inbox.count() != 3 {
		t.Fatalf("deliveries processed = %d, want 3", inbox.count())
	}
}

func TestRejectInboxReplays_RetriesFailedDeliveries(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusBadRequest, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			inbox := &countingInbox{statuses: []int{status}}
			f := newReplayFixture(t, NewMemoryInboxReplayStore(0), inbox)
			body := followActivity(sigActorURI)

			req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)
			wantStatus(t, f.serve(req), status)

			// The failed delivery was not recorded, so its retry is processed.
			wantStatus(t, f.serve(replayOf(req, body)), http.StatusAccepted)
			if inbox.count() != 2 {
				t.Fatalf("deliveries processed = %d, want 2", inbox.count())
			}

			wantStatus(t, f.serve(replayOf(req, body)), http.StatusAccepted)
			if inbox.count() != 2 {
				t.Fatalf("deliveries processed after replay = %d, want 2", inbox.count())
			}
		})
	}
}

func TestRejectInboxReplays_DuplicateWhileProcessing(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusAccepted)
	})
	f := newReplayFixture(t, NewMemoryInboxReplayStore(0), next)
	body := followActivity(sigActorURI)
	req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)
	replay := replayOf(req, body)

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- f.serve(req) }()
	<-started

	rec := f.serve(replayOf(req, body))
	wantStatus(t, rec, http.StatusServiceUnavailable)
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want %q", got, "60")
	}

	close(release)
	wantStatus(t, <-done, http.StatusAccepted)

	wantStatus(t, f.serve(replay), http.StatusAccepted)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("deliveries processed = %d, want 1", calls)
	}
}

func TestRejectInboxReplays_ReleasesClaimWhenHandlerPanics(t *testing.T) {
	panicked := false
	inbox := &countingInbox{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !panicked {
			panicked = true
			panic("inbox handler failure")
		}
		inbox.ServeHTTP(w, r)
	})
	f := newReplayFixture(t, NewMemoryInboxReplayStore(0), next)
	body := followActivity(sigActorURI)
	req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("handler panic was swallowed")
			}
		}()
		f.serve(req)
	}()

	wantStatus(t, f.serve(replayOf(req, body)), http.StatusAccepted)
	if inbox.count() != 1 {
		t.Fatalf("deliveries processed after panic = %d, want 1", inbox.count())
	}
}

// contextCheckingStore fails store calls made with a cancelled context, as a
// network-backed store would.
type contextCheckingStore struct {
	InboxReplayStore
}

func (s contextCheckingStore) Complete(ctx context.Context, key string, retainUntil time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.InboxReplayStore.Complete(ctx, key, retainUntil)
}

func (s contextCheckingStore) Release(ctx context.Context, key, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.InboxReplayStore.Release(ctx, key, token)
}

func TestRejectInboxReplays_RecordsOutcomeAfterClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inbox := &countingInbox{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		inbox.ServeHTTP(w, r)
	})
	f := newReplayFixture(t, contextCheckingStore{NewMemoryInboxReplayStore(0)}, next)
	body := followActivity(sigActorURI)
	req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)

	wantStatus(t, f.serve(req.WithContext(ctx)), http.StatusAccepted)
	wantStatus(t, f.serve(replayOf(req, body)), http.StatusAccepted)
	if inbox.count() != 1 {
		t.Fatalf("deliveries processed = %d, want 1", inbox.count())
	}
}

// faultyReplayStore wraps a store with injectable failures and records the
// durations it is given.
type faultyReplayStore struct {
	InboxReplayStore
	claimErr    error
	renewErr    error
	renewLost   bool
	completeErr error

	lease                       time.Duration
	claimRetain, completeRetain time.Time
	releases                    int
}

func (s *faultyReplayStore) Claim(ctx context.Context, key, token string, lease time.Duration, retainUntil time.Time) (ReplayState, error) {
	s.lease, s.claimRetain = lease, retainUntil
	if s.claimErr != nil {
		return 0, s.claimErr
	}
	return s.InboxReplayStore.Claim(ctx, key, token, lease, retainUntil)
}

func (s *faultyReplayStore) Renew(ctx context.Context, key, token string, lease time.Duration) (bool, error) {
	if s.renewErr != nil {
		return false, s.renewErr
	}
	if s.renewLost {
		return false, nil
	}
	return s.InboxReplayStore.Renew(ctx, key, token, lease)
}

func (s *faultyReplayStore) Complete(ctx context.Context, key string, retainUntil time.Time) error {
	s.completeRetain = retainUntil
	if s.completeErr != nil {
		return s.completeErr
	}
	return s.InboxReplayStore.Complete(ctx, key, retainUntil)
}

func (s *faultyReplayStore) Release(ctx context.Context, key, token string) error {
	s.releases++
	return s.InboxReplayStore.Release(ctx, key, token)
}

func TestRejectInboxReplays_StoreFailures(t *testing.T) {
	t.Run("claim fails", func(t *testing.T) {
		inbox := &countingInbox{}
		store := &faultyReplayStore{InboxReplayStore: NewMemoryInboxReplayStore(0), claimErr: errors.New("redis down")}
		f := newReplayFixture(t, store, inbox)

		rec := f.serve(newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, followActivity(sigActorURI)))
		wantStatus(t, rec, http.StatusServiceUnavailable)
		if rec.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
		if inbox.count() != 0 {
			t.Fatalf("deliveries processed = %d, want 0", inbox.count())
		}
	})

	t.Run("store full", func(t *testing.T) {
		inbox := &countingInbox{}
		f := newReplayFixture(t, NewMemoryInboxReplayStore(1), inbox)

		wantStatus(t, f.serve(newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, followActivity(sigActorURI))), http.StatusAccepted)
		like := []byte(`{"type":"Like","actor":"` + sigActorURI + `","object":"https://local.example/statuses/1"}`)
		rec := f.serve(newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, like))
		wantStatus(t, rec, http.StatusServiceUnavailable)
		if inbox.count() != 1 {
			t.Fatalf("deliveries processed = %d, want 1", inbox.count())
		}
	})

	t.Run("complete fails", func(t *testing.T) {
		inbox := &countingInbox{}
		store := &faultyReplayStore{InboxReplayStore: NewMemoryInboxReplayStore(0), completeErr: errors.New("redis down")}
		f := newReplayFixture(t, store, inbox)
		body := followActivity(sigActorURI)
		req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)

		// The response stands; the claim is released so that the delivery is
		// not stuck in flight, leaving a retry to the handlers' idempotency.
		wantStatus(t, f.serve(req), http.StatusAccepted)
		if store.releases != 1 {
			t.Fatalf("releases = %d, want 1", store.releases)
		}
		wantStatus(t, f.serve(replayOf(req, body)), http.StatusAccepted)
		if inbox.count() != 2 {
			t.Fatalf("deliveries processed = %d, want 2", inbox.count())
		}
	})
}

func TestRejectInboxReplays_RenewsClaimWhileProcessing(t *testing.T) {
	timings := replayTimings{lease: 80 * time.Millisecond, renewEvery: 20 * time.Millisecond}
	started := make(chan struct{})
	var calls int
	var mu sync.Mutex
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			close(started)
			// Outlast the lease several times over, as a slow handler would.
			select {
			case <-time.After(400 * time.Millisecond):
			case <-r.Context().Done():
				t.Errorf("processing cancelled: %v", context.Cause(r.Context()))
			}
		}
		w.WriteHeader(http.StatusAccepted)
	})
	f := newTimedReplayFixture(t, NewMemoryInboxReplayStore(0), next, timings)
	body := followActivity(sigActorURI)
	req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, body)
	replay := replayOf(req, body)

	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- f.serve(req) }()
	<-started

	// Well past the original lease, the renewed claim still holds off the
	// duplicate.
	time.Sleep(250 * time.Millisecond)
	wantStatus(t, f.serve(replayOf(req, body)), http.StatusServiceUnavailable)

	wantStatus(t, <-done, http.StatusAccepted)
	wantStatus(t, f.serve(replay), http.StatusAccepted)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("deliveries processed = %d, want 1", calls)
	}
}

func TestRejectInboxReplays_CancelsProcessingWithoutClaim(t *testing.T) {
	timings := replayTimings{lease: 400 * time.Millisecond, renewEvery: 100 * time.Millisecond}
	tests := []struct {
		name  string
		store *faultyReplayStore
	}{
		{
			name:  "claim lost",
			store: &faultyReplayStore{InboxReplayStore: NewMemoryInboxReplayStore(0), renewLost: true},
		},
		{
			name:  "claim cannot be renewed",
			store: &faultyReplayStore{InboxReplayStore: NewMemoryInboxReplayStore(0), renewErr: errors.New("redis down")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cause error
			var elapsed time.Duration
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start := time.Now()
				select {
				case <-r.Context().Done():
					cause = context.Cause(r.Context())
					elapsed = time.Since(start)
					w.WriteHeader(http.StatusServiceUnavailable)
				case <-time.After(5 * time.Second):
					w.WriteHeader(http.StatusAccepted)
				}
			})
			f := newTimedReplayFixture(t, tt.store, next, timings)

			rec := f.serve(newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, followActivity(sigActorURI)))
			wantStatus(t, rec, http.StatusServiceUnavailable)
			if !errors.Is(cause, errReplayClaimLost) {
				t.Fatalf("cancellation cause = %v, want %v", cause, errReplayClaimLost)
			}
			// Processing stops before the claim can lapse and let a duplicate in.
			if elapsed >= timings.lease {
				t.Fatalf("processing cancelled after %v, want before the %v lease lapses", elapsed, timings.lease)
			}
		})
	}
}

func TestRejectInboxReplays_RetainsUntilSignatureExpires(t *testing.T) {
	store := &faultyReplayStore{InboxReplayStore: NewMemoryInboxReplayStore(0)}
	f := newReplayFixture(t, store, &countingInbox{})

	// A Date-signed request verifies for five minutes plus an hour of clock
	// skew; it is remembered that long plus the clock margin.
	req := newSignedInboxPost(t, f.key, sigKeyID, sigMandatoryHeaders, followActivity(sigActorURI))
	date, err := http.ParseTime(req.Header.Get("Date"))
	if err != nil {
		t.Fatalf("ParseTime() error = %v", err)
	}
	wantStatus(t, f.serve(req), http.StatusAccepted)

	if store.lease != inboxReplayLease {
		t.Fatalf("lease = %v, want %v", store.lease, inboxReplayLease)
	}
	if inboxReplayRenewInterval >= inboxReplayLease/2 {
		t.Fatalf("renew interval %v leaves no room for a failed renewal within the %v lease", inboxReplayRenewInterval, inboxReplayLease)
	}
	want := date.Add(5*time.Minute + time.Hour + inboxReplayClockMargin)
	for name, got := range map[string]time.Time{"claim": store.claimRetain, "complete": store.completeRetain} {
		if !got.Equal(want) {
			t.Fatalf("%s retention = %v, want %v", name, got, want)
		}
	}
}

func TestRejectInboxReplays_KeepsLaterDeadlineOfInFlightDuplicate(t *testing.T) {
	store, clock := newClockedMemoryStore(0)
	clock.now = time.Now()

	started := make(chan struct{})
	release := make(chan struct{})
	inbox := &countingInbox{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inbox.count() == 0 {
			close(started)
			<-release
		}
		inbox.ServeHTTP(w, r)
	})
	f := newReplayFixture(t, store, next)
	body := followActivity(sigActorURI)

	// The first delivery was signed an hour ago, so its signature expires in
	// five minutes. A retry signed now arrives while it is being processed,
	// and a captured copy of that retry verifies for another 65 minutes.
	first := signedPostDated(t, f.key, sigInboxURL, body, time.Now().Add(-time.Hour))
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- f.serve(first) }()
	<-started

	retry := signedPostDated(t, f.key, sigInboxURL, body, time.Now())
	captured := replayOf(retry, body)
	wantStatus(t, f.serve(retry), http.StatusServiceUnavailable)

	close(release)
	wantStatus(t, <-done, http.StatusAccepted)

	// Past the first delivery's retention, the captured retry is still
	// recognized.
	clock.Advance(30 * time.Minute)
	wantStatus(t, f.serve(captured), http.StatusAccepted)
	if inbox.count() != 1 {
		t.Fatalf("deliveries processed = %d, want 1", inbox.count())
	}
}

func TestRejectInboxReplays_RequiresVerifiedRequest(t *testing.T) {
	inbox := &countingInbox{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, sigInboxURL, bytes.NewReader(followActivity(sigActorURI)))
	RejectInboxReplays(NewMemoryInboxReplayStore(0))(inbox).ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusInternalServerError)

	rec = httptest.NewRecorder()
	ctx := context.WithValue(req.Context(), ContextKeySignedActivity, SignedActivity{
		ActorURI:   sigActorURI,
		ValidUntil: time.Now().Add(time.Hour),
	})
	RejectInboxReplays(nil)(inbox).ServeHTTP(rec, req.WithContext(ctx))
	wantStatus(t, rec, http.StatusInternalServerError)

	if inbox.count() != 0 {
		t.Fatalf("deliveries processed = %d, want 0", inbox.count())
	}
}

// testInboxReplayStore checks the InboxReplayStore contract that does not
// depend on the passage of time.
func testInboxReplayStore(t *testing.T, store InboxReplayStore) {
	t.Helper()
	ctx := context.Background()
	const lease, retain = time.Minute, time.Hour

	claim := func(key, token string, want ReplayState) {
		t.Helper()
		got, err := store.Claim(ctx, key, token, lease, time.Now().Add(retain))
		if err != nil {
			t.Fatalf("Claim(%s, %s) error = %v", key, token, err)
		}
		if got != want {
			t.Fatalf("Claim(%s, %s) = %v, want %v", key, token, got, want)
		}
	}
	release := func(key, token string) {
		t.Helper()
		if err := store.Release(ctx, key, token); err != nil {
			t.Fatalf("Release(%s, %s) error = %v", key, token, err)
		}
	}
	renew := func(key, token string, want bool) {
		t.Helper()
		got, err := store.Renew(ctx, key, token, lease)
		if err != nil {
			t.Fatalf("Renew(%s, %s) error = %v", key, token, err)
		}
		if got != want {
			t.Fatalf("Renew(%s, %s) = %v, want %v", key, token, got, want)
		}
	}

	renew("a", "t0", false)
	claim("a", "t1", ReplayClaimed)
	claim("a", "t2", ReplayInFlight)
	claim("a", "t1", ReplayInFlight)

	// Only the claim's own token renews it.
	renew("a", "t1", true)
	renew("a", "t2", false)
	claim("a", "t2", ReplayInFlight)

	// Only the claim's own token releases it.
	release("a", "t2")
	claim("a", "t3", ReplayInFlight)
	release("a", "t1")
	claim("a", "t4", ReplayClaimed)

	if err := store.Complete(ctx, "a", time.Now().Add(retain)); err != nil {
		t.Fatalf("Complete(a) error = %v", err)
	}
	claim("a", "t5", ReplayProcessed)

	// Releasing never forgets a processed delivery, and renewing never turns
	// it back into a claim.
	release("a", "t4")
	renew("a", "t4", false)
	claim("a", "t6", ReplayProcessed)

	claim("b", "t7", ReplayClaimed)
}

func TestMemoryInboxReplayStore(t *testing.T) {
	testInboxReplayStore(t, NewMemoryInboxReplayStore(0))
}

// fakeClock drives a MemoryInboxReplayStore through time.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newClockedMemoryStore(capacity int) (*MemoryInboxReplayStore, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	store := NewMemoryInboxReplayStore(capacity)
	store.now = clock.Now
	return store, clock
}

func TestMemoryInboxReplayStore_Expiry(t *testing.T) {
	ctx := context.Background()
	store, clock := newClockedMemoryStore(0)

	claim := func(key, token string, lease, retain time.Duration, want ReplayState) {
		t.Helper()
		got, err := store.Claim(ctx, key, token, lease, clock.now.Add(retain))
		if err != nil {
			t.Fatalf("Claim(%s) error = %v", key, err)
		}
		if got != want {
			t.Fatalf("Claim(%s, %s) = %v, want %v", key, token, got, want)
		}
	}

	t.Run("abandoned claim expires after its lease", func(t *testing.T) {
		claim("lease", "t1", 5*time.Minute, time.Hour, ReplayClaimed)
		clock.Advance(4 * time.Minute)
		claim("lease", "t2", 5*time.Minute, time.Hour, ReplayInFlight)
		clock.Advance(2 * time.Minute)
		claim("lease", "t3", 5*time.Minute, time.Hour, ReplayClaimed)

		// The abandoned claim's token no longer releases or renews the key.
		if err := store.Release(ctx, "lease", "t1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if held, err := store.Renew(ctx, "lease", "t1", 5*time.Minute); err != nil || held {
			t.Fatalf("Renew(abandoned) = %v, %v, want false", held, err)
		}
		claim("lease", "t4", 5*time.Minute, time.Hour, ReplayInFlight)
	})

	t.Run("renewal extends the lease", func(t *testing.T) {
		claim("renew", "t1", 2*time.Minute, time.Hour, ReplayClaimed)
		for range 5 {
			clock.Advance(90 * time.Second)
			if held, err := store.Renew(ctx, "renew", "t1", 2*time.Minute); err != nil || !held {
				t.Fatalf("Renew() = %v, %v, want held", held, err)
			}
			claim("renew", "t2", 2*time.Minute, time.Hour, ReplayInFlight)
		}

		// A lapsed claim is no longer held and cannot be revived.
		clock.Advance(3 * time.Minute)
		if held, err := store.Renew(ctx, "renew", "t1", 2*time.Minute); err != nil || held {
			t.Fatalf("Renew(lapsed) = %v, %v, want false", held, err)
		}
		claim("renew", "t3", 2*time.Minute, time.Hour, ReplayClaimed)
	})

	t.Run("replays extend retention", func(t *testing.T) {
		claim("retain", "t1", time.Minute, time.Hour, ReplayClaimed)
		if err := store.Complete(ctx, "retain", clock.now.Add(time.Hour)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		clock.Advance(50 * time.Minute)
		claim("retain", "t2", time.Minute, 2*time.Hour, ReplayProcessed)
		// A shorter retention never cuts the entry short.
		claim("retain", "t3", time.Minute, time.Minute, ReplayProcessed)
		clock.Advance(119 * time.Minute)
		claim("retain", "t4", time.Minute, time.Minute, ReplayProcessed)
		clock.Advance(2 * time.Minute)
		claim("retain", "t5", time.Minute, time.Hour, ReplayClaimed)
	})

	complete := func(key string, retain time.Duration) {
		t.Helper()
		if err := store.Complete(ctx, key, clock.now.Add(retain)); err != nil {
			t.Fatalf("Complete(%s) error = %v", key, err)
		}
	}

	t.Run("in-flight duplicates extend retention", func(t *testing.T) {
		claim("inflight", "t1", time.Minute, 10*time.Minute, ReplayClaimed)
		claim("inflight", "t2", time.Minute, 70*time.Minute, ReplayInFlight)
		// The duplicate's deadline does not extend the claim itself.
		clock.Advance(2 * time.Minute)
		claim("inflight", "t3", time.Minute, time.Minute, ReplayClaimed)
		claim("inflight", "t4", time.Minute, 90*time.Minute, ReplayInFlight)
		complete("inflight", 10*time.Minute)
		clock.Advance(89 * time.Minute)
		claim("inflight", "t5", time.Minute, time.Minute, ReplayProcessed)
		clock.Advance(2 * time.Minute)
		claim("inflight", "t6", time.Minute, time.Minute, ReplayClaimed)
	})

	t.Run("lapsed claims keep their deadlines", func(t *testing.T) {
		claim("lapsed", "t1", time.Minute, 70*time.Minute, ReplayClaimed)
		clock.Advance(2 * time.Minute)
		claim("lapsed", "t2", time.Minute, 10*time.Minute, ReplayClaimed)
		complete("lapsed", 10*time.Minute)
		clock.Advance(67 * time.Minute)
		claim("lapsed", "t3", time.Minute, time.Minute, ReplayProcessed)
		clock.Advance(2 * time.Minute)
		claim("lapsed", "t4", time.Minute, time.Minute, ReplayClaimed)
	})

	t.Run("released claims keep their deadlines", func(t *testing.T) {
		claim("released", "t1", time.Minute, 10*time.Minute, ReplayClaimed)
		claim("released", "t2", time.Minute, 70*time.Minute, ReplayInFlight)
		if err := store.Release(ctx, "released", "t1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		// Released deliveries can be processed again at once.
		claim("released", "t3", time.Minute, 10*time.Minute, ReplayClaimed)
		complete("released", 10*time.Minute)
		clock.Advance(69 * time.Minute)
		claim("released", "t4", time.Minute, time.Minute, ReplayProcessed)
		clock.Advance(2 * time.Minute)
		claim("released", "t5", time.Minute, time.Minute, ReplayClaimed)
	})

	t.Run("released claims are forgotten once their deadlines pass", func(t *testing.T) {
		claim("forgotten", "t1", time.Minute, 10*time.Minute, ReplayClaimed)
		if err := store.Release(ctx, "forgotten", "t1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		clock.Advance(11 * time.Minute)
		store.sweepLocked(clock.now, 0)
		if _, ok := store.entries["forgotten"]; ok {
			t.Fatal("released entry kept past its deadlines")
		}
	})

	t.Run("complete never shortens retention", func(t *testing.T) {
		claim("complete", "t1", time.Minute, time.Hour, ReplayClaimed)
		if err := store.Complete(ctx, "complete", clock.now.Add(2*time.Hour)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if err := store.Complete(ctx, "complete", clock.now.Add(time.Minute)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		clock.Advance(time.Hour)
		claim("complete", "t2", time.Minute, time.Minute, ReplayProcessed)
	})
}

func TestMemoryInboxReplayStore_Capacity(t *testing.T) {
	ctx := context.Background()

	t.Run("refuses new claims while full", func(t *testing.T) {
		store, clock := newClockedMemoryStore(2)
		for _, key := range []string{"a", "b"} {
			if state, err := store.Claim(ctx, key, "t-"+key, 5*time.Minute, clock.now.Add(time.Hour)); err != nil || state != ReplayClaimed {
				t.Fatalf("Claim(%s) = %v, %v", key, state, err)
			}
		}
		if err := store.Complete(ctx, "a", clock.now.Add(time.Hour)); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}

		if _, err := store.Claim(ctx, "c", "t-c", 5*time.Minute, clock.now.Add(time.Hour)); !errors.Is(err, ErrReplayStoreFull) {
			t.Fatalf("Claim(c) error = %v, want ErrReplayStoreFull", err)
		}
		// Tracked deliveries are still recognized.
		if state, err := store.Claim(ctx, "a", "t-a2", 5*time.Minute, clock.now.Add(time.Hour)); err != nil || state != ReplayProcessed {
			t.Fatalf("Claim(a) = %v, %v, want processed", state, err)
		}
		if state, err := store.Claim(ctx, "b", "t-b2", 5*time.Minute, clock.now.Add(time.Hour)); err != nil || state != ReplayInFlight {
			t.Fatalf("Claim(b) = %v, %v, want in flight", state, err)
		}

		// A lapsed claim keeps its slot while its deadline may still matter.
		clock.Advance(6 * time.Minute)
		if _, err := store.Claim(ctx, "c", "t-c", 5*time.Minute, clock.now.Add(time.Hour)); !errors.Is(err, ErrReplayStoreFull) {
			t.Fatalf("Claim(c) error = %v, want ErrReplayStoreFull", err)
		}

		// Once the deadlines pass, the slots are reclaimed.
		clock.Advance(time.Hour)
		if state, err := store.Claim(ctx, "c", "t-c", 5*time.Minute, clock.now.Add(time.Hour)); err != nil || state != ReplayClaimed {
			t.Fatalf("Claim(c) = %v, %v, want claimed", state, err)
		}
	})

	t.Run("sweeps a full store at most once a second", func(t *testing.T) {
		store, clock := newClockedMemoryStore(1)
		if _, err := store.Claim(ctx, "a", "t-a", 100*time.Millisecond, clock.now.Add(100*time.Millisecond)); err != nil {
			t.Fatalf("Claim(a) error = %v", err)
		}
		clock.Advance(200 * time.Millisecond)
		if _, err := store.Claim(ctx, "b", "t-b", time.Minute, clock.now.Add(time.Hour)); !errors.Is(err, ErrReplayStoreFull) {
			t.Fatalf("Claim(b) error = %v, want ErrReplayStoreFull", err)
		}
		clock.Advance(time.Second)
		if state, err := store.Claim(ctx, "b", "t-b", time.Minute, clock.now.Add(time.Hour)); err != nil || state != ReplayClaimed {
			t.Fatalf("Claim(b) = %v, %v, want claimed", state, err)
		}
	})
}
