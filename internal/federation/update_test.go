package federation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type updateStore struct {
	*createStore
	statusEdits []db.StatusEdit
}

func newUpdateStore() *updateStore {
	return &updateStore{createStore: newCreateStore()}
}

func (s *updateStore) UpdateStatus(ctx context.Context, arg db.UpdateStatusParams) (db.Status, error) {
	status, ok := s.statusesByID[arg.ID]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	if arg.Text.Valid {
		status.Text = arg.Text.String
	}
	if arg.SpoilerText.Valid {
		status.SpoilerText = arg.SpoilerText.String
	}
	if arg.Sensitive.Valid {
		status.Sensitive = arg.Sensitive.Bool
	}
	if arg.Language.Valid {
		status.Language = arg.Language
	}
	status.EditedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	status.UpdatedAt = status.EditedAt
	s.statusesByID[arg.ID] = status
	if status.Uri.Valid {
		s.statusesByURI[status.Uri.String] = status
	}
	return status, nil
}

func (s *updateStore) CreateStatusEdit(ctx context.Context, arg db.CreateStatusEditParams) (db.StatusEdit, error) {
	edit := db.StatusEdit{
		ID:                        int64(len(s.statusEdits) + 1),
		StatusID:                  arg.StatusID,
		AccountID:                 arg.AccountID,
		Text:                      arg.Text,
		SpoilerText:               arg.SpoilerText,
		OrderedMediaAttachmentIds: arg.OrderedMediaAttachmentIds,
		MediaDescriptions:         arg.MediaDescriptions,
		PollOptions:               arg.PollOptions,
		Sensitive:                 arg.Sensitive,
		CreatedAt:                 pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		UpdatedAt:                 pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	s.statusEdits = append(s.statusEdits, edit)
	return edit, nil
}

func (s *updateStore) ListMediaAttachmentsByStatus(ctx context.Context, statusID pgtype.Int8) ([]db.MediaAttachment, error) {
	if !statusID.Valid {
		return nil, nil
	}
	var result []db.MediaAttachment
	for _, att := range s.media {
		if att.StatusID.Valid && att.StatusID.Int64 == statusID.Int64 {
			result = append(result, att)
		}
	}
	return result, nil
}

func (s *updateStore) UpdateMediaAttachment(ctx context.Context, arg db.UpdateMediaAttachmentParams) (db.MediaAttachment, error) {
	for i, att := range s.media {
		if att.ID != arg.ID {
			continue
		}
		if att.AccountID.Valid && arg.AccountID.Valid && att.AccountID.Int64 != arg.AccountID.Int64 {
			continue
		}
		if arg.Description.Valid {
			att.Description = arg.Description
		}
		if arg.FileMeta != nil {
			att.FileMeta = arg.FileMeta
		}
		s.media[i] = att
		return att, nil
	}
	return db.MediaAttachment{}, pgx.ErrNoRows
}

func (s *updateStore) DeleteMediaAttachment(ctx context.Context, arg db.DeleteMediaAttachmentParams) error {
	updated := s.media[:0]
	for _, att := range s.media {
		if att.ID == arg.ID && att.AccountID.Valid && arg.AccountID.Valid && att.AccountID.Int64 == arg.AccountID.Int64 {
			continue
		}
		updated = append(updated, att)
	}
	s.media = updated
	return nil
}

func (s *updateStore) GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error) {
	var result []db.Mention
	for _, mention := range s.mentions {
		if mention.StatusID == statusID {
			result = append(result, mention)
		}
	}
	return result, nil
}

func (s *updateStore) RemoveMentions(ctx context.Context, arg db.RemoveMentionsParams) error {
	toRemove := make(map[int64]bool)
	for _, id := range arg.Column2 {
		toRemove[id] = true
	}
	updated := s.mentions[:0]
	for _, mention := range s.mentions {
		if mention.StatusID == arg.StatusID && toRemove[mention.AccountID] {
			continue
		}
		updated = append(updated, mention)
	}
	s.mentions = updated
	return nil
}

func (s *updateStore) ClearStatusTags(ctx context.Context, statusID int64) error {
	delete(s.statusTags, statusID)
	return nil
}

func (s *updateStore) UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error) {
	account, ok := s.accountsByID[arg.ID]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	if arg.DisplayName.Valid {
		account.DisplayName = arg.DisplayName.String
	}
	if arg.Note.Valid {
		account.Note = arg.Note.String
	}
	if arg.Locked.Valid {
		account.Locked = arg.Locked.Bool
	}
	if arg.Discoverable.Valid {
		account.Discoverable = arg.Discoverable
	}
	if arg.Indexable.Valid {
		account.Indexable = arg.Indexable.Bool
	}
	if arg.Fields != nil {
		account.Fields = arg.Fields
	}
	if arg.Url.Valid {
		account.Url = arg.Url
	}
	if arg.PublicKey.Valid {
		account.PublicKey = arg.PublicKey.String
	}
	if arg.ActorType.Valid {
		account.ActorType = arg.ActorType
	}
	if arg.InboxUrl.Valid {
		account.InboxUrl = arg.InboxUrl.String
	}
	if arg.OutboxUrl.Valid {
		account.OutboxUrl = arg.OutboxUrl.String
	}
	if arg.SharedInboxUrl.Valid {
		account.SharedInboxUrl = arg.SharedInboxUrl.String
	}
	if arg.FollowersUrl.Valid {
		account.FollowersUrl = arg.FollowersUrl.String
	}
	if arg.FollowingUrl.Valid {
		account.FollowingUrl = arg.FollowingUrl.String
	}
	if arg.FeaturedCollectionUrl.Valid {
		account.FeaturedCollectionUrl = arg.FeaturedCollectionUrl
	}
	if arg.AvatarRemoteUrl.Valid {
		account.AvatarRemoteUrl = arg.AvatarRemoteUrl
	}
	if arg.HeaderRemoteUrl.Valid {
		account.HeaderRemoteUrl = arg.HeaderRemoteUrl.String
	}
	if arg.AvatarFileName.Valid {
		account.AvatarFileName = arg.AvatarFileName
	}
	if arg.HeaderFileName.Valid {
		account.HeaderFileName = arg.HeaderFileName
	}
	s.accountsByID[account.ID] = account
	s.accountsByURI[account.Uri] = account
	return account, nil
}

func TestUpdateService_UpdateNote(t *testing.T) {
	store := newUpdateStore()
	author := db.Account{
		ID:     10,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accountsByURI[author.Uri] = author
	store.accountsByID[author.ID] = author

	status := db.Status{
		ID:          100,
		Uri:         pgtype.Text{String: "https://remote.example/statuses/1", Valid: true},
		Text:        "old text",
		SpoilerText: "old cw",
		Sensitive:   false,
		AccountID:   author.ID,
		Local:       pgtype.Bool{Bool: false, Valid: true},
	}
	store.statusesByURI[status.Uri.String] = status
	store.statusesByID[status.ID] = status

	store.media = append(store.media, db.MediaAttachment{
		ID:          1,
		StatusID:    pgtype.Int8{Int64: status.ID, Valid: true},
		RemoteUrl:   "https://remote.example/media/1",
		Description: pgtype.Text{String: "old desc", Valid: true},
		AccountID:   pgtype.Int8{Int64: author.ID, Valid: true},
	})
	store.media = append(store.media, db.MediaAttachment{
		ID:        2,
		StatusID:  pgtype.Int8{Int64: status.ID, Valid: true},
		RemoteUrl: "https://remote.example/media/2",
		AccountID: pgtype.Int8{Int64: author.ID, Valid: true},
	})
	store.nextMediaID = 2

	mention1 := db.Account{ID: 20, Uri: "https://local.test/users/bob"}
	mention2 := db.Account{ID: 21, Uri: "https://local.test/users/charlie"}
	mention3 := db.Account{ID: 22, Uri: "https://local.test/users/dora"}
	store.accountsByURI[mention1.Uri] = mention1
	store.accountsByID[mention1.ID] = mention1
	store.accountsByURI[mention2.Uri] = mention2
	store.accountsByID[mention2.ID] = mention2
	store.accountsByURI[mention3.Uri] = mention3
	store.accountsByID[mention3.ID] = mention3

	store.mentions = []db.Mention{
		{StatusID: status.ID, AccountID: mention1.ID},
		{StatusID: status.ID, AccountID: mention2.ID},
	}

	service := NewUpdateService(store, "local.test", stubActorFetcher{actor: &activitypub.Actor{}})

	activity := &activitypub.Activity{
		Type:  activitypub.TypeUpdate,
		Actor: author.Uri,
		ActivityObject: map[string]any{
			"id":        status.Uri.String,
			"type":      activitypub.TypeNote,
			"content":   "<p>new text</p>",
			"summary":   "new cw",
			"sensitive": true,
			"language":  "en",
			"tag": []any{
				map[string]any{"type": "Mention", "href": mention2.Uri, "name": "@charlie"},
				map[string]any{"type": "Mention", "href": mention3.Uri, "name": "@dora"},
				map[string]any{"type": "Hashtag", "name": "#newtag"},
			},
			"attachment": []any{
				map[string]any{"type": "Image", "url": "https://remote.example/media/2", "name": "updated"},
				map[string]any{"type": "Image", "url": "https://remote.example/media/3", "name": "new"},
			},
		},
	}

	if err := service.HandleUpdate(context.Background(), activity); err != nil {
		t.Fatalf("HandleUpdate error: %v", err)
	}

	updated := store.statusesByID[status.ID]
	if updated.Text != "<p>new text</p>" {
		t.Fatalf("text = %q, want %q", updated.Text, "<p>new text</p>")
	}
	if updated.SpoilerText != "new cw" {
		t.Fatalf("spoiler = %q, want %q", updated.SpoilerText, "new cw")
	}
	if !updated.Sensitive {
		t.Fatalf("sensitive = false, want true")
	}
	if updated.Language.String != "en" {
		t.Fatalf("language = %q, want %q", updated.Language.String, "en")
	}

	if len(store.statusEdits) != 1 {
		t.Fatalf("status edits = %d, want 1", len(store.statusEdits))
	}
	if store.statusEdits[0].Text != "old text" {
		t.Fatalf("status edit text = %q, want %q", store.statusEdits[0].Text, "old text")
	}

	if len(store.media) != 2 {
		t.Fatalf("media count = %d, want 2", len(store.media))
	}
	foundNew := false
	foundUpdated := false
	for _, att := range store.media {
		switch att.RemoteUrl {
		case "https://remote.example/media/2":
			foundUpdated = true
			if !att.Description.Valid || att.Description.String != "updated" {
				t.Fatalf("media 2 description = %q, want %q", att.Description.String, "updated")
			}
		case "https://remote.example/media/3":
			foundNew = true
		}
	}
	if !foundUpdated || !foundNew {
		t.Fatalf("media updated=%v new=%v, want true/true", foundUpdated, foundNew)
	}

	tagIDs := store.statusTags[status.ID]
	if len(tagIDs) != 1 {
		t.Fatalf("tag count = %d, want 1", len(tagIDs))
	}
	tagID := tagIDs[0]
	if tagID == 0 || store.tagsByName["newtag"].ID != tagID {
		t.Fatalf("tag id = %d, want %d", tagID, store.tagsByName["newtag"].ID)
	}

	mentions := make(map[int64]bool)
	for _, mention := range store.mentions {
		if mention.StatusID == status.ID {
			mentions[mention.AccountID] = true
		}
	}
	if mentions[mention1.ID] {
		t.Fatalf("mention for removed account still present")
	}
	if !mentions[mention2.ID] || !mentions[mention3.ID] {
		t.Fatalf("mentions missing for updated accounts")
	}

	if len(store.notifications) != 1 || store.notifications[0].AccountID != mention3.ID {
		t.Fatalf("notifications = %+v, want one for account %d", store.notifications, mention3.ID)
	}
}

func TestUpdateService_UpdatePerson(t *testing.T) {
	store := newUpdateStore()
	account := db.Account{
		ID:     5,
		Uri:    "https://remote.example/users/alice",
		Domain: pgtype.Text{String: "remote.example", Valid: true},
	}
	store.accountsByURI[account.Uri] = account
	store.accountsByID[account.ID] = account

	service := NewUpdateService(store, "local.test", stubActorFetcher{actor: &activitypub.Actor{}})

	activity := &activitypub.Activity{
		Type:  activitypub.TypeUpdate,
		Actor: account.Uri,
		ActivityObject: map[string]any{
			"id":        account.Uri,
			"type":      activitypub.TypePerson,
			"name":      "Alice Updated",
			"summary":   "bio",
			"url":       "https://remote.example/@alice",
			"inbox":     "https://remote.example/users/alice/inbox",
			"outbox":    "https://remote.example/users/alice/outbox",
			"followers": "https://remote.example/users/alice/followers",
			"following": "https://remote.example/users/alice/following",
			"featured":  "https://remote.example/users/alice/collections/featured",
			"endpoints": map[string]any{
				"sharedInbox": "https://remote.example/inbox",
			},
			"manuallyApprovesFollowers": true,
			"discoverable":              true,
			"indexable":                 true,
			"icon": map[string]any{
				"url": "https://remote.example/avatar.png",
			},
			"image": map[string]any{
				"url": "https://remote.example/header.png",
			},
			"publicKey": map[string]any{
				"publicKeyPem": "new-key",
			},
			"attachment": []any{
				map[string]any{
					"type":  "PropertyValue",
					"name":  "Website",
					"value": "https://example.com",
				},
			},
		},
	}

	if err := service.HandleUpdate(context.Background(), activity); err != nil {
		t.Fatalf("HandleUpdate error: %v", err)
	}

	updated := store.accountsByID[account.ID]
	if updated.DisplayName != "Alice Updated" {
		t.Fatalf("display name = %q, want %q", updated.DisplayName, "Alice Updated")
	}
	if updated.Note != "bio" {
		t.Fatalf("note = %q, want %q", updated.Note, "bio")
	}
	if !updated.Locked {
		t.Fatalf("locked = false, want true")
	}
	if !updated.Discoverable.Valid || !updated.Discoverable.Bool {
		t.Fatalf("discoverable = %+v, want true", updated.Discoverable)
	}
	if !updated.Indexable {
		t.Fatalf("indexable = false, want true")
	}
	if updated.Url.String != "https://remote.example/@alice" {
		t.Fatalf("url = %q, want %q", updated.Url.String, "https://remote.example/@alice")
	}
	if updated.PublicKey != "new-key" {
		t.Fatalf("public key = %q, want %q", updated.PublicKey, "new-key")
	}
	if updated.InboxUrl != "https://remote.example/users/alice/inbox" {
		t.Fatalf("inbox = %q, want %q", updated.InboxUrl, "https://remote.example/users/alice/inbox")
	}
	if updated.SharedInboxUrl != "https://remote.example/inbox" {
		t.Fatalf("shared inbox = %q, want %q", updated.SharedInboxUrl, "https://remote.example/inbox")
	}
	if updated.AvatarRemoteUrl.String != "https://remote.example/avatar.png" {
		t.Fatalf("avatar url = %q, want %q", updated.AvatarRemoteUrl.String, "https://remote.example/avatar.png")
	}
	if updated.HeaderRemoteUrl != "https://remote.example/header.png" {
		t.Fatalf("header url = %q, want %q", updated.HeaderRemoteUrl, "https://remote.example/header.png")
	}

	var fields []map[string]string
	if err := json.Unmarshal(updated.Fields, &fields); err != nil {
		t.Fatalf("fields json: %v", err)
	}
	if len(fields) != 1 || fields[0]["name"] != "Website" || fields[0]["value"] != "https://example.com" {
		t.Fatalf("fields = %+v, want Website entry", fields)
	}
}
