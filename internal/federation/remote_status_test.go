package federation

import (
	"context"
	"errors"
	"testing"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type mapObjectFetcher struct {
	objects map[string]any
	calls   map[string]int
	err     error
}

func (f *mapObjectFetcher) FetchObject(ctx context.Context, objectURI string) (any, error) {
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[objectURI]++
	if f.err != nil {
		return nil, f.err
	}
	obj, ok := f.objects[objectURI]
	if !ok {
		return nil, errors.New("object not found")
	}
	return obj, nil
}

func TestRemoteStatusFetcher_FetchStatusCreatesStatusAndMedia(t *testing.T) {
	store := newCreateStore()

	local := db.Account{ID: 1, Uri: "https://local.test/users/bob"}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	actorURI := "https://remote.example/users/alice"
	actor := &activitypub.Actor{
		Object: activitypub.Object{
			ID:   actorURI,
			Type: activitypub.TypePerson,
			Name: "Alice",
		},
		PreferredUsername: "alice",
		Inbox:             "https://remote.example/inbox",
		PublicKey:         &activitypub.PublicKey{PublicKeyPem: "remote-public"},
	}

	statusURI := "https://remote.example/statuses/123"
	note := map[string]any{
		"id":           statusURI,
		"type":         activitypub.TypeNote,
		"content":      "<p>Hello @bob</p>",
		"attributedTo": actorURI,
		"to":           []any{activitypub.PublicAudience},
		"attachment": []any{
			map[string]any{
				"type":      "Document",
				"mediaType": "image/png",
				"url":       "https://remote.example/media/file.png",
			},
		},
		"tag": []any{
			map[string]any{
				"type": "Mention",
				"href": local.Uri,
				"name": "@bob@local.test",
			},
		},
	}

	fetcher := NewRemoteStatusFetcher(store, "local.test", stubActorFetcher{actor: actor})
	objectFetcher := &mapObjectFetcher{
		objects: map[string]any{
			statusURI: note,
		},
	}
	fetcher.objectFetcher = objectFetcher

	status, err := fetcher.FetchStatus(context.Background(), statusURI)
	if err != nil {
		t.Fatalf("FetchStatus error: %v", err)
	}
	if status.ID == 0 {
		t.Fatal("expected status to be created")
	}
	if len(store.media) != 1 {
		t.Fatalf("media attachments = %d, want 1", len(store.media))
	}
	if store.media[0].RemoteUrl == "" {
		t.Fatal("expected remote_url to be set on media attachment")
	}
	if len(store.mentions) != 0 {
		t.Fatalf("mentions = %d, want 0", len(store.mentions))
	}
	if len(store.notifications) != 0 {
		t.Fatalf("notifications = %d, want 0", len(store.notifications))
	}
}

func TestRemoteStatusFetcher_FetchThreadLoadsReplies(t *testing.T) {
	store := newCreateStore()

	actorURI := "https://remote.example/users/alice"
	actor := &activitypub.Actor{
		Object: activitypub.Object{
			ID:   actorURI,
			Type: activitypub.TypePerson,
		},
		PreferredUsername: "alice",
		Inbox:             "https://remote.example/inbox",
		PublicKey:         &activitypub.PublicKey{PublicKeyPem: "remote-public"},
	}

	rootURI := "https://remote.example/statuses/root"
	replyURI := "https://remote.example/statuses/reply"

	rootNote := map[string]any{
		"id":           rootURI,
		"type":         activitypub.TypeNote,
		"content":      "root",
		"attributedTo": actorURI,
		"to":           []any{activitypub.PublicAudience},
		"replies": map[string]any{
			"type":  activitypub.TypeCollection,
			"items": []any{replyURI},
		},
	}

	replyNote := map[string]any{
		"id":           replyURI,
		"type":         activitypub.TypeNote,
		"content":      "reply",
		"attributedTo": actorURI,
		"inReplyTo":    rootURI,
		"to":           []any{activitypub.PublicAudience},
	}

	fetcher := NewRemoteStatusFetcher(store, "local.test", stubActorFetcher{actor: actor})
	objectFetcher := &mapObjectFetcher{
		objects: map[string]any{
			rootURI:  rootNote,
			replyURI: replyNote,
		},
	}
	fetcher.objectFetcher = objectFetcher

	rootStatus, err := fetcher.FetchStatus(context.Background(), rootURI)
	if err != nil {
		t.Fatalf("FetchStatus error: %v", err)
	}
	if err := fetcher.FetchThread(context.Background(), rootStatus); err != nil {
		t.Fatalf("FetchThread error: %v", err)
	}

	reply, ok := store.statusesByURI[replyURI]
	if !ok {
		t.Fatal("expected reply status to be created")
	}
	root, ok := store.statusesByURI[rootURI]
	if !ok {
		t.Fatal("expected root status to be present")
	}
	if !root.FetchedRepliesAt.Valid {
		t.Fatal("expected fetched_replies_at to be set")
	}
	if !reply.InReplyToID.Valid || reply.InReplyToID.Int64 != root.ID {
		t.Fatalf("reply InReplyToID = %v, want %d", reply.InReplyToID, root.ID)
	}
	if !reply.InReplyToAccountID.Valid || reply.InReplyToAccountID.Int64 != root.AccountID {
		t.Fatalf("reply InReplyToAccountID = %v, want %d", reply.InReplyToAccountID, root.AccountID)
	}
	if root.FetchedRepliesAt.Time.IsZero() {
		t.Fatal("expected fetched_replies_at timestamp to be set")
	}
}
