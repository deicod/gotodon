package federation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type createStore struct {
	nextAccountID int64
	nextStatusID  int64
	nextMediaID   int64
	nextTagID     int64
	nextNotifID   int64
	accountsByURI map[string]db.Account
	accountsByID  map[int64]db.Account
	statusesByURI map[string]db.Status
	statusesByID  map[int64]db.Status
	statusStats   map[int64]db.StatusStat
	replyCounts   map[int64]int64
	mentions      []db.Mention
	notifications []db.Notification
	tagsByName    map[string]db.Tag
	statusTags    map[int64][]int64
	media         []db.MediaAttachment
}

func newCreateStore() *createStore {
	return &createStore{
		nextAccountID: 1,
		nextStatusID:  1,
		nextMediaID:   1,
		nextTagID:     1,
		nextNotifID:   1,
		accountsByURI: make(map[string]db.Account),
		accountsByID:  make(map[int64]db.Account),
		statusesByURI: make(map[string]db.Status),
		statusesByID:  make(map[int64]db.Status),
		statusStats:   make(map[int64]db.StatusStat),
		replyCounts:   make(map[int64]int64),
		tagsByName:    make(map[string]db.Tag),
		statusTags:    make(map[int64][]int64),
	}
}

func (s *createStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	account, ok := s.accountsByURI[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *createStore) GetAccountByUsername(ctx context.Context, username string) (db.Account, error) {
	for _, account := range s.accountsByID {
		if account.Domain.Valid {
			continue
		}
		if strings.EqualFold(account.Username, username) {
			return account, nil
		}
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *createStore) GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error) {
	for _, account := range s.accountsByID {
		if !account.Domain.Valid {
			continue
		}
		if strings.EqualFold(account.Username, arg.Username) && strings.EqualFold(account.Domain.String, arg.Domain) {
			return account, nil
		}
	}
	return db.Account{}, pgx.ErrNoRows
}

func (s *createStore) CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error) {
	s.nextAccountID++
	account := db.Account{
		ID:           s.nextAccountID,
		Username:     arg.Username,
		Domain:       arg.Domain,
		PrivateKey:   arg.PrivateKey,
		PublicKey:    arg.PublicKey,
		DisplayName:  arg.DisplayName,
		Note:         arg.Note,
		Uri:          arg.Uri,
		Url:          arg.Url,
		Locked:       arg.Locked,
		ActorType:    arg.ActorType,
		Discoverable: arg.Discoverable,
		Indexable:    arg.Indexable,
		Protocol:     arg.Protocol,
	}
	s.accountsByURI[arg.Uri] = account
	s.accountsByID[account.ID] = account
	return account, nil
}

func (s *createStore) CreateAccountStats(ctx context.Context, accountID int64) (db.AccountStat, error) {
	return db.AccountStat{AccountID: accountID}, nil
}

func (s *createStore) GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error) {
	if !uri.Valid || uri.String == "" {
		return db.Status{}, pgx.ErrNoRows
	}
	status, ok := s.statusesByURI[uri.String]
	if !ok {
		return db.Status{}, pgx.ErrNoRows
	}
	return status, nil
}

func (s *createStore) CreateStatus(ctx context.Context, arg db.CreateStatusParams) (db.Status, error) {
	s.nextStatusID++
	now := time.Now().UTC()
	status := db.Status{
		ID:                 s.nextStatusID,
		Uri:                arg.Uri,
		Text:               arg.Text,
		CreatedAt:          pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:          pgtype.Timestamptz{Time: now, Valid: true},
		InReplyToID:        arg.InReplyToID,
		InReplyToAccountID: arg.InReplyToAccountID,
		ReblogOfID:         arg.ReblogOfID,
		Sensitive:          arg.Sensitive,
		Visibility:         arg.Visibility,
		SpoilerText:        arg.SpoilerText,
		Language:           arg.Language,
		ConversationID:     arg.ConversationID,
		Local:              arg.Local,
		AccountID:          arg.AccountID,
		ApplicationID:      arg.ApplicationID,
		Reply:              arg.Reply,
	}
	if arg.Uri.Valid {
		s.statusesByURI[arg.Uri.String] = status
	}
	s.statusesByID[status.ID] = status
	return status, nil
}

func (s *createStore) CreateStatusStats(ctx context.Context, statusID int64) (db.StatusStat, error) {
	stat := db.StatusStat{ID: statusID, StatusID: statusID}
	s.statusStats[statusID] = stat
	return stat, nil
}

func (s *createStore) UpdateStatusURL(ctx context.Context, arg db.UpdateStatusURLParams) error {
	status, ok := s.statusesByID[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	status.Url = arg.Url
	s.statusesByID[arg.ID] = status
	if status.Uri.Valid {
		s.statusesByURI[status.Uri.String] = status
	}
	return nil
}

func (s *createStore) UpdateStatusTimestamps(ctx context.Context, arg db.UpdateStatusTimestampsParams) error {
	status, ok := s.statusesByID[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	status.CreatedAt = arg.CreatedAt
	status.UpdatedAt = arg.UpdatedAt
	s.statusesByID[arg.ID] = status
	if status.Uri.Valid {
		s.statusesByURI[status.Uri.String] = status
	}
	return nil
}

func (s *createStore) IncrementStatusReplies(ctx context.Context, statusID int64) error {
	s.replyCounts[statusID]++
	return nil
}

func (s *createStore) CreateMentions(ctx context.Context, arg db.CreateMentionsParams) error {
	for i, accountID := range arg.Column2 {
		silent := false
		if i < len(arg.Column3) {
			silent = arg.Column3[i]
		}
		s.mentions = append(s.mentions, db.Mention{
			StatusID:  arg.StatusID,
			AccountID: accountID,
			Silent:    silent,
		})
	}
	return nil
}

func (s *createStore) CreateNotifications(ctx context.Context, arg db.CreateNotificationsParams) ([]db.Notification, error) {
	var notifs []db.Notification
	for _, accountID := range arg.Column1 {
		s.nextNotifID++
		notif := db.Notification{
			ID:            s.nextNotifID,
			AccountID:     accountID,
			FromAccountID: arg.FromAccountID,
			ActivityType:  arg.ActivityType,
			ActivityID:    arg.ActivityID,
			Type:          arg.Type,
		}
		s.notifications = append(s.notifications, notif)
		notifs = append(notifs, notif)
	}
	return notifs, nil
}

func (s *createStore) GetMutesFromAccounts(ctx context.Context, arg db.GetMutesFromAccountsParams) ([]db.Mute, error) {
	return nil, nil
}

func (s *createStore) GetBlocksFromAccounts(ctx context.Context, arg db.GetBlocksFromAccountsParams) ([]db.Block, error) {
	return nil, nil
}

func (s *createStore) UpsertTags(ctx context.Context, tags []string) error {
	for _, name := range tags {
		if _, exists := s.tagsByName[name]; exists {
			continue
		}
		s.nextTagID++
		s.tagsByName[name] = db.Tag{ID: s.nextTagID, Name: name}
	}
	return nil
}

func (s *createStore) GetTagsByNames(ctx context.Context, tags []string) ([]db.Tag, error) {
	var result []db.Tag
	for _, name := range tags {
		if tag, ok := s.tagsByName[name]; ok {
			result = append(result, tag)
		}
	}
	return result, nil
}

func (s *createStore) AddStatusTags(ctx context.Context, arg db.AddStatusTagsParams) error {
	s.statusTags[arg.StatusID] = append(s.statusTags[arg.StatusID], arg.Column2...)
	return nil
}

func (s *createStore) CreateRemoteMediaAttachment(ctx context.Context, arg db.CreateRemoteMediaAttachmentParams) (db.MediaAttachment, error) {
	s.nextMediaID++
	attachment := db.MediaAttachment{
		ID:                 s.nextMediaID,
		StatusID:           arg.StatusID,
		RemoteUrl:          arg.RemoteUrl,
		ThumbnailRemoteUrl: arg.ThumbnailRemoteUrl,
		Type:               arg.Type,
		AccountID:          arg.AccountID,
		Description:        arg.Description,
		Blurhash:           arg.Blurhash,
		FileContentType:    arg.FileContentType,
		FileMeta:           arg.FileMeta,
		Processing:         arg.Processing,
	}
	s.media = append(s.media, attachment)
	return attachment, nil
}

func TestCreateService_CreateNote(t *testing.T) {
	store := newCreateStore()
	local := db.Account{ID: 1, Uri: "https://local.test/users/bob"}
	store.accountsByURI[local.Uri] = local
	store.accountsByID[local.ID] = local

	parent := db.Status{
		ID:        10,
		Uri:       pgtype.Text{String: "https://local.test/statuses/10", Valid: true},
		AccountID: local.ID,
	}
	store.statusesByURI[parent.Uri.String] = parent
	store.statusesByID[parent.ID] = parent

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

	service := NewCreateService(store, "local.test", stubActorFetcher{actor: actor})

	noteID := "https://remote.example/statuses/123"
	activity := &activitypub.Activity{
		Type:  activitypub.TypeCreate,
		Actor: actorURI,
		ActivityObject: map[string]any{
			"id":           noteID,
			"type":         activitypub.TypeNote,
			"content":      "<p>Hello @bob</p>",
			"summary":      "CW",
			"sensitive":    true,
			"attributedTo": actorURI,
			"to":           []any{activitypub.PublicAudience},
			"cc":           []any{local.Uri},
			"inReplyTo":    parent.Uri.String,
			"attachment": []any{
				map[string]any{
					"type":      "Document",
					"mediaType": "image/png",
					"url":       "https://remote.example/media/file.png",
					"name":      "preview",
				},
			},
			"tag": []any{
				map[string]any{
					"type": "Mention",
					"href": local.Uri,
					"name": "@bob@local.test",
				},
				map[string]any{
					"type": "Hashtag",
					"name": "#golang",
				},
			},
			"published": "2024-01-02T03:04:05Z",
		},
	}

	if err := service.HandleCreate(context.Background(), activity); err != nil {
		t.Fatalf("HandleCreate() error = %v", err)
	}

	created, ok := store.statusesByURI[noteID]
	if !ok {
		t.Fatal("expected status to be created")
	}
	if created.AccountID == local.ID {
		t.Fatal("expected status to belong to remote actor")
	}
	if !created.InReplyToID.Valid || created.InReplyToID.Int64 != parent.ID {
		t.Fatalf("inReplyToID = %v, want %d", created.InReplyToID, parent.ID)
	}
	if store.replyCounts[parent.ID] != 1 {
		t.Fatalf("reply count = %d, want 1", store.replyCounts[parent.ID])
	}
	if created.Visibility != httpVisibilityPublic {
		t.Fatalf("visibility = %d, want %d", created.Visibility, httpVisibilityPublic)
	}
	if !created.Sensitive || created.SpoilerText != "CW" {
		t.Fatal("expected sensitive status with spoiler text")
	}
	if created.Url.String == "" {
		t.Fatal("expected status URL to be set")
	}

	if len(store.media) != 1 {
		t.Fatalf("media attachments = %d, want 1", len(store.media))
	}
	if store.media[0].RemoteUrl == "" {
		t.Fatal("expected remote_url to be set on media attachment")
	}

	if len(store.mentions) != 1 || store.mentions[0].AccountID != local.ID {
		t.Fatalf("mentions = %+v, want mention for local account", store.mentions)
	}
	if len(store.notifications) != 1 || store.notifications[0].AccountID != local.ID {
		t.Fatalf("notifications = %+v, want mention notification", store.notifications)
	}

	if _, ok := store.tagsByName["golang"]; !ok {
		t.Fatal("expected hashtag to be created")
	}
	if len(store.statusTags[created.ID]) == 0 {
		t.Fatal("expected status tags to be linked")
	}
}

func TestCreateService_DeduplicatesByURI(t *testing.T) {
	store := newCreateStore()
	actorURI := "https://remote.example/users/alice"
	statusURI := "https://remote.example/statuses/123"
	store.statusesByURI[statusURI] = db.Status{
		ID:  10,
		Uri: pgtype.Text{String: statusURI, Valid: true},
	}

	service := NewCreateService(store, "local.test", stubActorFetcher{actor: &activitypub.Actor{}})
	activity := &activitypub.Activity{
		Type:  activitypub.TypeCreate,
		Actor: actorURI,
		ActivityObject: map[string]any{
			"id":      statusURI,
			"type":    activitypub.TypeNote,
			"content": "hello",
		},
	}

	if err := service.HandleCreate(context.Background(), activity); err != nil {
		t.Fatalf("HandleCreate() error = %v", err)
	}
	if len(store.media) != 0 || len(store.mentions) != 0 || len(store.notifications) != 0 {
		t.Fatal("expected no side effects for duplicate status")
	}
}

func TestCreateService_ActorMismatch(t *testing.T) {
	store := newCreateStore()
	actorURI := "https://remote.example/users/alice"
	service := NewCreateService(store, "local.test", stubActorFetcher{actor: &activitypub.Actor{}})
	activity := &activitypub.Activity{
		Type:  activitypub.TypeCreate,
		Actor: actorURI,
		ActivityObject: map[string]any{
			"id":           "https://remote.example/statuses/123",
			"type":         activitypub.TypeNote,
			"content":      "hello",
			"attributedTo": "https://remote.example/users/other",
		},
	}

	err := service.HandleCreate(context.Background(), activity)
	var inboxErr *apihandler.InboxError
	if !errors.As(err, &inboxErr) {
		t.Fatalf("expected InboxError, got %v", err)
	}
	if inboxErr.Status != httpStatusBadRequest {
		t.Fatalf("status = %d, want %d", inboxErr.Status, httpStatusBadRequest)
	}
}

func TestVisibilityFromRecipients(t *testing.T) {
	actorURI := "https://remote.example/users/alice"
	actor := &activitypub.Actor{Followers: actorURI + "/followers"}

	tests := []struct {
		name string
		to   []string
		cc   []string
		want int32
	}{
		{name: "public", to: []string{activitypub.PublicAudience}, cc: nil, want: httpVisibilityPublic},
		{name: "unlisted", to: []string{actorURI + "/followers"}, cc: []string{activitypub.PublicAudience}, want: httpVisibilityUnlisted},
		{name: "private", to: []string{actorURI + "/followers"}, cc: nil, want: httpVisibilityPrivate},
		{name: "direct", to: []string{actorURI + "/recipients"}, cc: nil, want: httpVisibilityDirect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visibilityFromRecipients(tt.to, tt.cc, actorURI, actor)
			if got != tt.want {
				t.Fatalf("visibility = %d, want %d", got, tt.want)
			}
		})
	}
}
