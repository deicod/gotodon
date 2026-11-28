package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

func TestOutgoingService_DeliverAccountDeletion(t *testing.T) {
	suspendedAt := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	alice := suspendedLocalAccount(1, "alice", suspendedAt)
	follower := remoteAccount(2, "remote.example", "bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	personal := remoteAccount(3, "other.example", "carol", "https://other.example/users/carol/inbox", "")

	store := &outgoingStore{followers: map[int64][]db.Account{1: {follower, personal}}}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	known := []string{
		"https://known.example/inbox",
		"https://remote.example/inbox",
		"",
		"https://known.example/inbox",
		"https://third.example/inbox",
	}
	if err := service.DeliverAccountDeletion(context.Background(), alice, known); err != nil {
		t.Fatalf("DeliverAccountDeletion: %v", err)
	}

	gotInboxes := make([]string, 0, len(enqueuer.calls))
	for _, call := range enqueuer.calls {
		gotInboxes = append(gotInboxes, call.inbox)
		if call.localActor.ID != alice.ID {
			t.Fatalf("signed by account %d, want the deleted account %d", call.localActor.ID, alice.ID)
		}
	}
	// Followers first, then every other known inbox, each once.
	wantInboxes := []string{
		"https://other.example/users/carol/inbox",
		"https://remote.example/inbox",
		"https://known.example/inbox",
		"https://third.example/inbox",
	}
	if strings.Join(gotInboxes, ",") != strings.Join(wantInboxes, ",") {
		t.Fatalf("inboxes = %v, want %v", gotInboxes, wantInboxes)
	}

	encoded, err := json.Marshal(enqueuer.calls[0].activity)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"id":     "https://local.test/users/alice#delete",
		"type":   "Delete",
		"actor":  "https://local.test/users/alice",
		"object": "https://local.test/users/alice",
	}
	for key, value := range want {
		if decoded[key] != value {
			t.Errorf("activity[%q] = %v, want %v", key, decoded[key], value)
		}
	}
	to, _ := decoded["to"].([]any)
	if len(to) != 1 || to[0] != activitypub.PublicAudience {
		t.Errorf("to = %v, want public", decoded["to"])
	}

	// A retry sends the same activity.
	enqueuer.calls = nil
	if err := service.DeliverAccountDeletion(context.Background(), alice, known); err != nil {
		t.Fatalf("DeliverAccountDeletion again: %v", err)
	}
	if len(enqueuer.calls) != len(wantInboxes) || enqueuer.calls[0].activity.ID != "https://local.test/users/alice#delete" {
		t.Fatalf("retry enqueued %d activities, want %d with the same id", len(enqueuer.calls), len(wantInboxes))
	}
}

func TestOutgoingService_DeliverAccountDeletionSkipsAndRefuses(t *testing.T) {
	suspendedAt := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	remote := remoteAccount(2, "remote.example", "bob", "https://remote.example/users/bob/inbox", "")
	remote.SuspendedAt = pgtype.Timestamptz{Time: suspendedAt, Valid: true}
	active := suspendedLocalAccount(3, "carol", suspendedAt)
	active.SuspendedAt = pgtype.Timestamptz{}
	noKey := suspendedLocalAccount(4, "dan", suspendedAt)
	noKey.PrivateKey = pgtype.Text{}

	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(&outgoingStore{}, "local.test", enqueuer)
	inboxes := []string{"https://known.example/inbox"}

	if err := service.DeliverAccountDeletion(context.Background(), remote, inboxes); err != nil {
		t.Fatalf("remote account: %v", err)
	}
	if err := service.DeliverAccountDeletion(context.Background(), active, inboxes); !errors.Is(err, errAccountNotSuspended) {
		t.Fatalf("unsuspended account: err = %v, want errAccountNotSuspended", err)
	}
	if err := service.DeliverAccountDeletion(context.Background(), noKey, inboxes); err == nil {
		t.Fatal("account without a signing key: want an error")
	}
	if len(enqueuer.calls) != 0 {
		t.Fatalf("enqueued %d activities, want none", len(enqueuer.calls))
	}
}

func TestOutgoingService_DeliverFollowUndo(t *testing.T) {
	alice := localAccount(1, "alice")
	bob := remoteAccount(2, "remote.example", "bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	sharedOnly := remoteAccount(3, "shared.example", "carol", "", "https://shared.example/inbox")

	tests := []struct {
		name       string
		undo       FollowUndo
		wantInbox  string
		wantID     string
		wantFollow string
	}{
		{
			name:       "follow with a URI, to the personal inbox",
			undo:       FollowUndo{Follower: alice, Target: bob, ID: 7, URI: pgtype.Text{String: "https://local.test/follows/70", Valid: true}},
			wantInbox:  "https://remote.example/users/bob/inbox",
			wantID:     "https://local.test/users/alice#follows/7/undo",
			wantFollow: "https://local.test/follows/70",
		},
		{
			name:       "follow without a URI",
			undo:       FollowUndo{Follower: alice, Target: bob, ID: 8},
			wantInbox:  "https://remote.example/users/bob/inbox",
			wantID:     "https://local.test/users/alice#follows/8/undo",
			wantFollow: "https://local.test/follows/8",
		},
		{
			name:       "target with a shared inbox only",
			undo:       FollowUndo{Follower: alice, Target: sharedOnly, ID: 9},
			wantInbox:  "https://shared.example/inbox",
			wantID:     "https://local.test/users/alice#follows/9/undo",
			wantFollow: "https://local.test/follows/9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enqueuer := &recordingEnqueuer{}
			service := NewOutgoingService(&outgoingStore{}, "local.test", enqueuer)
			if err := service.DeliverFollowUndo(context.Background(), tt.undo); err != nil {
				t.Fatalf("DeliverFollowUndo: %v", err)
			}
			if len(enqueuer.calls) != 1 {
				t.Fatalf("enqueued %d activities, want 1", len(enqueuer.calls))
			}
			call := enqueuer.calls[0]
			if call.inbox != tt.wantInbox {
				t.Fatalf("inbox = %q, want %q", call.inbox, tt.wantInbox)
			}
			if call.localActor.ID != alice.ID {
				t.Fatalf("signed by account %d, want the local follower %d", call.localActor.ID, alice.ID)
			}
			activity := call.activity
			if activity.Type != activitypub.TypeUndo || activity.ID != tt.wantID || activity.Actor != alice.Uri {
				t.Fatalf("activity = %s %q by %v, want Undo %q by %q", activity.Type, activity.ID, activity.Actor, tt.wantID, alice.Uri)
			}
			follow, ok := activity.ActivityObject.(*activitypub.Activity)
			if !ok {
				t.Fatalf("object = %T, want a Follow activity", activity.ActivityObject)
			}
			if follow.Type != activitypub.TypeFollow || follow.ID != tt.wantFollow || follow.Actor != alice.Uri || follow.ActivityObject != tt.undo.Target.Uri {
				t.Fatalf("follow = %+v", follow)
			}
		})
	}
}

func TestOutgoingService_DeliverFollowUndoSkipsAndRefuses(t *testing.T) {
	alice := localAccount(1, "alice")
	dan := localAccount(2, "dan")
	bob := remoteAccount(3, "remote.example", "bob", "https://remote.example/users/bob/inbox", "")
	noInbox := remoteAccount(4, "remote.example", "carol", "", "")

	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(&outgoingStore{}, "local.test", enqueuer)
	ctx := context.Background()

	if err := service.DeliverFollowUndo(ctx, FollowUndo{Follower: alice, Target: dan, ID: 1}); err != nil {
		t.Fatalf("local target: %v", err)
	}
	if err := service.DeliverFollowUndo(ctx, FollowUndo{Follower: bob, Target: noInbox, ID: 2}); err != nil {
		t.Fatalf("remote follower: %v", err)
	}
	if err := service.DeliverFollowUndo(ctx, FollowUndo{Follower: alice, Target: noInbox, ID: 3}); !errors.Is(err, ErrInboxMissing) {
		t.Fatalf("target without inbox: err = %v, want ErrInboxMissing", err)
	}
	if len(enqueuer.calls) != 0 {
		t.Fatalf("enqueued %d activities, want none", len(enqueuer.calls))
	}
}
