package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/pkg/activitypub"
)

var errDeliveryQueueDown = errors.New("delivery queue down")

// recordingDeliveryQueue stands in for the delivery queue of the outgoing
// federation service. It records each delivery it is handed, as "<activity>
// by <signer> to <inbox>", and fails the next fail of them, as a queue that
// is down does.
type recordingDeliveryQueue struct {
	mu         sync.Mutex
	fail       int
	deliveries []string
}

func (q *recordingDeliveryQueue) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.fail > 0 {
		q.fail--
		return errDeliveryQueueDown
	}
	// Read the activity as its recipient does.
	raw, err := json.Marshal(activity)
	if err != nil {
		return err
	}
	var wire struct {
		Type   string          `json:"type"`
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	kind := wire.Type
	if kind == activitypub.TypeUpdate {
		var actor struct {
			Suspended bool `json:"suspended"`
		}
		if err := json.Unmarshal(wire.Object, &actor); err != nil {
			return err
		}
		kind = "Update of the actor"
		if actor.Suspended {
			kind = "Update of the suspended actor"
		}
	}
	q.deliveries = append(q.deliveries, fmt.Sprintf("%s by %s to %s", kind, localActor.Username, inbox))
	return nil
}

// take returns the deliveries recorded since the last take, sorted, and the
// injected failures that are left.
func (q *recordingDeliveryQueue) take() ([]string, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	got := q.deliveries
	q.deliveries = nil
	slices.Sort(got)
	return got, q.fail
}

func (q *recordingDeliveryQueue) failNext(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fail = n
}

// lifecycleWorld is a database with alice, the local account whose
// lifecycle the test runs, and the accounts around her:
//
//   - olive, the Owner, and mo, a Moderator, who act on her;
//   - carol, a local user without a role, who follows alice and bob;
//   - bob (remote.example, behind a shared inbox) and erin (other.example,
//     without one), remote accounts that follow alice.
//
// alice and bob each have a public status, alice's trending.
//
// The admin routes run as `gotodon serve` wires them without workers: with
// the roles of the database, and with the in-process runners of the
// suspension, unsuspension and deletion services, whose deliveries an
// outgoing federation service hands to queue.
type lifecycleWorld struct {
	pool  *pgxpool.Pool
	q     *db.Queries
	mux   *http.ServeMux
	queue *recordingDeliveryQueue
	logs  bytes.Buffer

	suspensions   *services.InProcessAccountSuspension
	unsuspensions *services.InProcessAccountUnsuspension
	deletions     *services.InProcessAccountDeletion

	owner, moderator, carol, alice, bob, erin int64
	names                                     map[int64]string
}

func newLifecycleWorld(t *testing.T) *lifecycleWorld {
	pool := openMigratedPool(t)
	ctx := context.Background()
	w := &lifecycleWorld{pool: pool, q: db.New(pool), queue: &recordingDeliveryQueue{}, names: map[int64]string{}}

	id := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	local := func(username, role string) int64 {
		account := id(`INSERT INTO accounts (username, uri, private_key, public_key, display_name)
			VALUES ($1::text, 'https://local.test/users/' || $1::text, 'private-key', 'public-key', $1::text) RETURNING id`, username)
		id(`INSERT INTO users (email, account_id, confirmed_at, approved, role_id)
			VALUES ($1::text || '@local.test', $2, now(), true, (SELECT id FROM user_roles WHERE name = NULLIF($3::text, ''))) RETURNING id`,
			username, account, role)
		w.names[account] = username
		return account
	}
	remote := func(domain, username, sharedInbox string) int64 {
		account := id(`INSERT INTO accounts (username, domain, uri, inbox_url, shared_inbox_url)
			VALUES ($1::text, $2::text, 'https://' || $2::text || '/users/' || $1::text, 'https://' || $2::text || '/users/' || $1::text || '/inbox', $3)
			RETURNING id`, username, domain, sharedInbox)
		w.names[account] = username
		return account
	}
	status := func(author int64, local bool) int64 {
		status := id(`INSERT INTO statuses (account_id, text, visibility, local) VALUES ($1, 'hello', 0, $2) RETURNING id`, author, local)
		id(`INSERT INTO status_stats (status_id) VALUES ($1) RETURNING id`, status)
		return status
	}
	follow := func(follower, target int64, uri string) {
		id(`INSERT INTO follows (account_id, target_account_id, uri) VALUES ($1, $2, NULLIF($3, '')) RETURNING id`, follower, target, uri)
	}

	w.owner = local("olive", "Owner")
	w.moderator = local("mo", "Moderator")
	w.carol = local("carol", "")
	w.alice = local("alice", "")
	w.bob = remote("remote.example", "bob", "https://remote.example/inbox")
	w.erin = remote("other.example", "erin", "")
	trending := status(w.alice, true)
	id(`INSERT INTO status_trends (status_id, account_id, allowed) VALUES ($1, $2, true) RETURNING id`, trending, w.alice)
	status(w.bob, false)
	follow(w.bob, w.alice, "https://remote.example/follows/1")
	follow(w.erin, w.alice, "")
	follow(w.carol, w.alice, "")
	follow(w.carol, w.bob, "")

	logger := slog.New(slog.NewTextHandler(&w.logs, nil))
	outgoing := federation.NewOutgoingService(w.q, "local.test", w.queue)
	storage := media.NewLocalStorage(t.TempDir(), "https://local.test/files")
	w.suspensions = &services.InProcessAccountSuspension{
		Suspender:   services.NewAccountSuspensionService(w.q, outgoing),
		BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Logger: logger,
	}
	w.unsuspensions = &services.InProcessAccountUnsuspension{
		Unsuspender: services.NewAccountUnsuspensionService(w.q, outgoing),
		BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Logger: logger,
	}
	w.deletions = &services.InProcessAccountDeletion{
		Deleter: services.NewAccountDeletionService(w.q, services.DBTransactor{DB: pool, Queries: w.q}, outgoing, storage, nil, logger),
		Logger:  logger,
	}

	handler := NewAccountsHandler(w.q, pool, &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test"}})
	handler.SetAccountSuspensionScheduler(w.suspensions)
	handler.SetAccountUnsuspensionScheduler(w.unsuspensions)
	handler.SetAccountDeletionScheduler(w.deletions)
	w.mux = http.NewServeMux()
	handler.RegisterRoutes(w.mux)
	return w
}

// serve sends a request as the account as, with a token holding the admin
// scopes, and waits for the side effects it started in the background.
func (w *lifecycleWorld) serve(as int64, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req = withScopes(withIdentity(req, &as, nil), adminToken)
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	w.suspensions.Wait()
	w.unsuspensions.Wait()
	w.deletions.Wait()
	return rec
}

// lifecycleRows is what the lifecycle changes in the rows of alice and bob.
type lifecycleRows struct {
	aliceSuspended, aliceDeletionRequested, aliceUserDisabled bool
	// aliceFollows counts her follows and her followers.
	aliceStatuses, aliceFollows, aliceTrends int64
	bobSuspended                             bool
	// bobFollows counts his follows, of alice.
	bobFollows int64
}

func (w *lifecycleWorld) rows(t *testing.T) lifecycleRows {
	t.Helper()
	var r lifecycleRows
	if err := w.pool.QueryRow(context.Background(), `
		SELECT
			(SELECT suspended_at IS NOT NULL FROM accounts WHERE id = $1),
			EXISTS (SELECT 1 FROM account_deletion_requests WHERE account_id = $1),
			(SELECT disabled FROM users WHERE account_id = $1),
			(SELECT count(*) FROM statuses WHERE account_id = $1),
			(SELECT count(*) FROM follows WHERE account_id = $1 OR target_account_id = $1),
			(SELECT count(*) FROM status_trends WHERE account_id = $1),
			(SELECT suspended_at IS NOT NULL FROM accounts WHERE id = $2),
			(SELECT count(*) FROM follows WHERE account_id = $2)`,
		w.alice, w.bob,
	).Scan(&r.aliceSuspended, &r.aliceDeletionRequested, &r.aliceUserDisabled, &r.aliceStatuses, &r.aliceFollows, &r.aliceTrends,
		&r.bobSuspended, &r.bobFollows); err != nil {
		t.Fatalf("read rows: %v", err)
	}
	return r
}

// visible lists, sorted, where the queries that serve timelines and actors
// find alice and bob.
func (w *lifecycleWorld) visible(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	var found []string
	public, err := w.q.GetPublicTimeline(ctx, db.GetPublicTimelineParams{Limit: 20})
	if err != nil {
		t.Fatalf("GetPublicTimeline: %v", err)
	}
	for _, status := range public {
		found = append(found, w.names[status.AccountID]+" in the public timeline")
	}
	home, err := w.q.GetHomeTimeline(ctx, db.GetHomeTimelineParams{AccountID: w.carol, Limit: 20})
	if err != nil {
		t.Fatalf("GetHomeTimeline: %v", err)
	}
	for _, status := range home {
		found = append(found, w.names[status.AccountID]+" in carol's home timeline")
	}
	switch _, err := w.q.GetAccountByUsername(ctx, "alice"); {
	case err == nil:
		found = append(found, "alice's actor")
	case !errors.Is(err, pgx.ErrNoRows):
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	slices.Sort(found)
	return found
}

// TestAccountLifecycleAcceptance runs the lifecycle of alice, and of bob, a
// remote account, through the admin routes against the database: each step
// suspends, unsuspends or deletes an account, or is refused, and checks the
// answer, the deliveries the step queued, the rows it changed and where the
// timelines and actor lookups find the accounts afterwards. The steps build
// on each other, so the test stops at the first one that fails; its name
// gives the lifecycle action and the case.
//
// The tests of each layer check its details: the services in
// internal/services, the queries in internal/db, the worker tasks in
// internal/workers and the routes against stubs here. This one checks that
// they add up to Mastodon's lifecycle.
func TestAccountLifecycleAcceptance(t *testing.T) {
	w := newLifecycleWorld(t)
	action := func(id int64) string { return fmt.Sprintf("/api/v1/admin/accounts/%d/action", id) }
	unsuspend := func(id int64) string { return fmt.Sprintf("/api/v1/admin/accounts/%d/unsuspend", id) }
	account := func(id int64) string { return fmt.Sprintf("/api/v1/admin/accounts/%d", id) }
	const suspend = `{"type":"suspend"}`

	var (
		everywhere = []string{"alice in carol's home timeline", "alice in the public timeline", "alice's actor",
			"bob in carol's home timeline", "bob in the public timeline"}
		bobOnly = []string{"bob in carol's home timeline", "bob in the public timeline"}

		active          = lifecycleRows{aliceStatuses: 1, aliceFollows: 3, aliceTrends: 1, bobFollows: 1}
		suspended       = lifecycleRows{aliceSuspended: true, aliceDeletionRequested: true, aliceStatuses: 1, aliceFollows: 3, bobFollows: 1}
		unsuspended     = lifecycleRows{aliceStatuses: 1, aliceFollows: 3, bobFollows: 1}
		bobSuspended    = lifecycleRows{aliceStatuses: 1, aliceFollows: 2, bobSuspended: true}
		bobUnsuspended  = lifecycleRows{aliceStatuses: 1, aliceFollows: 2}
		suspendedAgain  = lifecycleRows{aliceSuspended: true, aliceDeletionRequested: true, aliceStatuses: 1, aliceFollows: 2}
		deletionStopped = lifecycleRows{aliceSuspended: true, aliceDeletionRequested: true, aliceUserDisabled: true, aliceStatuses: 1, aliceFollows: 2}
		deleted         = lifecycleRows{aliceSuspended: true, aliceUserDisabled: true}
	)

	steps := []struct {
		name               string
		as                 int64
		method, path, body string
		// failDeliveries is how many deliveries fail before the step's
		// side effects get through.
		failDeliveries int

		wantStatus     int
		wantDeliveries []string
		wantRows       lifecycleRows
		wantVisible    []string
	}{
		{
			name: "suspend: a user without a role is refused", as: w.carol,
			method: http.MethodPost, path: action(w.alice), body: suspend,
			wantStatus: http.StatusForbidden, wantRows: active, wantVisible: everywhere,
		},
		{
			// The first attempt meets a queue that is down for both
			// deliveries; the runner tries again.
			name: "suspend: local account, retried after the delivery queue fails", as: w.owner,
			method: http.MethodPost, path: action(w.alice), body: suspend, failDeliveries: 2,
			wantStatus: http.StatusOK,
			wantDeliveries: []string{
				"Update of the suspended actor by alice to https://other.example/users/erin/inbox",
				"Update of the suspended actor by alice to https://remote.example/inbox",
			},
			wantRows: suspended, wantVisible: bobOnly,
		},
		{
			name: "suspend: repeated request federates nothing more", as: w.owner,
			method: http.MethodPost, path: action(w.alice), body: suspend,
			wantStatus: http.StatusOK, wantRows: suspended, wantVisible: bobOnly,
		},
		{
			name: "unsuspend: local account, retried after the delivery queue fails", as: w.moderator,
			method: http.MethodPost, path: unsuspend(w.alice), failDeliveries: 2,
			wantStatus: http.StatusOK,
			wantDeliveries: []string{
				"Update of the actor by alice to https://other.example/users/erin/inbox",
				"Update of the actor by alice to https://remote.example/inbox",
			},
			// The status trends that the suspension dropped stay dropped.
			wantRows: unsuspended, wantVisible: everywhere,
		},
		{
			name: "unsuspend: repeated request is refused", as: w.owner,
			method: http.MethodPost, path: unsuspend(w.alice),
			wantStatus: http.StatusForbidden, wantRows: unsuspended, wantVisible: everywhere,
		},
		{
			name: "suspend: remote account has its follows of local accounts rejected", as: w.owner,
			method: http.MethodPost, path: action(w.bob), body: suspend,
			wantStatus:     http.StatusOK,
			wantDeliveries: []string{"Reject by alice to https://remote.example/users/bob/inbox"},
			wantRows:       bobSuspended, wantVisible: []string{"alice in carol's home timeline", "alice in the public timeline", "alice's actor"},
		},
		{
			name: "unsuspend: remote account federates nothing and its rejected follow stays gone", as: w.owner,
			method: http.MethodPost, path: unsuspend(w.bob),
			wantStatus: http.StatusOK, wantRows: bobUnsuspended, wantVisible: everywhere,
		},
		{
			name: "delete: account that isn't suspended is refused", as: w.owner,
			method: http.MethodDelete, path: account(w.alice),
			wantStatus: http.StatusForbidden, wantRows: bobUnsuspended, wantVisible: everywhere,
		},
		{
			name: "suspend: local account again reaches its remaining follower", as: w.owner,
			method: http.MethodPost, path: action(w.alice), body: suspend,
			wantStatus:     http.StatusOK,
			wantDeliveries: []string{"Update of the suspended actor by alice to https://other.example/users/erin/inbox"},
			wantRows:       suspendedAgain, wantVisible: bobOnly,
		},
		{
			name: "delete: a Moderator without delete_user_data is refused", as: w.moderator,
			method: http.MethodDelete, path: account(w.alice),
			wantStatus: http.StatusForbidden, wantRows: suspendedAgain, wantVisible: bobOnly,
		},
		{
			// The actor Delete goes to her follower and every known
			// inbox. While the queue is down it can't, so the deletion
			// stops before it deletes anything and keeps its request.
			name: "delete: delivery queue down keeps the deletion request", as: w.owner,
			method: http.MethodDelete, path: account(w.alice), failDeliveries: 2,
			wantStatus: http.StatusOK, wantRows: deletionStopped, wantVisible: bobOnly,
		},
		{
			name: "delete: requested again, finishes", as: w.owner,
			method: http.MethodDelete, path: account(w.alice),
			wantStatus: http.StatusOK,
			wantDeliveries: []string{
				"Delete by alice to https://other.example/users/erin/inbox",
				"Delete by alice to https://remote.example/inbox",
			},
			wantRows: deleted, wantVisible: bobOnly,
		},
		{
			name: "delete: repeated request is refused", as: w.owner,
			method: http.MethodDelete, path: account(w.alice),
			wantStatus: http.StatusForbidden, wantRows: deleted, wantVisible: bobOnly,
		},
		{
			name: "unsuspend: a deleted account stays deleted", as: w.owner,
			method: http.MethodPost, path: unsuspend(w.alice),
			wantStatus: http.StatusForbidden, wantRows: deleted, wantVisible: bobOnly,
		},
	}

	for _, step := range steps {
		passed := t.Run(step.name, func(t *testing.T) {
			w.logs.Reset()
			w.queue.failNext(step.failDeliveries)
			rec := w.serve(step.as, step.method, step.path, step.body)

			if rec.Code != step.wantStatus {
				t.Errorf("status = %d (%s), want %d", rec.Code, strings.TrimSpace(rec.Body.String()), step.wantStatus)
			}
			deliveries, unmet := w.queue.take()
			if !slices.Equal(deliveries, step.wantDeliveries) {
				t.Errorf("deliveries queued = %q, want %q", deliveries, step.wantDeliveries)
			}
			if unmet != 0 {
				t.Errorf("%d of %d injected delivery failures never happened", unmet, step.failDeliveries)
			}
			if got := w.rows(t); got != step.wantRows {
				t.Errorf("rows = %+v, want %+v", got, step.wantRows)
			}
			if got := w.visible(t); !slices.Equal(got, step.wantVisible) {
				t.Errorf("found %q, want %q", got, step.wantVisible)
			}
			if t.Failed() && w.logs.Len() > 0 {
				t.Logf("server log:\n%s", w.logs.String())
			}
		})
		if !passed {
			t.Fatalf("stopped after %q: the steps after it build on it", step.name)
		}
	}
}
