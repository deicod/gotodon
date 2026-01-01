package federation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/conversations"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// CreateStore describes the persistence needed for incoming Create activities.
type CreateStore interface {
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error)
	CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error)
	CreateAccountStats(ctx context.Context, accountID int64) (db.AccountStat, error)
	GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error)
	CreateStatus(ctx context.Context, arg db.CreateStatusParams) (db.Status, error)
	CreateStatusStats(ctx context.Context, statusID int64) (db.StatusStat, error)
	UpdateStatusURL(ctx context.Context, arg db.UpdateStatusURLParams) error
	UpdateStatusTimestamps(ctx context.Context, arg db.UpdateStatusTimestampsParams) error
	IncrementStatusReplies(ctx context.Context, statusID int64) error
	CreateMentions(ctx context.Context, arg db.CreateMentionsParams) error
	CreateNotifications(ctx context.Context, arg db.CreateNotificationsParams) ([]db.Notification, error)
	GetMutesFromAccounts(ctx context.Context, arg db.GetMutesFromAccountsParams) ([]db.Mute, error)
	GetBlocksFromAccounts(ctx context.Context, arg db.GetBlocksFromAccountsParams) ([]db.Block, error)
	UpsertTags(ctx context.Context, tags []string) error
	GetTagsByNames(ctx context.Context, tags []string) ([]db.Tag, error)
	AddStatusTags(ctx context.Context, arg db.AddStatusTagsParams) error
	CreateRemoteMediaAttachment(ctx context.Context, arg db.CreateRemoteMediaAttachmentParams) (db.MediaAttachment, error)
	conversations.ConversationStore
}

// StatusFetcher retrieves remote statuses referenced by incoming activities.
type StatusFetcher interface {
	FetchStatus(ctx context.Context, uri string) (db.Status, error)
}

type createOptions struct {
	skipMentions bool
}

// CreateService processes incoming Create Note activities.
type CreateService struct {
	store         CreateStore
	fetcher       activitypub.ActorFetcher
	localDomain   string
	statusFetcher StatusFetcher
}

// NewCreateService creates a Create service for incoming notes.
func NewCreateService(store CreateStore, localDomain string, fetcher activitypub.ActorFetcher) *CreateService {
	return &CreateService{
		store:       store,
		fetcher:     fetcher,
		localDomain: localDomain,
	}
}

// SetStatusFetcher configures the optional status fetcher for missing replies.
func (s *CreateService) SetStatusFetcher(fetcher StatusFetcher) {
	s.statusFetcher = fetcher
}

// HandleCreate processes a Create activity with a Note object.
func (s *CreateService) HandleCreate(ctx context.Context, activity *activitypub.Activity) error {
	return s.handleCreate(ctx, activity, createOptions{})
}

func (s *CreateService) handleCreate(ctx context.Context, activity *activitypub.Activity, opts createOptions) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid create activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing create actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}

	note, err := parseCreateNote(activity.ActivityObject)
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

	if _, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: noteURI, Valid: true}); err == nil {
		return nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	if s.fetcher == nil {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Actor fetcher unavailable"}
	}
	actor, err := s.fetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}

	author, err := s.ensureActorAccount(ctx, actorURI, actor)
	if err != nil {
		return err
	}
	if author.SuspendedAt.Valid {
		return &apihandler.InboxError{Status: httpStatusForbidden, Message: "Actor is suspended"}
	}

	content := note.Content
	if content == "" && note.ContentMap != nil {
		if lang, text := firstContentMapEntry(note.ContentMap); text != "" {
			content = text
			note.Language = lang
		}
	}

	attachments := extractAttachments(note.Attachments)
	if content == "" && len(attachments) == 0 {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note content"}
	}

	var inReplyToID, inReplyToAccountID pgtype.Int8
	var parentStatus *db.Status
	inReplyToURI := extractObjectURI(note.InReplyTo)
	if inReplyToURI != "" {
		parent, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: inReplyToURI, Valid: true})
		parentFound := err == nil
		if errors.Is(err, pgx.ErrNoRows) && s.statusFetcher != nil && !isLocalURI(inReplyToURI, s.localDomain) {
			if _, fetchErr := s.statusFetcher.FetchStatus(ctx, inReplyToURI); fetchErr == nil {
				parent, err = s.store.GetStatusByURI(ctx, pgtype.Text{String: inReplyToURI, Valid: true})
				parentFound = err == nil
			}
		}

		if parentFound {
			inReplyToID = pgtype.Int8{Int64: parent.ID, Valid: true}
			inReplyToAccountID = pgtype.Int8{Int64: parent.AccountID, Valid: true}
			parentStatus = &parent
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}

	to := mergeRecipients(note.To, activity.To)
	cc := mergeRecipients(note.Cc, activity.Cc)
	visibility := visibilityFromRecipients(to, cc, actorURI, actor)

	var language pgtype.Text
	if note.Language != "" {
		language = pgtype.Text{String: note.Language, Valid: true}
	}

	status, err := s.store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:                pgtype.Text{String: noteURI, Valid: true},
		Text:               content,
		AccountID:          author.ID,
		InReplyToID:        inReplyToID,
		InReplyToAccountID: inReplyToAccountID,
		Sensitive:          note.Sensitive,
		Visibility:         visibility,
		SpoilerText:        note.Summary,
		Language:           language,
		Local:              pgtype.Bool{Bool: false, Valid: true},
		Reply:              inReplyToURI != "",
	})
	if err != nil {
		return err
	}

	noteURL := extractURL(note.URL)
	if noteURL == "" {
		noteURL = noteURI
	}
	if noteURL != "" {
		if err := s.store.UpdateStatusURL(ctx, db.UpdateStatusURLParams{
			ID:  status.ID,
			Url: pgtype.Text{String: noteURL, Valid: true},
		}); err != nil {
			return err
		}
	}

	if published, updated, ok := parseNoteTimestamps(note.Published, note.Updated); ok {
		_ = s.store.UpdateStatusTimestamps(ctx, db.UpdateStatusTimestampsParams{
			ID:        status.ID,
			CreatedAt: published,
			UpdatedAt: updated,
		})
	}

	if _, err := s.store.CreateStatusStats(ctx, status.ID); err != nil && !isUniqueViolation(err) {
		return err
	}

	if inReplyToID.Valid {
		_ = s.store.IncrementStatusReplies(ctx, inReplyToID.Int64)
	}

	if err := s.attachRemoteMedia(ctx, author.ID, status.ID, attachments); err != nil {
		return err
	}

	tags := extractHashtags(note.Tags)
	if err := s.saveTags(ctx, status.ID, tags); err != nil {
		return err
	}

	recipients := mergeRecipients(to, cc)
	var activeMentions []db.Account
	if !opts.skipMentions {
		var err error
		activeMentions, err = s.processMentions(ctx, author.ID, status.ID, recipients, note.Tags)
		if err != nil {
			return err
		}
	}

	if visibility == httpVisibilityDirect {
		convParent := parentStatus
		if convParent != nil && convParent.Visibility != httpVisibilityDirect && !convParent.ConversationID.Valid {
			convParent = nil
		}
		updatedStatus, err := conversations.EnsureConversationForStatus(ctx, s.store, status, convParent)
		if err != nil {
			return err
		}
		status = updatedStatus
		if !opts.skipMentions {
			if err := conversations.AddStatusToAccountConversations(ctx, s.store, status, author, activeMentions); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *CreateService) ensureActorAccount(ctx context.Context, actorURI string, actor *activitypub.Actor) (db.Account, error) {
	account, err := s.store.GetAccountByURI(ctx, actorURI)
	if err == nil {
		return account, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, err
	}

	if isLocalURI(actorURI, s.localDomain) {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusNotFound, Message: "Actor not found"}
	}
	if actor == nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	if actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor missing public key"}
	}

	username := actorUsername(actor, actorURI)
	if username == "" {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor missing username"}
	}

	domain := activitypub.ExtractDomain(actorURI)
	if domain == "" {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor domain invalid"}
	}

	displayName := actor.Name
	if displayName == "" {
		displayName = username
	}
	note := actor.Summary
	if note == "" {
		note = actor.Content
	}

	var discoverable pgtype.Bool
	if actor.Discoverable != nil {
		discoverable = pgtype.Bool{Bool: *actor.Discoverable, Valid: true}
	}

	var actorType pgtype.Text
	if actor.Type != "" {
		actorType = pgtype.Text{String: actor.Type, Valid: true}
	}

	indexable := false
	if actor.Indexable != nil {
		indexable = *actor.Indexable
	}

	account, err = s.store.CreateAccount(ctx, db.CreateAccountParams{
		Username:     username,
		Domain:       pgtype.Text{String: domain, Valid: true},
		PrivateKey:   pgtype.Text{Valid: false},
		PublicKey:    actor.PublicKey.PublicKeyPem,
		DisplayName:  displayName,
		Note:         note,
		Uri:          actorURI,
		Url:          actorURLValue(actor),
		Locked:       actor.ManuallyApprovesFollowers,
		Discoverable: discoverable,
		Indexable:    indexable,
		ActorType:    actorType,
		Protocol:     1,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return s.store.GetAccountByURI(ctx, actorURI)
		}
		return db.Account{}, err
	}

	if _, err := s.store.CreateAccountStats(ctx, account.ID); err != nil && !isUniqueViolation(err) {
		return db.Account{}, err
	}

	return account, nil
}

func (s *CreateService) attachRemoteMedia(ctx context.Context, accountID, statusID int64, attachments []attachmentPayload) error {
	if len(attachments) == 0 {
		return nil
	}

	for _, att := range attachments {
		if att.URL == "" {
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
		var thumb pgtype.Text
		if att.PreviewURL != "" {
			thumb = pgtype.Text{String: att.PreviewURL, Valid: true}
		}
		var contentType pgtype.Text
		if att.MediaType != "" {
			contentType = pgtype.Text{String: att.MediaType, Valid: true}
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

func (s *CreateService) saveTags(ctx context.Context, statusID int64, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	if err := s.store.UpsertTags(ctx, tags); err != nil {
		return err
	}

	dbTags, err := s.store.GetTagsByNames(ctx, tags)
	if err != nil {
		return err
	}

	tagIDs := make([]int64, len(dbTags))
	for i, t := range dbTags {
		tagIDs[i] = t.ID
	}
	if err := s.store.AddStatusTags(ctx, db.AddStatusTagsParams{
		StatusID: statusID,
		Column2:  tagIDs,
	}); err != nil {
		return err
	}

	return nil
}

func (s *CreateService) processMentions(ctx context.Context, authorID, statusID int64, recipients []string, tags []any) ([]db.Account, error) {
	mentionRefs := extractMentionRefs(tags)
	mentionRefs = append(mentionRefs, extractRecipientMentions(recipients, s.localDomain)...)

	accounts, err := s.resolveMentionAccounts(ctx, mentionRefs)
	if err != nil {
		return nil, err
	}

	accountMap := make(map[int64]db.Account)
	targetIDs := make([]int64, 0, len(accounts))
	for _, acc := range accounts {
		if acc.ID == authorID {
			continue
		}
		if _, exists := accountMap[acc.ID]; exists {
			continue
		}
		accountMap[acc.ID] = acc
		targetIDs = append(targetIDs, acc.ID)
	}

	if len(targetIDs) == 0 {
		return nil, nil
	}

	mutedBy := make(map[int64]bool)
	blockedBy := make(map[int64]bool)

	if mutes, err := s.store.GetMutesFromAccounts(ctx, db.GetMutesFromAccountsParams{
		TargetAccountID: authorID,
		AccountIds:      targetIDs,
	}); err == nil {
		for _, m := range mutes {
			mutedBy[m.AccountID] = true
		}
	}

	if blocks, err := s.store.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
		TargetAccountID: authorID,
		AccountIds:      targetIDs,
	}); err == nil {
		for _, b := range blocks {
			blockedBy[b.AccountID] = true
		}
	}

	mentionSilent := make([]bool, 0, len(targetIDs))
	activeAccounts := make([]db.Account, 0, len(targetIDs))
	for _, tid := range targetIDs {
		silent := mutedBy[tid] || blockedBy[tid]
		mentionSilent = append(mentionSilent, silent)
		if !silent {
			if acc, ok := accountMap[tid]; ok {
				activeAccounts = append(activeAccounts, acc)
			}
		}
	}

	if err := s.store.CreateMentions(ctx, db.CreateMentionsParams{
		StatusID: statusID,
		Column2:  targetIDs,
		Column3:  mentionSilent,
	}); err != nil {
		return nil, err
	}

	_, err = s.store.CreateNotifications(ctx, db.CreateNotificationsParams{
		Column1:       targetIDs,
		FromAccountID: authorID,
		ActivityType:  "Status",
		ActivityID:    statusID,
		Type:          pgtype.Text{String: "mention", Valid: true},
	})
	if err != nil {
		return nil, err
	}

	return activeAccounts, nil
}

func (s *CreateService) resolveMentionAccounts(ctx context.Context, mentions []mentionRef) ([]db.Account, error) {
	if len(mentions) == 0 {
		return nil, nil
	}

	var accounts []db.Account
	for _, ref := range mentions {
		account, ok, err := s.resolveMentionAccount(ctx, ref)
		if err != nil {
			return nil, err
		}
		if ok {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (s *CreateService) resolveMentionAccount(ctx context.Context, ref mentionRef) (db.Account, bool, error) {
	if ref.Href != "" {
		account, err := s.store.GetAccountByURI(ctx, ref.Href)
		if err == nil {
			return account, true, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return db.Account{}, false, err
		}
		if isLocalURI(ref.Href, s.localDomain) {
			return db.Account{}, false, nil
		}
		if s.fetcher == nil {
			return db.Account{}, false, nil
		}
		actor, err := s.fetcher.FetchActor(ctx, ref.Href)
		if err != nil {
			return db.Account{}, false, nil
		}
		account, err = s.ensureActorAccount(ctx, ref.Href, actor)
		if err != nil {
			var inboxErr *apihandler.InboxError
			if errors.As(err, &inboxErr) {
				return db.Account{}, false, nil
			}
			return db.Account{}, false, err
		}
		return account, true, nil
	}

	username, domain := parseMentionName(ref.Name)
	if username == "" {
		return db.Account{}, false, nil
	}
	if domain == "" {
		account, err := s.store.GetAccountByUsername(ctx, username)
		if err == nil {
			return account, true, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Account{}, false, nil
		}
		return db.Account{}, false, err
	}

	account, err := s.store.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
		Username: username,
		Domain:   domain,
	})
	if err == nil {
		return account, true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, false, nil
	}
	return db.Account{}, false, err
}

type notePayload struct {
	ID           string
	Type         string
	Content      string
	Summary      string
	Sensitive    bool
	AttributedTo any
	To           []string
	Cc           []string
	InReplyTo    any
	Published    string
	Updated      string
	URL          any
	Tags         []any
	Attachments  []any
	Replies      any
	ContentMap   map[string]any
	Language     string
}

func parseCreateNote(value any) (*notePayload, error) {
	switch note := value.(type) {
	case activitypub.Object:
		return &notePayload{
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
			Replies:      note.Replies,
		}, nil
	case *activitypub.Object:
		if note == nil {
			return nil, errors.New("missing note object")
		}
		return &notePayload{
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
			Replies:      note.Replies,
		}, nil
	case map[string]any:
		payload := &notePayload{
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
			Replies:      note["replies"],
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

func tagsToAny(tags []activitypub.Tag) []any {
	if len(tags) == 0 {
		return nil
	}
	out := make([]any, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tag)
	}
	return out
}

func firstContentMapEntry(contentMap map[string]any) (string, string) {
	for lang, value := range contentMap {
		if s, ok := value.(string); ok {
			return lang, s
		}
	}
	return "", ""
}

func parseNoteTimestamps(published, updated string) (pgtype.Timestamptz, pgtype.Timestamptz, bool) {
	createdAt, createdOK := parseTimestamp(published)
	updatedAt, updatedOK := parseTimestamp(updated)
	if !createdOK && !updatedOK {
		return pgtype.Timestamptz{}, pgtype.Timestamptz{}, false
	}
	if !createdOK && updatedOK {
		createdAt = updatedAt
		createdOK = true
	}
	if createdOK && !updatedOK {
		updatedAt = createdAt
	}
	return createdAt, updatedAt, true
}

func parseTimestamp(value string) (pgtype.Timestamptz, bool) {
	if value == "" {
		return pgtype.Timestamptz{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return pgtype.Timestamptz{}, false
		}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, true
}

func extractAttributedActor(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if id, ok := v["id"].(string); ok {
			return id
		}
	case []any:
		for _, item := range v {
			if id := extractAttributedActor(item); id != "" {
				return id
			}
		}
	}
	return ""
}

func mergeRecipients(sets ...[]string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, list := range sets {
		for _, entry := range list {
			if entry == "" {
				continue
			}
			if seen[entry] {
				continue
			}
			seen[entry] = true
			result = append(result, entry)
		}
	}
	return result
}

func visibilityFromRecipients(to, cc []string, actorURI string, actor *activitypub.Actor) int32 {
	if containsRecipient(to, activitypub.PublicAudience) {
		return httpVisibilityPublic
	}
	if containsRecipient(cc, activitypub.PublicAudience) {
		return httpVisibilityUnlisted
	}

	followers := ""
	if actor != nil && actor.Followers != "" {
		followers = actor.Followers
	} else if actorURI != "" {
		followers = actorURI + "/followers"
	}

	if followers != "" && (containsRecipient(to, followers) || containsRecipient(cc, followers)) {
		return httpVisibilityPrivate
	}

	return httpVisibilityDirect
}

func containsRecipient(recipients []string, target string) bool {
	for _, r := range recipients {
		if r == target {
			return true
		}
	}
	return false
}

type mentionRef struct {
	Href string
	Name string
}

func extractMentionRefs(tags []any) []mentionRef {
	var refs []mentionRef
	for _, raw := range tags {
		tagType, href, name := extractTagFields(raw)
		if !strings.EqualFold(tagType, "Mention") {
			continue
		}
		if href == "" && name == "" {
			continue
		}
		refs = append(refs, mentionRef{Href: href, Name: name})
	}
	return refs
}

func extractRecipientMentions(recipients []string, localDomain string) []mentionRef {
	var refs []mentionRef
	for _, recipient := range recipients {
		if !isLocalURI(recipient, localDomain) {
			continue
		}
		refs = append(refs, mentionRef{Href: recipient})
	}
	return refs
}

func parseMentionName(name string) (string, string) {
	if name == "" {
		return "", ""
	}
	value := strings.TrimPrefix(name, "@")
	parts := strings.SplitN(value, "@", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

type attachmentPayload struct {
	URL         string
	PreviewURL  string
	Type        int32
	Description string
	Blurhash    string
	MediaType   string
	FileMeta    []byte
}

func extractAttachments(items []any) []attachmentPayload {
	var attachments []attachmentPayload
	for _, raw := range items {
		payload, ok := parseAttachment(raw)
		if !ok {
			continue
		}
		if payload.URL == "" {
			continue
		}
		attachments = append(attachments, payload)
	}
	return attachments
}

func parseAttachment(value any) (attachmentPayload, bool) {
	switch item := value.(type) {
	case map[string]any:
		attachmentType := coerceActivityType(item["type"])
		if attachmentType == "" {
			return attachmentPayload{}, false
		}
		url := extractURL(item["url"])
		if url == "" {
			return attachmentPayload{}, false
		}
		preview := extractURL(item["previewUrl"])
		if preview == "" {
			preview = extractURL(item["thumbnailUrl"])
		}
		mediaType := stringValue(item["mediaType"])
		description := stringValue(item["name"])
		if description == "" {
			description = stringValue(item["summary"])
		}
		blurhash := stringValue(item["blurhash"])
		return attachmentPayload{
			URL:         url,
			PreviewURL:  preview,
			Type:        mapAttachmentType(attachmentType, mediaType),
			Description: description,
			Blurhash:    blurhash,
			MediaType:   mediaType,
		}, true
	case activitypub.Object:
		return attachmentPayload{
			URL:         extractURL(item.URL),
			Type:        mapAttachmentType(item.Type, ""),
			Description: item.Name,
		}, item.URL != ""
	default:
		return attachmentPayload{}, false
	}
}

func mapAttachmentType(kind, mediaType string) int32 {
	switch {
	case strings.EqualFold(kind, activitypub.TypeImage):
		return 0
	case strings.EqualFold(kind, activitypub.TypeVideo):
		return 2
	case strings.EqualFold(kind, activitypub.TypeAudio):
		return 4
	default:
		if strings.HasPrefix(strings.ToLower(mediaType), "video/") {
			return 2
		}
		if strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
			return 4
		}
		return 0
	}
}

func extractHashtags(tags []any) []string {
	seen := make(map[string]bool)
	var results []string
	for _, raw := range tags {
		tagType, _, name := extractTagFields(raw)
		if !strings.EqualFold(tagType, "Hashtag") {
			continue
		}
		clean := strings.TrimPrefix(name, "#")
		clean = strings.ToLower(strings.TrimSpace(clean))
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		results = append(results, clean)
	}
	return results
}

func extractTagFields(value any) (string, string, string) {
	switch tag := value.(type) {
	case activitypub.Tag:
		return tag.Type, tag.Href, tag.Name
	case map[string]any:
		return coerceActivityType(tag["type"]), stringValue(tag["href"]), stringValue(tag["name"])
	default:
		return "", "", ""
	}
}

func extractURL(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if href, ok := v["href"].(string); ok {
			return href
		}
		if url, ok := v["url"].(string); ok {
			return url
		}
	case []any:
		for _, item := range v {
			if url := extractURL(item); url != "" {
				return url
			}
		}
	}
	return ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func boolValue(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return false
}

func stringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}

func sliceValue(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

const (
	httpVisibilityPublic   = 0
	httpVisibilityUnlisted = 1
	httpVisibilityPrivate  = 2
	httpVisibilityDirect   = 3
)
