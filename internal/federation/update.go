package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// UpdateStore describes the persistence needed for incoming Update activities.
type UpdateStore interface {
	CreateStore
	UpdateStatus(ctx context.Context, arg db.UpdateStatusParams) (db.Status, error)
	CreateStatusEdit(ctx context.Context, arg db.CreateStatusEditParams) (db.StatusEdit, error)
	ListMediaAttachmentsByStatus(ctx context.Context, statusID pgtype.Int8) ([]db.MediaAttachment, error)
	UpdateMediaAttachment(ctx context.Context, arg db.UpdateMediaAttachmentParams) (db.MediaAttachment, error)
	DeleteMediaAttachment(ctx context.Context, arg db.DeleteMediaAttachmentParams) error
	GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error)
	RemoveMentions(ctx context.Context, arg db.RemoveMentionsParams) error
	ClearStatusTags(ctx context.Context, statusID int64) error
	UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error)
}

// UpdateService processes incoming Update activities.
type UpdateService struct {
	store         UpdateStore
	localDomain   string
	fetcher       activitypub.ActorFetcher
	helper        *CreateService
	pushPublisher PushNotificationPublisher
}

// SetPushPublisher enables Web Push delivery for mention notifications
// created by status edits.
func (s *UpdateService) SetPushPublisher(publisher PushNotificationPublisher) {
	s.pushPublisher = publisher
}

// NewUpdateService creates an Update service for incoming edits.
func NewUpdateService(store UpdateStore, localDomain string, fetcher activitypub.ActorFetcher) *UpdateService {
	return &UpdateService{
		store:       store,
		localDomain: localDomain,
		fetcher:     fetcher,
		helper:      NewCreateService(store, localDomain, fetcher),
	}
}

// HandleUpdate processes Update activities for notes and person profiles.
func (s *UpdateService) HandleUpdate(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid update activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing update actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if isLocalURI(actorURI, s.localDomain) {
		return nil
	}

	objectType := extractObjectType(activity.ActivityObject)
	if strings.EqualFold(objectType, activitypub.TypeNote) {
		return s.handleUpdateNote(ctx, activity, actorURI)
	}

	if objectType == "" || isActorType(objectType) {
		return s.handleUpdatePerson(ctx, activity, actorURI)
	}

	return nil
}

func (s *UpdateService) handleUpdateNote(ctx context.Context, activity *activitypub.Activity, actorURI string) error {
	note, err := parseUpdateNote(activity.ActivityObject)
	if err != nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: err.Error()}
	}
	if note.Type == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note type"}
	}
	if !strings.EqualFold(note.Type, activitypub.TypeNote) {
		return nil
	}

	attributedTo := extractAttributedActor(note.AttributedTo)
	if attributedTo != "" && attributedTo != actorURI {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Note actor mismatch"}
	}

	noteURI := note.ID
	if noteURI == "" {
		noteURI = extractURL(note.URL)
	}
	if noteURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note id"}
	}

	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: noteURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if status.Local.Valid && status.Local.Bool {
		return nil
	}

	account, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Actor not found"}
		}
		return err
	}
	if account.ID != status.AccountID {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Note actor mismatch"}
	}
	if account.SuspendedAt.Valid {
		return &apihandler.InboxError{Status: httpStatusForbidden, Message: "Actor is suspended"}
	}

	if note.Updated != "" {
		if updatedAt, ok := parseTimestamp(note.Updated); ok {
			if status.EditedAt.Valid && !updatedAt.Time.After(status.EditedAt.Time) {
				return nil
			}
			if !status.EditedAt.Valid && status.UpdatedAt.Valid && !updatedAt.Time.After(status.UpdatedAt.Time) {
				return nil
			}
		}
	}

	content := note.Content
	contentProvided := note.HasContent
	if content == "" && note.ContentMap != nil {
		if lang, text := firstContentMapEntry(note.ContentMap); text != "" {
			content = text
			if note.Language == "" {
				note.Language = lang
			}
			contentProvided = true
		}
	}

	attachments := extractAttachments(note.Attachments)
	attachmentsProvided := note.HasAttachments

	hasContentOrAttachments := contentProvided || attachmentsProvided
	if hasContentOrAttachments && content == "" && len(attachments) == 0 {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note content"}
	}

	existingMedia, err := s.store.ListMediaAttachmentsByStatus(ctx, pgtype.Int8{Int64: status.ID, Valid: true})
	if err != nil {
		return err
	}

	shouldRecordEdit := contentProvided || note.HasSummary || note.HasSensitive || note.HasLanguage || attachmentsProvided || note.HasTags || note.HasTo || note.HasCc
	if shouldRecordEdit {
		if err := s.recordStatusEdit(ctx, status, existingMedia); err != nil {
			return err
		}
	}

	if contentProvided || note.HasSummary || note.HasSensitive || note.HasLanguage {
		params := db.UpdateStatusParams{ID: status.ID}
		if contentProvided {
			params.Text = pgtype.Text{String: content, Valid: true}
		}
		if note.HasSummary {
			params.SpoilerText = pgtype.Text{String: note.Summary, Valid: true}
		}
		if note.HasSensitive {
			params.Sensitive = pgtype.Bool{Bool: note.Sensitive, Valid: true}
		}
		if note.HasLanguage {
			params.Language = pgtype.Text{String: note.Language, Valid: true}
		} else if note.HasContentMap && note.Language != "" {
			params.Language = pgtype.Text{String: note.Language, Valid: true}
		}
		if _, err := s.store.UpdateStatus(ctx, params); err != nil {
			return err
		}
	}

	if attachmentsProvided {
		if err := s.syncAttachments(ctx, status.ID, account.ID, existingMedia, attachments); err != nil {
			return err
		}
	}

	if note.HasTags {
		if err := s.syncTags(ctx, status.ID, extractHashtags(note.Tags)); err != nil {
			return err
		}
	}

	if note.HasTags || note.HasTo || note.HasCc || len(activity.To) > 0 || len(activity.Cc) > 0 {
		to := mergeRecipients(note.To, activity.To)
		cc := mergeRecipients(note.Cc, activity.Cc)
		recipients := mergeRecipients(to, cc)
		if err := s.syncMentions(ctx, account.ID, status.ID, recipients, note.Tags); err != nil {
			return err
		}
	}

	return nil
}

func (s *UpdateService) handleUpdatePerson(ctx context.Context, activity *activitypub.Activity, actorURI string) error {
	payload, ok, err := parseUpdateActor(activity.ActivityObject)
	if err != nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: err.Error()}
	}
	if !ok {
		return nil
	}
	if payload.ID == "" {
		payload.ID = actorURI
	}
	if payload.ID != "" && payload.ID != actorURI {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Update actor mismatch"}
	}

	if payload.Type == "" && s.fetcher != nil {
		actor, err := s.fetcher.FetchActor(ctx, actorURI)
		if err == nil && actor != nil {
			payload = actorPayloadFromActor(actor)
		}
	}
	if payload.Type == "" || !isActorType(payload.Type) {
		return nil
	}

	account, err := s.store.GetAccountByURI(ctx, actorURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if s.fetcher == nil {
				return nil
			}
			actor, err := s.fetcher.FetchActor(ctx, actorURI)
			if err != nil || actor == nil {
				return nil
			}
			account, err = s.helper.ensureActorAccount(ctx, actorURI, actor)
			if err != nil {
				var inboxErr *apihandler.InboxError
				if errors.As(err, &inboxErr) {
					return nil
				}
				return err
			}
		} else {
			return err
		}
	}
	if !account.Domain.Valid {
		return nil
	}

	params := db.UpdateAccountParams{ID: account.ID}
	updateNeeded := false

	if payload.Name != "" {
		params.DisplayName = pgtype.Text{String: payload.Name, Valid: true}
		updateNeeded = true
	}

	note := payload.Summary
	if note == "" {
		note = payload.Content
	}
	if payload.HasSummary || payload.HasContent {
		params.Note = pgtype.Text{String: note, Valid: true}
		updateNeeded = true
	} else if note != "" {
		params.Note = pgtype.Text{String: note, Valid: true}
		updateNeeded = true
	}

	if payload.HasManuallyApprovesFollowers {
		params.Locked = pgtype.Bool{Bool: payload.ManuallyApprovesFollowers, Valid: true}
		updateNeeded = true
	}
	if payload.Discoverable != nil {
		params.Discoverable = pgtype.Bool{Bool: *payload.Discoverable, Valid: true}
		updateNeeded = true
	}
	if payload.Indexable != nil {
		params.Indexable = pgtype.Bool{Bool: *payload.Indexable, Valid: true}
		updateNeeded = true
	}

	if url := extractURL(payload.URL); url != "" {
		params.Url = pgtype.Text{String: url, Valid: true}
		updateNeeded = true
	}
	if payload.PublicKey != "" {
		params.PublicKey = pgtype.Text{String: payload.PublicKey, Valid: true}
		updateNeeded = true
	}
	if payload.Type != "" {
		params.ActorType = pgtype.Text{String: payload.Type, Valid: true}
		updateNeeded = true
	}
	if inbox := payload.Inbox; inbox != "" {
		params.InboxUrl = pgtype.Text{String: inbox, Valid: true}
		updateNeeded = true
	}
	if outbox := payload.Outbox; outbox != "" {
		params.OutboxUrl = pgtype.Text{String: outbox, Valid: true}
		updateNeeded = true
	}
	if followers := payload.Followers; followers != "" {
		params.FollowersUrl = pgtype.Text{String: followers, Valid: true}
		updateNeeded = true
	}
	if following := payload.Following; following != "" {
		params.FollowingUrl = pgtype.Text{String: following, Valid: true}
		updateNeeded = true
	}
	if featured := payload.Featured; featured != "" {
		params.FeaturedCollectionUrl = pgtype.Text{String: featured, Valid: true}
		updateNeeded = true
	}
	if sharedInbox := payload.SharedInbox; sharedInbox != "" {
		params.SharedInboxUrl = pgtype.Text{String: sharedInbox, Valid: true}
		updateNeeded = true
	}

	if avatarURL := imageURL(payload.Icon); avatarURL != "" {
		params.AvatarRemoteUrl = pgtype.Text{String: avatarURL, Valid: true}
		updateNeeded = true
	}
	if headerURL := imageURL(payload.Image); headerURL != "" {
		params.HeaderRemoteUrl = pgtype.Text{String: headerURL, Valid: true}
		updateNeeded = true
	}

	if payload.HasAttachment {
		fields := parseActorFields(payload.Attachment)
		if data, err := json.Marshal(fields); err == nil {
			params.Fields = data
			updateNeeded = true
		}
	}

	if !updateNeeded {
		return nil
	}

	_, err = s.store.UpdateAccount(ctx, params)
	return err
}

func (s *UpdateService) recordStatusEdit(ctx context.Context, status db.Status, media []db.MediaAttachment) error {
	orderedIDs := make([]int64, 0, len(media))
	descriptions := make([]string, 0, len(media))
	for _, att := range media {
		orderedIDs = append(orderedIDs, att.ID)
		if att.Description.Valid {
			descriptions = append(descriptions, att.Description.String)
		} else {
			descriptions = append(descriptions, "")
		}
	}

	_, err := s.store.CreateStatusEdit(ctx, db.CreateStatusEditParams{
		StatusID:                  status.ID,
		AccountID:                 pgtype.Int8{Int64: status.AccountID, Valid: true},
		Text:                      status.Text,
		SpoilerText:               status.SpoilerText,
		OrderedMediaAttachmentIds: orderedIDs,
		MediaDescriptions:         descriptions,
		Sensitive:                 pgtype.Bool{Bool: status.Sensitive, Valid: true},
	})
	return err
}

func (s *UpdateService) syncAttachments(ctx context.Context, statusID int64, accountID int64, existing []db.MediaAttachment, attachments []attachmentPayload) error {
	newByURL := make(map[string]attachmentPayload)
	for _, att := range attachments {
		if att.URL == "" {
			continue
		}
		newByURL[att.URL] = att
	}

	existingByURL := make(map[string]db.MediaAttachment)
	for _, att := range existing {
		if att.RemoteUrl != "" {
			existingByURL[att.RemoteUrl] = att
		}
	}

	for _, att := range existing {
		if att.RemoteUrl == "" {
			continue
		}
		if _, ok := newByURL[att.RemoteUrl]; !ok {
			if err := s.store.DeleteMediaAttachment(ctx, db.DeleteMediaAttachmentParams{
				ID:        att.ID,
				AccountID: pgtype.Int8{Int64: accountID, Valid: true},
			}); err != nil {
				return err
			}
		}
	}

	for _, att := range attachments {
		if att.URL == "" {
			continue
		}
		if existingAtt, ok := existingByURL[att.URL]; ok {
			if att.Description != "" && (!existingAtt.Description.Valid || existingAtt.Description.String != att.Description) {
				if _, err := s.store.UpdateMediaAttachment(ctx, db.UpdateMediaAttachmentParams{
					ID:          existingAtt.ID,
					AccountID:   existingAtt.AccountID,
					Description: pgtype.Text{String: att.Description, Valid: true},
				}); err != nil {
					return err
				}
			}
			continue
		}

		var description pgtype.Text
		if att.Description != "" {
			description = pgtype.Text{String: att.Description, Valid: true}
		}
		var blurhash pgtype.Text
		if att.Blurhash != "" {
			blurhash = pgtype.Text{String: att.Blurhash, Valid: true}
		}
		var contentType pgtype.Text
		if att.MediaType != "" {
			contentType = pgtype.Text{String: att.MediaType, Valid: true}
		}
		var thumb pgtype.Text
		if att.PreviewURL != "" {
			thumb = pgtype.Text{String: att.PreviewURL, Valid: true}
		}

		if _, err := s.store.CreateRemoteMediaAttachment(ctx, db.CreateRemoteMediaAttachmentParams{
			AccountID:          pgtype.Int8{Int64: accountID, Valid: true},
			StatusID:           pgtype.Int8{Int64: statusID, Valid: true},
			RemoteUrl:          att.URL,
			ThumbnailRemoteUrl: thumb,
			Type:               att.Type,
			Description:        description,
			Blurhash:           blurhash,
			FileContentType:    contentType,
			FileMeta:           att.FileMeta,
			Processing:         pgtype.Int4{Int32: 2, Valid: true},
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *UpdateService) syncTags(ctx context.Context, statusID int64, tags []string) error {
	if err := s.store.ClearStatusTags(ctx, statusID); err != nil {
		return err
	}
	if len(tags) == 0 {
		return nil
	}
	return s.helper.saveTags(ctx, statusID, tags)
}

func (s *UpdateService) syncMentions(ctx context.Context, authorID, statusID int64, recipients []string, tags []any) error {
	mentionRefs := extractMentionRefs(tags)
	mentionRefs = append(mentionRefs, extractRecipientMentions(recipients, s.localDomain)...)

	accounts, err := s.helper.resolveMentionAccounts(ctx, mentionRefs)
	if err != nil {
		return err
	}

	targetIDs := make([]int64, 0, len(accounts))
	seen := make(map[int64]bool)
	for _, acc := range accounts {
		if acc.ID == authorID {
			continue
		}
		if seen[acc.ID] {
			continue
		}
		seen[acc.ID] = true
		targetIDs = append(targetIDs, acc.ID)
	}

	existing, err := s.store.GetMentionsByStatusID(ctx, statusID)
	if err != nil {
		return err
	}
	existingIDs := make(map[int64]bool)
	for _, mention := range existing {
		existingIDs[mention.AccountID] = true
	}

	newIDs := make([]int64, 0, len(targetIDs))
	for _, id := range targetIDs {
		if !existingIDs[id] {
			newIDs = append(newIDs, id)
		}
		delete(existingIDs, id)
	}

	if len(existingIDs) > 0 {
		var removed []int64
		for id := range existingIDs {
			removed = append(removed, id)
		}
		if err := s.store.RemoveMentions(ctx, db.RemoveMentionsParams{
			StatusID: statusID,
			Column2:  removed,
		}); err != nil {
			return err
		}
	}

	if len(newIDs) == 0 {
		return nil
	}

	mutedBy := make(map[int64]bool)
	blockedBy := make(map[int64]bool)

	if mutes, err := s.store.GetMutesFromAccounts(ctx, db.GetMutesFromAccountsParams{
		TargetAccountID: authorID,
		AccountIds:      newIDs,
	}); err == nil {
		for _, m := range mutes {
			mutedBy[m.AccountID] = true
		}
	}
	if blocks, err := s.store.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
		TargetAccountID: authorID,
		AccountIds:      newIDs,
	}); err == nil {
		for _, b := range blocks {
			blockedBy[b.AccountID] = true
		}
	}

	mentionSilent := make([]bool, 0, len(newIDs))
	for _, id := range newIDs {
		mentionSilent = append(mentionSilent, mutedBy[id] || blockedBy[id])
	}

	if err := s.store.CreateMentions(ctx, db.CreateMentionsParams{
		StatusID: statusID,
		Column2:  newIDs,
		Column3:  mentionSilent,
	}); err != nil {
		return err
	}

	notifs, err := s.store.CreateNotifications(ctx, db.CreateNotificationsParams{
		Column1:       newIDs,
		FromAccountID: authorID,
		ActivityType:  "Status",
		ActivityID:    statusID,
		Type:          pgtype.Text{String: "mention", Valid: true},
	})
	if err != nil {
		return err
	}
	publishPushNotifications(ctx, s.pushPublisher, notifs)

	return nil
}

type noteUpdate struct {
	notePayload
	HasContent     bool
	HasSummary     bool
	HasSensitive   bool
	HasTags        bool
	HasAttachments bool
	HasTo          bool
	HasCc          bool
	HasContentMap  bool
	HasLanguage    bool
}

func parseUpdateNote(value any) (*noteUpdate, error) {
	switch note := value.(type) {
	case activitypub.Object:
		return &noteUpdate{
			notePayload: notePayload{
				ID:           note.ID,
				Type:         note.Type,
				Content:      note.Content,
				Summary:      note.Summary,
				Sensitive:    note.Sensitive,
				AttributedTo: note.AttributedTo,
				To:           note.To,
				Cc:           note.Cc,
				InReplyTo:    note.InReplyTo,
				Published:    note.Published,
				Updated:      note.Updated,
				URL:          note.URL,
				Tags:         tagsToAny(note.Tag),
				Attachments:  note.Attachment,
			},
			HasContent:     note.Content != "",
			HasSummary:     note.Summary != "",
			HasSensitive:   true,
			HasTags:        len(note.Tag) > 0,
			HasAttachments: len(note.Attachment) > 0,
			HasTo:          len(note.To) > 0,
			HasCc:          len(note.Cc) > 0,
		}, nil
	case *activitypub.Object:
		if note == nil {
			return nil, errors.New("missing note object")
		}
		return &noteUpdate{
			notePayload: notePayload{
				ID:           note.ID,
				Type:         note.Type,
				Content:      note.Content,
				Summary:      note.Summary,
				Sensitive:    note.Sensitive,
				AttributedTo: note.AttributedTo,
				To:           note.To,
				Cc:           note.Cc,
				InReplyTo:    note.InReplyTo,
				Published:    note.Published,
				Updated:      note.Updated,
				URL:          note.URL,
				Tags:         tagsToAny(note.Tag),
				Attachments:  note.Attachment,
			},
			HasContent:     note.Content != "",
			HasSummary:     note.Summary != "",
			HasSensitive:   true,
			HasTags:        len(note.Tag) > 0,
			HasAttachments: len(note.Attachment) > 0,
			HasTo:          len(note.To) > 0,
			HasCc:          len(note.Cc) > 0,
		}, nil
	case map[string]any:
		payload := &noteUpdate{
			notePayload: notePayload{
				ID:           stringValue(note["id"]),
				Type:         coerceActivityType(note["type"]),
				Content:      stringValue(note["content"]),
				Summary:      stringValue(note["summary"]),
				Sensitive:    boolValue(note["sensitive"]),
				AttributedTo: note["attributedTo"],
				To:           stringSlice(note["to"]),
				Cc:           stringSlice(note["cc"]),
				InReplyTo:    note["inReplyTo"],
				Published:    stringValue(note["published"]),
				Updated:      stringValue(note["updated"]),
				URL:          note["url"],
				Tags:         sliceValue(note["tag"]),
				Attachments:  sliceValue(note["attachment"]),
			},
			HasContent:     hasKey(note, "content"),
			HasSummary:     hasKey(note, "summary"),
			HasSensitive:   hasKey(note, "sensitive"),
			HasTags:        hasKey(note, "tag"),
			HasAttachments: hasKey(note, "attachment"),
			HasTo:          hasKey(note, "to"),
			HasCc:          hasKey(note, "cc"),
			HasContentMap:  hasKey(note, "contentMap"),
			HasLanguage:    hasKey(note, "language"),
		}
		if contentMap, ok := note["contentMap"].(map[string]any); ok {
			payload.ContentMap = contentMap
		}
		if lang, ok := note["language"].(string); ok {
			payload.Language = lang
		}
		return payload, nil
	default:
		return nil, errors.New("missing note object")
	}
}

type actorPayload struct {
	ID                           string
	Type                         string
	Name                         string
	Summary                      string
	Content                      string
	PreferredUsername            string
	Inbox                        string
	Outbox                       string
	Followers                    string
	Following                    string
	Featured                     string
	SharedInbox                  string
	ManuallyApprovesFollowers    bool
	HasManuallyApprovesFollowers bool
	Discoverable                 *bool
	Indexable                    *bool
	URL                          any
	Icon                         any
	Image                        any
	PublicKey                    string
	HasPublicKey                 bool
	Attachment                   []any
	HasAttachment                bool
	HasSummary                   bool
	HasContent                   bool
}

func parseUpdateActor(value any) (actorPayload, bool, error) {
	switch actor := value.(type) {
	case activitypub.Actor:
		return actorPayloadFromActor(&actor), true, nil
	case *activitypub.Actor:
		if actor == nil {
			return actorPayload{}, false, errors.New("missing actor object")
		}
		return actorPayloadFromActor(actor), true, nil
	case map[string]any:
		return actorPayloadFromMap(actor), true, nil
	case string:
		return actorPayload{ID: actor}, true, nil
	default:
		return actorPayload{}, false, nil
	}
}

func actorPayloadFromActor(actor *activitypub.Actor) actorPayload {
	payload := actorPayload{
		ID:                           actor.ID,
		Type:                         actor.Type,
		Name:                         actor.Name,
		Summary:                      actor.Summary,
		Content:                      actor.Content,
		PreferredUsername:            actor.PreferredUsername,
		Inbox:                        actor.Inbox,
		Outbox:                       actor.Outbox,
		Followers:                    actor.Followers,
		Following:                    actor.Following,
		Featured:                     actor.Featured,
		ManuallyApprovesFollowers:    actor.ManuallyApprovesFollowers,
		HasManuallyApprovesFollowers: true,
		URL:                          actor.URL,
		HasSummary:                   actor.Summary != "",
		HasContent:                   actor.Content != "",
	}

	if actor.Endpoints != nil {
		payload.SharedInbox = actor.Endpoints.SharedInbox
	}
	if actor.Discoverable != nil {
		payload.Discoverable = actor.Discoverable
	}
	if actor.Indexable != nil {
		payload.Indexable = actor.Indexable
	}
	if actor.Icon != nil {
		payload.Icon = actor.Icon
	}
	if actor.Image != nil {
		payload.Image = actor.Image
	}
	if actor.PublicKey != nil && actor.PublicKey.PublicKeyPem != "" {
		payload.PublicKey = actor.PublicKey.PublicKeyPem
		payload.HasPublicKey = true
	}
	if actor.Attachment != nil {
		payload.Attachment = actor.Attachment
		payload.HasAttachment = true
	}

	return payload
}

func actorPayloadFromMap(actor map[string]any) actorPayload {
	payload := actorPayload{
		ID:                stringValue(actor["id"]),
		Type:              coerceActivityType(actor["type"]),
		Name:              stringValue(actor["name"]),
		Summary:           stringValue(actor["summary"]),
		Content:           stringValue(actor["content"]),
		PreferredUsername: stringValue(actor["preferredUsername"]),
		Inbox:             stringValue(actor["inbox"]),
		Outbox:            stringValue(actor["outbox"]),
		Followers:         stringValue(actor["followers"]),
		Following:         stringValue(actor["following"]),
		Featured:          stringValue(actor["featured"]),
		URL:               actor["url"],
		Icon:              actor["icon"],
		Image:             actor["image"],
		HasSummary:        hasKey(actor, "summary"),
		HasContent:        hasKey(actor, "content"),
	}

	if v, ok := actor["manuallyApprovesFollowers"].(bool); ok {
		payload.ManuallyApprovesFollowers = v
		payload.HasManuallyApprovesFollowers = true
	}
	if v, ok := actor["discoverable"].(bool); ok {
		payload.Discoverable = &v
	}
	if v, ok := actor["indexable"].(bool); ok {
		payload.Indexable = &v
	}
	if endpoints, ok := actor["endpoints"].(map[string]any); ok {
		payload.SharedInbox = stringValue(endpoints["sharedInbox"])
	}
	if publicKey, ok := actor["publicKey"].(map[string]any); ok {
		if pk := stringValue(publicKey["publicKeyPem"]); pk != "" {
			payload.PublicKey = pk
			payload.HasPublicKey = true
		}
	}
	if attachment, ok := actor["attachment"]; ok {
		payload.Attachment = sliceValue(attachment)
		payload.HasAttachment = true
	}

	return payload
}

type actorField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func parseActorFields(attachments []any) []actorField {
	fields := make([]actorField, 0)
	for _, raw := range attachments {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := coerceActivityType(item["type"])
		if !strings.EqualFold(typ, "PropertyValue") {
			continue
		}
		name := stringValue(item["name"])
		value := stringValue(item["value"])
		if name == "" && value == "" {
			continue
		}
		fields = append(fields, actorField{Name: name, Value: value})
	}
	return fields
}

func imageURL(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case activitypub.Image:
		return v.URL
	case *activitypub.Image:
		if v != nil {
			return v.URL
		}
	case map[string]any, []any:
		return extractURL(value)
	}
	return ""
}

func extractObjectType(value any) string {
	switch obj := value.(type) {
	case activitypub.Object:
		return obj.Type
	case *activitypub.Object:
		if obj != nil {
			return obj.Type
		}
	case activitypub.Actor:
		return obj.Type
	case *activitypub.Actor:
		if obj != nil {
			return obj.Type
		}
	case map[string]any:
		return coerceActivityType(obj["type"])
	}
	return ""
}

func isActorType(value string) bool {
	switch {
	case strings.EqualFold(value, activitypub.TypePerson):
		return true
	case strings.EqualFold(value, activitypub.TypeApplication):
		return true
	case strings.EqualFold(value, activitypub.TypeService):
		return true
	case strings.EqualFold(value, activitypub.TypeGroup):
		return true
	case strings.EqualFold(value, activitypub.TypeOrganization):
		return true
	default:
		return false
	}
}

func hasKey(values map[string]any, key string) bool {
	_, ok := values[key]
	return ok
}
