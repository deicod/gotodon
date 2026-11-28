package workers

import (
	"context"
	"fmt"
	"testing"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// TestAccountUnsuspensionDeliveredByWorker suspends and then unsuspends a
// local account with the suspension and unsuspension services, and runs
// every task they queue through DeliveryHandler against the database: the
// followers that were sent the suspended actor get the actor back, profile
// included, signed by the account; a repeated unsuspension sends the same
// activity again; and an account suspended again, or whose deletion has
// finished, gets nothing.
func TestAccountUnsuspensionDeliveredByWorker(t *testing.T) {
	conn := openMigratedTestDB(t)
	queries := db.New(conn)
	ctx := context.Background()
	exec := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	local := func(username string) int64 {
		return exec(`INSERT INTO accounts (username, uri, private_key, public_key, display_name, note, discoverable)
			VALUES ($1, $2, 'private-key', 'public-key', $3, 'bio', true) RETURNING id`,
			username, "https://local.test/users/"+username, "Display "+username)
	}
	remote := func(username, inbox, sharedInbox string) int64 {
		return exec(`INSERT INTO accounts (username, domain, uri, inbox_url, shared_inbox_url) VALUES ($1, 'remote.example', $2, $3, $4) RETURNING id`,
			username, "https://remote.example/users/"+username, inbox, sharedInbox)
	}
	follow := func(follower, target int64) {
		exec(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, follower, target)
	}

	alice := local("alice")
	dora := local("dora")
	bob := remote("bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	dave := remote("dave", "https://remote.example/users/dave/inbox", "https://remote.example/inbox")
	erin := remote("erin", "https://remote.example/users/erin/inbox", "")
	for _, follower := range []int64{bob, dave, erin} {
		follow(follower, alice)
		follow(follower, dora)
	}

	enqueuer := &taskEnqueuer{}
	outgoing := federation.NewOutgoingService(queries, "local.test", enqueuer)
	suspension := services.NewAccountSuspensionService(queries, outgoing)
	unsuspension := services.NewAccountUnsuspensionService(queries, outgoing)
	var deliveries []delivered
	handler := &DeliveryHandler{
		store: queries,
		deliver: func(_ context.Context, actor db.Account, inbox string, activity *activitypub.Activity) error {
			deliveries = append(deliveries, delivered{signer: actor.Username, suspended: actor.SuspendedAt.Valid, inbox: inbox, activity: activity})
			return nil
		},
	}
	// run runs fn and then every task it queued through the delivery
	// worker.
	run := func(t *testing.T, fn func() error) []delivered {
		t.Helper()
		enqueuer.tasks, deliveries = nil, nil
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		for _, task := range enqueuer.tasks {
			if err := handler.ProcessTask(ctx, task); err != nil {
				t.Fatalf("ProcessTask: %v", err)
			}
		}
		if len(deliveries) != len(enqueuer.tasks) {
			t.Fatalf("worker delivered %d of %d tasks", len(deliveries), len(enqueuer.tasks))
		}
		return deliveries
	}
	suspend := func(t *testing.T, accountID int64) []delivered {
		t.Helper()
		if err := queries.AdminSuspendAccount(ctx, accountID); err != nil {
			t.Fatalf("AdminSuspendAccount: %v", err)
		}
		return run(t, func() error { return suspension.Suspend(ctx, accountID) })
	}
	unsuspend := func(t *testing.T, accountID int64) []delivered {
		t.Helper()
		return run(t, func() error { return unsuspension.Unsuspend(ctx, accountID) })
	}
	lift := func(t *testing.T, accountID int64, want int64) {
		t.Helper()
		n, err := queries.AdminUnsuspendAccount(ctx, accountID)
		if err != nil {
			t.Fatalf("AdminUnsuspendAccount: %v", err)
		}
		if n != want {
			t.Fatalf("AdminUnsuspendAccount lifted %d suspensions, want %d", n, want)
		}
	}
	wantInboxes := "[https://remote.example/inbox https://remote.example/users/erin/inbox]"

	t.Run("local account", func(t *testing.T) {
		var suspensionIDs []string
		for _, d := range suspend(t, alice) {
			suspensionIDs = append(suspensionIDs, d.activity.ID)
		}
		lift(t, alice, 1)

		var firstIDs []string
		for attempt := 1; attempt <= 2; attempt++ {
			var inboxes, ids []string
			for _, d := range unsuspend(t, alice) {
				actor, _ := d.activity.ActivityObject.(map[string]any)
				if d.activity.Type != activitypub.TypeUpdate || d.signer != "alice" || d.suspended {
					t.Fatalf("attempt %d: delivery = %s by %s (suspended %v)", attempt, d.activity.Type, d.signer, d.suspended)
				}
				if actor["id"] != "https://local.test/users/alice" || actor["name"] != "Display alice" ||
					actor["summary"] != "bio" || actor["discoverable"] != true {
					t.Fatalf("attempt %d: actor = %v, want alice's profile", attempt, actor)
				}
				if _, ok := actor["suspended"]; ok {
					t.Fatalf("attempt %d: actor is marked suspended: %v", attempt, actor)
				}
				inboxes = append(inboxes, d.inbox)
				ids = append(ids, d.activity.ID)
			}
			if fmt.Sprint(inboxes) != wantInboxes {
				t.Fatalf("attempt %d: inboxes = %v, want %s", attempt, inboxes, wantInboxes)
			}
			if fmt.Sprint(ids) == fmt.Sprint(suspensionIDs) {
				t.Fatalf("attempt %d: unsuspension reuses the suspension's activity ids %v", attempt, ids)
			}
			if attempt == 1 {
				firstIDs = ids
			} else if fmt.Sprint(ids) != fmt.Sprint(firstIDs) {
				t.Fatalf("retry sent activities %v, want the same %v", ids, firstIDs)
			}
		}

		// Unsuspending again lifts nothing.
		lift(t, alice, 0)
	})

	t.Run("account suspended again", func(t *testing.T) {
		suspend(t, alice)
		if got := unsuspend(t, alice); len(got) != 0 {
			t.Fatalf("delivered %d activities for a suspended account", len(got))
		}
	})

	t.Run("account whose deletion finished", func(t *testing.T) {
		suspend(t, dora)
		if err := queries.DeleteAccountDeletionRequest(ctx, dora); err != nil {
			t.Fatalf("finish the deletion of dora: %v", err)
		}
		lift(t, dora, 0)
		if got := unsuspend(t, dora); len(got) != 0 {
			t.Fatalf("delivered %d activities for a deleted account", len(got))
		}
	})
}
