package federation

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type followerStore struct {
	followers []db.Account
}

func (s *followerStore) GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error) {
	start := int(arg.Offset)
	if start >= len(s.followers) {
		return []db.Account{}, nil
	}
	end := start + int(arg.Limit)
	if end > len(s.followers) {
		end = len(s.followers)
	}
	return s.followers[start:end], nil
}

func TestInboxResolver_UsesSharedInboxAndBatches(t *testing.T) {
	store := &followerStore{
		followers: []db.Account{
			{ID: 1, Username: "local"}, // local, skipped
			{
				ID:             2,
				Username:       "alice",
				Domain:         pgtype.Text{String: "remote.test", Valid: true},
				InboxUrl:       "https://remote.test/users/alice/inbox",
				SharedInboxUrl: "https://remote.test/inbox",
			},
			{
				ID:             3,
				Username:       "bob",
				Domain:         pgtype.Text{String: "remote.test", Valid: true},
				InboxUrl:       "https://remote.test/users/bob/inbox",
				SharedInboxUrl: "https://remote.test/inbox",
			},
			{
				ID:       4,
				Username: "carol",
				Domain:   pgtype.Text{String: "other.test", Valid: true},
				InboxUrl: "https://other.test/users/carol/inbox",
			},
		},
	}

	resolver := NewInboxResolver(store, "local.test")
	resolver.batchSize = 2

	targets, err := resolver.ResolveFollowerInboxes(context.Background(), 10)
	if err != nil {
		t.Fatalf("ResolveFollowerInboxes error: %v", err)
	}

	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}

	got := make(map[string][]int64)
	for _, target := range targets {
		got[target.Inbox] = target.AccountIDs
	}

	wantShared := []int64{2, 3}
	if !reflect.DeepEqual(got["https://remote.test/inbox"], wantShared) {
		t.Fatalf("shared inbox accounts = %v, want %v", got["https://remote.test/inbox"], wantShared)
	}

	wantPersonal := []int64{4}
	if !reflect.DeepEqual(got["https://other.test/users/carol/inbox"], wantPersonal) {
		t.Fatalf("personal inbox accounts = %v, want %v", got["https://other.test/users/carol/inbox"], wantPersonal)
	}
}

func TestInboxResolver_SkipsMissingOrLocal(t *testing.T) {
	store := &followerStore{
		followers: []db.Account{
			{
				ID:       1,
				Username: "local",
			},
			{
				ID:       2,
				Username: "remote-missing",
				Domain:   pgtype.Text{String: "remote.test", Valid: true},
			},
			{
				ID:       3,
				Username: "remote-local-domain",
				Domain:   pgtype.Text{String: "local.test", Valid: true},
				InboxUrl: "https://local.test/users/remote/inbox",
			},
		},
	}

	resolver := NewInboxResolver(store, "local.test")
	targets, err := resolver.ResolveFollowerInboxes(context.Background(), 10)
	if err != nil {
		t.Fatalf("ResolveFollowerInboxes error: %v", err)
	}

	if len(targets) != 0 {
		t.Fatalf("targets = %d, want 0", len(targets))
	}
}
