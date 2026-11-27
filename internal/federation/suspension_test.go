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

func suspendedLocalAccount(id int64, username string, updatedAt time.Time) db.Account {
	account := localAccount(id, username)
	account.DisplayName = "Display " + username
	account.Note = "<p>bio</p>"
	account.Locked = true
	account.Discoverable = pgtype.Bool{Bool: true, Valid: true}
	account.Indexable = true
	account.PublicKey = "public-key-pem"
	account.CreatedAt = pgtype.Timestamptz{Time: time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC), Valid: true}
	account.UpdatedAt = pgtype.Timestamptz{Time: updatedAt, Valid: true}
	account.SuspendedAt = pgtype.Timestamptz{Time: updatedAt, Valid: true}
	return account
}

func TestOutgoingService_DeliverAccountSuspension(t *testing.T) {
	updatedAt := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	alice := suspendedLocalAccount(1, "alice", updatedAt)
	shared := remoteAccount(2, "remote.example", "bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	sameShared := remoteAccount(3, "remote.example", "carol", "https://remote.example/users/carol/inbox", "https://remote.example/inbox")
	personal := remoteAccount(4, "other.example", "dan", "https://other.example/users/dan/inbox", "")
	localFollower := localAccount(5, "erin")

	store := &outgoingStore{
		accounts:  map[int64]db.Account{1: alice},
		followers: map[int64][]db.Account{1: {shared, sameShared, personal, localFollower}},
	}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	if err := service.DeliverAccountSuspension(context.Background(), alice); err != nil {
		t.Fatalf("DeliverAccountSuspension: %v", err)
	}

	gotInboxes := make([]string, 0, len(enqueuer.calls))
	for _, call := range enqueuer.calls {
		gotInboxes = append(gotInboxes, call.inbox)
		if call.localActor.ID != alice.ID {
			t.Fatalf("signed by account %d, want the suspended account %d", call.localActor.ID, alice.ID)
		}
	}
	wantInboxes := []string{"https://other.example/users/dan/inbox", "https://remote.example/inbox"}
	if strings.Join(gotInboxes, ",") != strings.Join(wantInboxes, ",") {
		t.Fatalf("inboxes = %v, want %v", gotInboxes, wantInboxes)
	}

	activity := enqueuer.calls[0].activity
	if activity.Type != activitypub.TypeUpdate {
		t.Fatalf("type = %q, want Update", activity.Type)
	}
	wantID := "https://local.test/users/alice#updates/" + "1795168800"
	if activity.ID != wantID {
		t.Fatalf("id = %q, want %q", activity.ID, wantID)
	}
	if activity.Actor != alice.Uri {
		t.Fatalf("actor = %v, want %q", activity.Actor, alice.Uri)
	}
	if activity.Published != "2026-11-20T10:00:00Z" {
		t.Fatalf("published = %q", activity.Published)
	}
	if len(activity.To) != 1 || activity.To[0] != activitypub.PublicAudience {
		t.Fatalf("to = %v, want public", activity.To)
	}

	encoded, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Object map[string]any `json:"object"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	object := decoded.Object
	want := map[string]any{
		"id":                "https://local.test/users/alice",
		"type":              "Person",
		"name":              "alice",
		"preferredUsername": "alice",
		"url":               "https://local.test/@alice",
		"inbox":             "https://local.test/users/alice/inbox",
		"outbox":            "https://local.test/users/alice/outbox",
		"followers":         "https://local.test/users/alice/followers",
		"following":         "https://local.test/users/alice/following",
		"featured":          "https://local.test/users/alice/collections/featured",
		"featuredTags":      "https://local.test/users/alice/collections/tags",
		"published":         "2024-05-06T07:08:09Z",
		"discoverable":      false,
		"indexable":         false,
		"suspended":         true,
	}
	for key, value := range want {
		if object[key] != value {
			t.Errorf("object[%q] = %v, want %v", key, object[key], value)
		}
	}
	for _, key := range []string{"summary", "content", "icon", "image", "manuallyApprovesFollowers", "@context"} {
		if _, ok := object[key]; ok {
			t.Errorf("object has %q, which a suspended actor leaves out", key)
		}
	}
	publicKey, _ := object["publicKey"].(map[string]any)
	if publicKey["id"] != "https://local.test/users/alice#main-key" || publicKey["owner"] != alice.Uri || publicKey["publicKeyPem"] != "public-key-pem" {
		t.Errorf("publicKey = %v", object["publicKey"])
	}
	endpoints, _ := object["endpoints"].(map[string]any)
	if endpoints["sharedInbox"] != "https://local.test/inbox" {
		t.Errorf("endpoints = %v", object["endpoints"])
	}

	// A retry sends the same activity.
	first := activity.ID
	enqueuer.calls = nil
	if err := service.DeliverAccountSuspension(context.Background(), alice); err != nil {
		t.Fatalf("DeliverAccountSuspension again: %v", err)
	}
	if len(enqueuer.calls) != 2 || enqueuer.calls[0].activity.ID != first {
		t.Fatalf("retry delivered %d activities, first id %v; want 2 with id %q", len(enqueuer.calls), enqueuer.calls, first)
	}
}

func TestOutgoingService_DeliverAccountSuspensionSkipsAndRefuses(t *testing.T) {
	updatedAt := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	remote := remoteAccount(2, "remote.example", "bob", "https://remote.example/users/bob/inbox", "")
	remote.SuspendedAt = pgtype.Timestamptz{Time: updatedAt, Valid: true}
	active := suspendedLocalAccount(3, "carol", updatedAt)
	active.SuspendedAt = pgtype.Timestamptz{}
	noKey := suspendedLocalAccount(4, "dan", updatedAt)
	noKey.PrivateKey = pgtype.Text{}
	follower := remoteAccount(5, "remote.example", "erin", "https://remote.example/users/erin/inbox", "")

	store := &outgoingStore{followers: map[int64][]db.Account{2: {follower}, 3: {follower}, 4: {follower}}}
	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(store, "local.test", enqueuer)

	if err := service.DeliverAccountSuspension(context.Background(), remote); err != nil {
		t.Fatalf("remote account: %v", err)
	}
	if err := service.DeliverAccountSuspension(context.Background(), active); !errors.Is(err, errAccountNotSuspended) {
		t.Fatalf("unsuspended account: err = %v, want errAccountNotSuspended", err)
	}
	if err := service.DeliverAccountSuspension(context.Background(), noKey); err == nil {
		t.Fatal("account without a signing key: want an error")
	}
	if len(enqueuer.calls) != 0 {
		t.Fatalf("enqueued %d activities, want none", len(enqueuer.calls))
	}
}

func TestOutgoingService_DeliverFollowReject(t *testing.T) {
	alice := localAccount(1, "alice")
	bob := remoteAccount(2, "remote.example", "bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	sharedOnly := remoteAccount(3, "shared.example", "carol", "", "https://shared.example/inbox")

	tests := []struct {
		name       string
		rejection  FollowRejection
		wantInbox  string
		wantID     string
		wantFollow string
	}{
		{
			name:       "follow, to the personal inbox",
			rejection:  FollowRejection{Follower: bob, Target: alice, ID: 7, URI: pgtype.Text{String: "https://remote.example/follows/1", Valid: true}},
			wantInbox:  "https://remote.example/users/bob/inbox",
			wantID:     "https://local.test/users/alice#rejects/follows/7",
			wantFollow: "https://remote.example/follows/1",
		},
		{
			name:       "follow request without a URI",
			rejection:  FollowRejection{Follower: bob, Target: alice, ID: 8, Request: true},
			wantInbox:  "https://remote.example/users/bob/inbox",
			wantID:     "https://local.test/users/alice#rejects/follow_requests/8",
			wantFollow: "https://local.test/follows/8",
		},
		{
			name:       "follower with a shared inbox only",
			rejection:  FollowRejection{Follower: sharedOnly, Target: alice, ID: 9, URI: pgtype.Text{String: "https://shared.example/follows/9", Valid: true}},
			wantInbox:  "https://shared.example/inbox",
			wantID:     "https://local.test/users/alice#rejects/follows/9",
			wantFollow: "https://shared.example/follows/9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enqueuer := &recordingEnqueuer{}
			service := NewOutgoingService(&outgoingStore{}, "local.test", enqueuer)
			if err := service.DeliverFollowReject(context.Background(), tt.rejection); err != nil {
				t.Fatalf("DeliverFollowReject: %v", err)
			}
			if len(enqueuer.calls) != 1 {
				t.Fatalf("enqueued %d activities, want 1", len(enqueuer.calls))
			}
			call := enqueuer.calls[0]
			if call.inbox != tt.wantInbox {
				t.Fatalf("inbox = %q, want %q", call.inbox, tt.wantInbox)
			}
			if call.localActor.ID != alice.ID {
				t.Fatalf("signed by account %d, want the local target %d", call.localActor.ID, alice.ID)
			}
			activity := call.activity
			if activity.Type != activitypub.TypeReject || activity.ID != tt.wantID || activity.Actor != alice.Uri {
				t.Fatalf("activity = %s %q by %v, want Reject %q by %q", activity.Type, activity.ID, activity.Actor, tt.wantID, alice.Uri)
			}
			follow, ok := activity.ActivityObject.(*activitypub.Activity)
			if !ok {
				t.Fatalf("object = %T, want a Follow activity", activity.ActivityObject)
			}
			if follow.Type != activitypub.TypeFollow || follow.ID != tt.wantFollow || follow.Actor != tt.rejection.Follower.Uri || follow.ActivityObject != alice.Uri {
				t.Fatalf("follow = %+v", follow)
			}
		})
	}
}

func TestOutgoingService_DeliverFollowRejectSkipsAndRefuses(t *testing.T) {
	alice := localAccount(1, "alice")
	dan := localAccount(2, "dan")
	bob := remoteAccount(3, "remote.example", "bob", "https://remote.example/users/bob/inbox", "")
	noInbox := remoteAccount(4, "remote.example", "carol", "", "")

	enqueuer := &recordingEnqueuer{}
	service := NewOutgoingService(&outgoingStore{}, "local.test", enqueuer)
	ctx := context.Background()

	if err := service.DeliverFollowReject(ctx, FollowRejection{Follower: dan, Target: alice, ID: 1}); err != nil {
		t.Fatalf("local follower: %v", err)
	}
	if err := service.DeliverFollowReject(ctx, FollowRejection{Follower: bob, Target: noInbox, ID: 2}); err != nil {
		t.Fatalf("remote target: %v", err)
	}
	if err := service.DeliverFollowReject(ctx, FollowRejection{Follower: noInbox, Target: alice, ID: 3}); !errors.Is(err, ErrInboxMissing) {
		t.Fatalf("follower without inbox: err = %v, want ErrInboxMissing", err)
	}
	if len(enqueuer.calls) != 0 {
		t.Fatalf("enqueued %d activities, want none", len(enqueuer.calls))
	}
}
