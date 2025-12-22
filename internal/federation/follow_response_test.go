package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

func TestFollowResponseService_AcceptCreatesFollow(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:  1,
		Uri: "https://local.test/users/bob",
	}
	remote := db.Account{
		ID:     2,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote
	store.followRequests[followKey{accountID: local.ID, targetID: remote.ID}] = db.FollowRequest{
		AccountID:       local.ID,
		TargetAccountID: remote.ID,
	}

	service := NewFollowResponseService(store, "local.test")
	accept := &activitypub.Activity{
		Type:  activitypub.TypeAccept,
		Actor: remote.Uri,
		ActivityObject: map[string]any{
			"type":   activitypub.TypeFollow,
			"actor":  local.Uri,
			"object": remote.Uri,
		},
	}

	if err := service.HandleAccept(context.Background(), accept); err != nil {
		t.Fatalf("HandleAccept() error = %v", err)
	}

	key := followKey{accountID: local.ID, targetID: remote.ID}
	if _, ok := store.follows[key]; !ok {
		t.Fatal("expected follow record to be created")
	}
	if _, ok := store.followRequests[key]; ok {
		t.Fatal("expected follow request to be removed")
	}
}

func TestFollowResponseService_AcceptNoRequestNoop(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:  1,
		Uri: "https://local.test/users/bob",
	}
	remote := db.Account{
		ID:     2,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote

	service := NewFollowResponseService(store, "local.test")
	accept := &activitypub.Activity{
		Type:  activitypub.TypeAccept,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          local.Uri,
			ActivityObject: remote.Uri,
		},
	}

	if err := service.HandleAccept(context.Background(), accept); err != nil {
		t.Fatalf("HandleAccept() error = %v", err)
	}
	if len(store.follows) != 0 {
		t.Fatal("did not expect follow record to be created")
	}
}

func TestFollowResponseService_RejectRemovesRequest(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:  1,
		Uri: "https://local.test/users/bob",
	}
	remote := db.Account{
		ID:     2,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote
	store.followRequests[followKey{accountID: local.ID, targetID: remote.ID}] = db.FollowRequest{
		AccountID:       local.ID,
		TargetAccountID: remote.ID,
	}

	service := NewFollowResponseService(store, "local.test")
	reject := &activitypub.Activity{
		Type:  activitypub.TypeReject,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          local.Uri,
			ActivityObject: remote.Uri,
		},
	}

	if err := service.HandleReject(context.Background(), reject); err != nil {
		t.Fatalf("HandleReject() error = %v", err)
	}

	key := followKey{accountID: local.ID, targetID: remote.ID}
	if _, ok := store.followRequests[key]; ok {
		t.Fatal("expected follow request to be removed")
	}
	if len(store.follows) != 0 {
		t.Fatal("did not expect follow record to be created")
	}
}

func TestFollowResponseService_AcceptNonLocalActor(t *testing.T) {
	store := newFakeStore()
	service := NewFollowResponseService(store, "local.test")
	accept := &activitypub.Activity{
		Type:  activitypub.TypeAccept,
		Actor: "https://remote.example/users/alice",
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          "https://remote.example/users/other",
			ActivityObject: "https://remote.example/users/alice",
		},
	}

	err := service.HandleAccept(context.Background(), accept)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusBadRequest)
	}
}

func TestFollowResponseService_AcceptTargetMismatch(t *testing.T) {
	store := newFakeStore()
	local := db.Account{
		ID:  1,
		Uri: "https://local.test/users/bob",
	}
	remote := db.Account{
		ID:     2,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	other := db.Account{
		ID:     3,
		Uri:    "https://remote.example/users/other",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accounts[local.Uri] = local
	store.accounts[remote.Uri] = remote
	store.accounts[other.Uri] = other

	service := NewFollowResponseService(store, "local.test")
	accept := &activitypub.Activity{
		Type:  activitypub.TypeAccept,
		Actor: remote.Uri,
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          local.Uri,
			ActivityObject: other.Uri,
		},
	}

	err := service.HandleAccept(context.Background(), accept)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusBadRequest)
	}
}

func TestFollowResponseService_AcceptMissingAccounts(t *testing.T) {
	store := newFakeStore()
	service := NewFollowResponseService(store, "local.test")
	accept := &activitypub.Activity{
		Type:  activitypub.TypeAccept,
		Actor: "https://remote.example/users/alice",
		ActivityObject: activitypub.Activity{
			Type:           activitypub.TypeFollow,
			Actor:          "https://local.test/users/bob",
			ActivityObject: "https://remote.example/users/alice",
		},
	}

	err := service.HandleAccept(context.Background(), accept)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusNotFound {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusNotFound)
	}
	if inboxErr.Message != "Follow actor not found" {
		t.Fatalf("message = %q, want follow actor not found", inboxErr.Message)
	}
}
