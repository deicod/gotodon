package federation

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	outgoingVisibilityPublic   = 0
	outgoingVisibilityUnlisted = 1
	outgoingVisibilityPrivate  = 2
	outgoingVisibilityDirect   = 3
)

// DeliveryEnqueuer is implemented by delivery queues.
type DeliveryEnqueuer interface {
	Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error
}

// OutgoingDeliverer describes outgoing federation delivery actions.
type OutgoingDeliverer interface {
	DeliverStatusCreate(ctx context.Context, statusID int64) error
	DeliverStatusUpdate(ctx context.Context, statusID int64) error
	DeliverStatusDelete(ctx context.Context, status db.Status, mentions []db.Account) error
	DeliverFollow(ctx context.Context, followerID, targetID, followID int64) error
	DeliverUndoFollow(ctx context.Context, followerID, targetID, followID int64) error
	DeliverFollowResponse(ctx context.Context, request db.FollowRequest, accepted bool) error
	DeliverLike(ctx context.Context, favourite db.Favourite) error
	DeliverUndoLike(ctx context.Context, favourite db.Favourite) error
	DeliverAnnounce(ctx context.Context, reblogStatusID int64) error
	DeliverUndoAnnounce(ctx context.Context, reblog db.Status) error
}

// OutgoingStore provides data required to build outgoing activities.
type OutgoingStore interface {
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error)
	GetTagsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetTagsByStatusIDsRow, error)
	ListMediaAttachmentsByStatus(ctx context.Context, statusID pgtype.Int8) ([]db.MediaAttachment, error)
	GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error)
}

// OutgoingService delivers ActivityPub activities for local actions.
type OutgoingService struct {
	store       OutgoingStore
	localDomain string
	resolver    *InboxResolver
	enqueuer    DeliveryEnqueuer
	now         func() time.Time
}

// NewOutgoingService creates an outgoing delivery service.
func NewOutgoingService(store OutgoingStore, localDomain string, enqueuer DeliveryEnqueuer) *OutgoingService {
	return &OutgoingService{
		store:       store,
		localDomain: localDomain,
		resolver:    NewInboxResolver(store, localDomain),
		enqueuer:    enqueuer,
		now:         time.Now,
	}
}

// DeliverStatusCreate sends a Create activity for a local status.
func (s *OutgoingService) DeliverStatusCreate(ctx context.Context, statusID int64) error {
	status, author, mentions, tags, attachments, err := s.loadStatusContext(ctx, statusID)
	if err != nil {
		return err
	}
	if !statusIsLocal(status, author, s.localDomain) {
		return nil
	}
	if status.ReblogOfID.Valid {
		return nil
	}

	activity, targets, err := s.buildStatusActivity(ctx, author, status, mentions, tags, attachments, activitypub.TypeCreate)
	if err != nil {
		return err
	}
	localActor, _, err := ensureLocalActor(s.localDomain, author)
	if err != nil {
		return err
	}
	return s.enqueueTargets(localActor, activity, targets)
}

// DeliverStatusUpdate sends an Update activity for an edited local status.
func (s *OutgoingService) DeliverStatusUpdate(ctx context.Context, statusID int64) error {
	status, author, mentions, tags, attachments, err := s.loadStatusContext(ctx, statusID)
	if err != nil {
		return err
	}
	if !statusIsLocal(status, author, s.localDomain) {
		return nil
	}
	if status.ReblogOfID.Valid {
		return nil
	}

	activity, targets, err := s.buildStatusActivity(ctx, author, status, mentions, tags, attachments, activitypub.TypeUpdate)
	if err != nil {
		return err
	}
	localActor, _, err := ensureLocalActor(s.localDomain, author)
	if err != nil {
		return err
	}
	return s.enqueueTargets(localActor, activity, targets)
}

// DeliverStatusDelete sends a Delete activity for a local status.
func (s *OutgoingService) DeliverStatusDelete(ctx context.Context, status db.Status, mentions []db.Account) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	author, err := s.store.GetAccountByID(ctx, status.AccountID)
	if err != nil {
		return err
	}
	if !statusIsLocal(status, author, s.localDomain) {
		return nil
	}

	if status.ReblogOfID.Valid {
		return s.DeliverUndoAnnounce(ctx, status)
	}

	noteID := statusURI(s.localDomain, author, status)
	if noteID == "" {
		return errors.New("status uri missing")
	}

	to, cc := statusRecipients(s.localDomain, author, status.Visibility, mentions)
	localActor, actor, err := ensureLocalActor(s.localDomain, author)
	if err != nil {
		return err
	}
	activity := &activitypub.Activity{
		Context:        activitypub.MastodonContextFull(),
		Type:           activitypub.TypeDelete,
		ID:             statusActivityID(noteID, activitypub.TypeDelete, status.UpdatedAt),
		Actor:          actor,
		Published:      formatTimestamp(s.now().UTC()),
		To:             to,
		Cc:             cc,
		ActivityObject: activitypub.Object{ID: noteID, Type: activitypub.TypeNote},
	}

	targets, err := s.resolveStatusTargets(ctx, author, status.Visibility, mentions)
	if err != nil {
		return err
	}
	return s.enqueueTargets(localActor, activity, targets)
}

// DeliverFollow sends a Follow activity to a remote account.
func (s *OutgoingService) DeliverFollow(ctx context.Context, followerID, targetID, followID int64) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	follower, err := s.store.GetAccountByID(ctx, followerID)
	if err != nil {
		return err
	}
	if !accountIsLocal(follower, s.localDomain) {
		return nil
	}
	localActor, actor, err := ensureLocalActor(s.localDomain, follower)
	if err != nil {
		return err
	}

	target, err := s.store.GetAccountByID(ctx, targetID)
	if err != nil {
		return err
	}
	if accountIsLocal(target, s.localDomain) {
		return nil
	}
	inbox := accountInbox(target)
	if inbox == "" {
		return errors.New("target inbox missing")
	}

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeFollow,
		ID:             followActivityID(s.localDomain, followID),
		Actor:          actor,
		Published:      formatTimestamp(s.now().UTC()),
		ActivityObject: target.Uri,
	}

	return s.enqueueTargets(localActor, activity, []string{inbox})
}

// DeliverUndoFollow sends an Undo Follow activity to a remote account.
func (s *OutgoingService) DeliverUndoFollow(ctx context.Context, followerID, targetID, followID int64) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	follower, err := s.store.GetAccountByID(ctx, followerID)
	if err != nil {
		return err
	}
	if !accountIsLocal(follower, s.localDomain) {
		return nil
	}
	localActor, actor, err := ensureLocalActor(s.localDomain, follower)
	if err != nil {
		return err
	}

	target, err := s.store.GetAccountByID(ctx, targetID)
	if err != nil {
		return err
	}
	if accountIsLocal(target, s.localDomain) {
		return nil
	}
	inbox := accountInbox(target)
	if inbox == "" {
		return errors.New("target inbox missing")
	}

	followActivity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		ID:             followActivityID(s.localDomain, followID),
		Actor:          actor,
		ActivityObject: target.Uri,
	}

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeUndo,
		Actor:          actor,
		Published:      formatTimestamp(s.now().UTC()),
		ActivityObject: followActivity,
	}

	return s.enqueueTargets(localActor, activity, []string{inbox})
}

// DeliverFollowResponse sends Accept/Reject for a follow request.
func (s *OutgoingService) DeliverFollowResponse(ctx context.Context, request db.FollowRequest, accepted bool) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	localAccount, err := s.store.GetAccountByID(ctx, request.TargetAccountID)
	if err != nil {
		return err
	}
	if !accountIsLocal(localAccount, s.localDomain) {
		return nil
	}
	localActor, actor, err := ensureLocalActor(s.localDomain, localAccount)
	if err != nil {
		return err
	}

	follower, err := s.store.GetAccountByID(ctx, request.AccountID)
	if err != nil {
		return err
	}
	if accountIsLocal(follower, s.localDomain) {
		return nil
	}
	inbox := accountInbox(follower)
	if inbox == "" {
		return errors.New("follower inbox missing")
	}

	followActivity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		ID:             followReferenceID(s.localDomain, request),
		Actor:          follower.Uri,
		ActivityObject: actor,
	}

	responseType := activitypub.TypeReject
	if accepted {
		responseType = activitypub.TypeAccept
	}

	response := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           responseType,
		Actor:          actor,
		Published:      formatTimestamp(s.now().UTC()),
		ActivityObject: followActivity,
	}

	return s.enqueueTargets(localActor, response, []string{inbox})
}

// DeliverLike sends a Like activity to the status author.
func (s *OutgoingService) DeliverLike(ctx context.Context, favourite db.Favourite) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	actor, err := s.store.GetAccountByID(ctx, favourite.AccountID)
	if err != nil {
		return err
	}
	if !accountIsLocal(actor, s.localDomain) {
		return nil
	}
	localActor, actorURI, err := ensureLocalActor(s.localDomain, actor)
	if err != nil {
		return err
	}

	status, err := s.store.GetStatusByID(ctx, favourite.StatusID)
	if err != nil {
		return err
	}
	author, err := s.store.GetAccountByID(ctx, status.AccountID)
	if err != nil {
		return err
	}
	if accountIsLocal(author, s.localDomain) {
		return nil
	}
	inbox := accountInbox(author)
	if inbox == "" {
		return errors.New("status author inbox missing")
	}

	objectURI := statusObjectURI(s.localDomain, author, status)
	if objectURI == "" {
		return errors.New("status object uri missing")
	}

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeLike,
		ID:             likeActivityID(s.localDomain, favourite.ID),
		Actor:          actorURI,
		Published:      formatTimestamp(s.now().UTC()),
		ActivityObject: objectURI,
	}

	return s.enqueueTargets(localActor, activity, []string{inbox})
}

// DeliverUndoLike sends an Undo Like activity to the status author.
func (s *OutgoingService) DeliverUndoLike(ctx context.Context, favourite db.Favourite) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	actor, err := s.store.GetAccountByID(ctx, favourite.AccountID)
	if err != nil {
		return err
	}
	if !accountIsLocal(actor, s.localDomain) {
		return nil
	}
	localActor, actorURI, err := ensureLocalActor(s.localDomain, actor)
	if err != nil {
		return err
	}

	status, err := s.store.GetStatusByID(ctx, favourite.StatusID)
	if err != nil {
		return err
	}
	author, err := s.store.GetAccountByID(ctx, status.AccountID)
	if err != nil {
		return err
	}
	if accountIsLocal(author, s.localDomain) {
		return nil
	}
	inbox := accountInbox(author)
	if inbox == "" {
		return errors.New("status author inbox missing")
	}

	objectURI := statusObjectURI(s.localDomain, author, status)
	if objectURI == "" {
		return errors.New("status object uri missing")
	}

	likeActivity := &activitypub.Activity{
		Type:           activitypub.TypeLike,
		ID:             likeActivityID(s.localDomain, favourite.ID),
		Actor:          actorURI,
		ActivityObject: objectURI,
	}

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeUndo,
		Actor:          actorURI,
		Published:      formatTimestamp(s.now().UTC()),
		ActivityObject: likeActivity,
	}

	return s.enqueueTargets(localActor, activity, []string{inbox})
}

// DeliverAnnounce sends an Announce activity for a local boost.
func (s *OutgoingService) DeliverAnnounce(ctx context.Context, reblogStatusID int64) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	reblog, err := s.store.GetStatusByID(ctx, reblogStatusID)
	if err != nil {
		return err
	}
	if !reblog.ReblogOfID.Valid {
		return nil
	}

	actor, err := s.store.GetAccountByID(ctx, reblog.AccountID)
	if err != nil {
		return err
	}
	if !statusIsLocal(reblog, actor, s.localDomain) {
		return nil
	}
	localActor, actorURI, err := ensureLocalActor(s.localDomain, actor)
	if err != nil {
		return err
	}

	original, err := s.store.GetStatusByID(ctx, reblog.ReblogOfID.Int64)
	if err != nil {
		return err
	}
	originalAuthor, err := s.store.GetAccountByID(ctx, original.AccountID)
	if err != nil {
		return err
	}

	objectURI := statusObjectURI(s.localDomain, originalAuthor, original)
	if objectURI == "" {
		return errors.New("status object uri missing")
	}

	announceID := statusURI(s.localDomain, actor, reblog)
	if announceID == "" {
		return errors.New("announce uri missing")
	}

	to, cc := statusRecipients(s.localDomain, actor, reblog.Visibility, nil)
	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeAnnounce,
		ID:             announceID,
		Actor:          actorURI,
		Published:      formatTimestamp(s.now().UTC()),
		To:             to,
		Cc:             cc,
		ActivityObject: objectURI,
	}

	targets, err := s.resolveAnnounceTargets(ctx, actor, reblog.Visibility, originalAuthor)
	if err != nil {
		return err
	}
	return s.enqueueTargets(localActor, activity, targets)
}

// DeliverUndoAnnounce sends an Undo Announce activity for a local boost.
func (s *OutgoingService) DeliverUndoAnnounce(ctx context.Context, reblog db.Status) error {
	if s.store == nil {
		return errors.New("outgoing store missing")
	}
	if !reblog.ReblogOfID.Valid {
		return nil
	}

	actor, err := s.store.GetAccountByID(ctx, reblog.AccountID)
	if err != nil {
		return err
	}
	if !statusIsLocal(reblog, actor, s.localDomain) {
		return nil
	}
	localActor, actorURI, err := ensureLocalActor(s.localDomain, actor)
	if err != nil {
		return err
	}

	original, err := s.store.GetStatusByID(ctx, reblog.ReblogOfID.Int64)
	if err != nil {
		return err
	}
	originalAuthor, err := s.store.GetAccountByID(ctx, original.AccountID)
	if err != nil {
		return err
	}

	objectURI := statusObjectURI(s.localDomain, originalAuthor, original)
	if objectURI == "" {
		return errors.New("status object uri missing")
	}

	announceID := statusURI(s.localDomain, actor, reblog)
	if announceID == "" {
		return errors.New("announce uri missing")
	}

	announceActivity := &activitypub.Activity{
		Type:           activitypub.TypeAnnounce,
		ID:             announceID,
		Actor:          actorURI,
		ActivityObject: objectURI,
	}

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeUndo,
		Actor:          actorURI,
		Published:      formatTimestamp(s.now().UTC()),
		ActivityObject: announceActivity,
	}

	targets, err := s.resolveAnnounceTargets(ctx, actor, reblog.Visibility, originalAuthor)
	if err != nil {
		return err
	}
	return s.enqueueTargets(localActor, activity, targets)
}

func (s *OutgoingService) loadStatusContext(ctx context.Context, statusID int64) (db.Status, db.Account, []db.Account, []db.GetTagsByStatusIDsRow, []db.MediaAttachment, error) {
	if s.store == nil {
		return db.Status{}, db.Account{}, nil, nil, nil, errors.New("outgoing store missing")
	}

	status, err := s.store.GetStatusByID(ctx, statusID)
	if err != nil {
		return db.Status{}, db.Account{}, nil, nil, nil, err
	}
	author, err := s.store.GetAccountByID(ctx, status.AccountID)
	if err != nil {
		return db.Status{}, db.Account{}, nil, nil, nil, err
	}

	mentions, err := s.resolveMentions(ctx, statusID)
	if err != nil {
		return db.Status{}, db.Account{}, nil, nil, nil, err
	}

	tags, err := s.store.GetTagsByStatusIDs(ctx, []int64{statusID})
	if err != nil {
		return db.Status{}, db.Account{}, nil, nil, nil, err
	}

	attachments, err := s.store.ListMediaAttachmentsByStatus(ctx, pgtype.Int8{Int64: statusID, Valid: true})
	if err != nil {
		return db.Status{}, db.Account{}, nil, nil, nil, err
	}

	return status, author, mentions, tags, attachments, nil
}

func (s *OutgoingService) resolveMentions(ctx context.Context, statusID int64) ([]db.Account, error) {
	mentions, err := s.store.GetMentionsByStatusID(ctx, statusID)
	if err != nil {
		return nil, err
	}
	if len(mentions) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(mentions))
	seen := make(map[int64]bool)
	for _, mention := range mentions {
		if mention.AccountID == 0 || seen[mention.AccountID] {
			continue
		}
		seen[mention.AccountID] = true
		ids = append(ids, mention.AccountID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	return s.store.GetAccountsByIDs(ctx, ids)
}

func (s *OutgoingService) buildStatusActivity(ctx context.Context, author db.Account, status db.Status, mentions []db.Account, tags []db.GetTagsByStatusIDsRow, attachments []db.MediaAttachment, activityType string) (*activitypub.Activity, []string, error) {
	_, actor, err := ensureLocalActor(s.localDomain, author)
	if err != nil {
		return nil, nil, err
	}

	noteID := statusURI(s.localDomain, author, status)
	if noteID == "" {
		return nil, nil, errors.New("status uri missing")
	}

	to, cc := statusRecipients(s.localDomain, author, status.Visibility, mentions)

	note := activitypub.Object{
		Context:      activitypub.MastodonContextFull(),
		ID:           noteID,
		Type:         activitypub.TypeNote,
		Content:      formatContent(status.Text),
		Summary:      status.SpoilerText,
		AttributedTo: actor,
		To:           to,
		Cc:           cc,
		Sensitive:    status.Sensitive,
	}

	if status.CreatedAt.Valid {
		note.Published = formatTimestamp(status.CreatedAt.Time)
	}

	updatedAt := status.EditedAt
	if !updatedAt.Valid {
		updatedAt = status.UpdatedAt
	}
	if updatedAt.Valid {
		note.Updated = formatTimestamp(updatedAt.Time)
	}

	if status.InReplyToID.Valid {
		parent, err := s.store.GetStatusByID(ctx, status.InReplyToID.Int64)
		if err == nil {
			parentAuthor, err := s.store.GetAccountByID(ctx, parent.AccountID)
			if err == nil {
				note.InReplyTo = statusObjectURI(s.localDomain, parentAuthor, parent)
			}
		}
	}

	note.URL = statusURL(noteID, status)

	note.Tag = buildTags(s.localDomain, mentions, tags, status.ID)

	if len(attachments) > 0 {
		note.Attachment = buildAttachments(s.localDomain, attachments)
	}

	published := status.CreatedAt
	if activityType == activitypub.TypeUpdate && updatedAt.Valid {
		published = updatedAt
	}

	activity := &activitypub.Activity{
		Context:        activitypub.MastodonContextFull(),
		Type:           activityType,
		ID:             statusActivityID(noteID, activityType, published),
		Actor:          actor,
		To:             to,
		Cc:             cc,
		ActivityObject: note,
	}
	if published.Valid {
		activity.Published = formatTimestamp(published.Time)
	}

	targets, err := s.resolveStatusTargets(ctx, author, status.Visibility, mentions)
	if err != nil {
		return nil, nil, err
	}

	return activity, targets, nil
}

func (s *OutgoingService) resolveStatusTargets(ctx context.Context, author db.Account, visibility int32, mentions []db.Account) ([]string, error) {
	inboxes := make(map[string]struct{})

	if visibility != outgoingVisibilityDirect {
		followers, err := s.resolver.ResolveFollowerInboxes(ctx, author.ID)
		if err != nil {
			return nil, err
		}
		for _, target := range followers {
			if target.Inbox == "" {
				continue
			}
			inboxes[target.Inbox] = struct{}{}
		}
	}

	for _, mention := range mentions {
		if accountIsLocal(mention, s.localDomain) {
			continue
		}
		inbox := accountInbox(mention)
		if inbox == "" {
			continue
		}
		inboxes[inbox] = struct{}{}
	}

	return mapKeys(inboxes), nil
}

func (s *OutgoingService) resolveAnnounceTargets(ctx context.Context, actor db.Account, visibility int32, originalAuthor db.Account) ([]string, error) {
	inboxes := make(map[string]struct{})
	if visibility != outgoingVisibilityDirect {
		followers, err := s.resolver.ResolveFollowerInboxes(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		for _, target := range followers {
			if target.Inbox == "" {
				continue
			}
			inboxes[target.Inbox] = struct{}{}
		}
	}

	if !accountIsLocal(originalAuthor, s.localDomain) {
		inbox := accountInbox(originalAuthor)
		if inbox != "" {
			inboxes[inbox] = struct{}{}
		}
	}

	return mapKeys(inboxes), nil
}

func (s *OutgoingService) enqueueTargets(localActor db.Account, activity *activitypub.Activity, inboxes []string) error {
	if s.enqueuer == nil {
		return errors.New("delivery enqueuer missing")
	}
	var lastErr error
	for _, inbox := range inboxes {
		if inbox == "" {
			continue
		}
		if err := s.enqueuer.Enqueue(localActor, inbox, activity); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func statusIsLocal(status db.Status, author db.Account, localDomain string) bool {
	if status.Local.Valid && status.Local.Bool {
		return accountIsLocal(author, localDomain)
	}
	return accountIsLocal(author, localDomain)
}

func accountIsLocal(account db.Account, localDomain string) bool {
	if account.Domain.Valid && account.Domain.String != "" {
		return localDomain != "" && strings.EqualFold(account.Domain.String, localDomain)
	}
	return true
}

func ensureLocalActor(localDomain string, account db.Account) (db.Account, string, error) {
	actor := account.Uri
	if actor == "" {
		actor = actorURI(localDomain, account)
		account.Uri = actor
	}
	if actor == "" {
		return account, "", errors.New("local actor uri missing")
	}
	if !account.PrivateKey.Valid || account.PrivateKey.String == "" {
		return account, actor, errors.New("local actor signing key missing")
	}
	return account, actor, nil
}

func actorURI(localDomain string, account db.Account) string {
	if account.Uri != "" {
		return account.Uri
	}
	if localDomain == "" || account.Username == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s", localDomain, account.Username)
}

func statusURI(localDomain string, author db.Account, status db.Status) string {
	if status.Uri.Valid && status.Uri.String != "" {
		return status.Uri.String
	}
	if localDomain == "" || author.Username == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s/statuses/%d", localDomain, author.Username, status.ID)
}

func statusObjectURI(localDomain string, author db.Account, status db.Status) string {
	if status.Uri.Valid && status.Uri.String != "" {
		return status.Uri.String
	}
	if status.Url.Valid && status.Url.String != "" {
		return status.Url.String
	}
	return statusURI(localDomain, author, status)
}

func statusURL(defaultURI string, status db.Status) any {
	if status.Url.Valid && status.Url.String != "" {
		return status.Url.String
	}
	return defaultURI
}

func accountInbox(account db.Account) string {
	if account.SharedInboxUrl != "" {
		return account.SharedInboxUrl
	}
	return account.InboxUrl
}

func statusRecipients(localDomain string, author db.Account, visibility int32, mentions []db.Account) ([]string, []string) {
	actor := actorURI(localDomain, author)
	followers := ""
	if actor != "" {
		followers = fmt.Sprintf("%s/followers", actor)
	}
	mentionURIs := mentionRecipientURIs(mentions)

	var to []string
	var cc []string

	switch visibility {
	case outgoingVisibilityPublic:
		to = append(to, activitypub.PublicAudience)
		if followers != "" {
			cc = append(cc, followers)
		}
	case outgoingVisibilityUnlisted:
		if followers != "" {
			to = append(to, followers)
		}
		cc = append(cc, activitypub.PublicAudience)
	case outgoingVisibilityPrivate:
		if followers != "" {
			to = append(to, followers)
		}
	case outgoingVisibilityDirect:
		// Direct messages are addressed to mentions only.
	}

	to = appendUnique(to, mentionURIs)
	return to, cc
}

func mentionRecipientURIs(mentions []db.Account) []string {
	uris := make([]string, 0, len(mentions))
	seen := make(map[string]bool)
	for _, mention := range mentions {
		uri := mention.Uri
		if uri == "" {
			continue
		}
		if seen[uri] {
			continue
		}
		seen[uri] = true
		uris = append(uris, uri)
	}
	return uris
}

func appendUnique(base []string, items []string) []string {
	seen := make(map[string]bool)
	for _, value := range base {
		seen[value] = true
	}
	for _, value := range items {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		base = append(base, value)
	}
	return base
}

func statusActivityID(noteID string, activityType string, ts pgtype.Timestamptz) string {
	if noteID == "" {
		return ""
	}
	if activityType == "" {
		activityType = "activity"
	}
	id := fmt.Sprintf("%s#%s", noteID, strings.ToLower(activityType))
	if strings.EqualFold(activityType, activitypub.TypeUpdate) && ts.Valid {
		id = fmt.Sprintf("%s#%s-%d", noteID, strings.ToLower(activityType), ts.Time.Unix())
	}
	return id
}

func followActivityID(localDomain string, followID int64) string {
	if localDomain == "" || followID == 0 {
		return ""
	}
	return fmt.Sprintf("https://%s/follows/%d", localDomain, followID)
}

func likeActivityID(localDomain string, favouriteID int64) string {
	if localDomain == "" || favouriteID == 0 {
		return ""
	}
	return fmt.Sprintf("https://%s/likes/%d", localDomain, favouriteID)
}

func followReferenceID(localDomain string, request db.FollowRequest) string {
	if request.Uri.Valid && request.Uri.String != "" {
		return request.Uri.String
	}
	return followActivityID(localDomain, request.ID)
}

func formatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func formatContent(text string) string {
	if text == "" {
		return ""
	}
	return "<p>" + text + "</p>"
}

func buildTags(localDomain string, mentions []db.Account, tags []db.GetTagsByStatusIDsRow, statusID int64) []activitypub.Tag {
	result := []activitypub.Tag{}

	mentionTags := mentionTags(mentions)
	result = append(result, mentionTags...)

	for _, tag := range tags {
		if tag.StatusID != statusID {
			continue
		}
		name := strings.TrimSpace(tag.Name)
		if name == "" {
			continue
		}
		result = append(result, activitypub.Tag{
			Type: "Hashtag",
			Name: "#" + name,
			Href: fmt.Sprintf("https://%s/tags/%s", localDomain, name),
		})
	}

	return result
}

func mentionTags(mentions []db.Account) []activitypub.Tag {
	tags := make([]activitypub.Tag, 0, len(mentions))
	seen := make(map[string]bool)
	for _, mention := range mentions {
		if mention.Uri == "" {
			continue
		}
		if seen[mention.Uri] {
			continue
		}
		seen[mention.Uri] = true
		name := "@" + mention.Username
		if mention.Domain.Valid && mention.Domain.String != "" {
			name = fmt.Sprintf("@%s@%s", mention.Username, mention.Domain.String)
		}
		tags = append(tags, activitypub.Tag{
			Type: "Mention",
			Href: mention.Uri,
			Name: name,
		})
	}
	return tags
}

func buildAttachments(localDomain string, attachments []db.MediaAttachment) []any {
	items := make([]any, 0, len(attachments))
	for _, attachment := range attachments {
		url := attachment.RemoteUrl
		if url == "" {
			url = localAttachmentURL(localDomain, attachment)
		}
		if url == "" {
			continue
		}

		item := map[string]any{
			"type":      attachmentActivityType(attachment),
			"mediaType": attachmentMediaType(attachment),
			"url":       url,
		}

		if attachment.Description.Valid && attachment.Description.String != "" {
			item["name"] = attachment.Description.String
		}
		if attachment.Blurhash.Valid && attachment.Blurhash.String != "" {
			item["blurhash"] = attachment.Blurhash.String
		}
		if attachment.ThumbnailRemoteUrl.Valid && attachment.ThumbnailRemoteUrl.String != "" {
			item["previewUrl"] = attachment.ThumbnailRemoteUrl.String
		} else if attachment.ThumbnailFileName.Valid && attachment.ThumbnailFileName.String != "" {
			previewURL := localAttachmentThumbnailURL(localDomain, attachment)
			if previewURL != "" {
				item["previewUrl"] = previewURL
			}
		}

		items = append(items, item)
	}
	return items
}

func attachmentActivityType(attachment db.MediaAttachment) string {
	switch attachment.Type {
	case 1:
		return activitypub.TypeVideo
	case 2:
		return activitypub.TypeVideo
	case 3:
		return activitypub.TypeAudio
	default:
		return activitypub.TypeImage
	}
}

func attachmentMediaType(attachment db.MediaAttachment) string {
	if attachment.FileContentType.Valid && attachment.FileContentType.String != "" {
		return attachment.FileContentType.String
	}
	return ""
}

func localAttachmentURL(localDomain string, attachment db.MediaAttachment) string {
	if localDomain == "" {
		return ""
	}
	if !attachment.AccountID.Valid || attachment.AccountID.Int64 == 0 {
		return ""
	}
	if !attachment.FileFileName.Valid || attachment.FileFileName.String == "" {
		return ""
	}
	if !attachment.CreatedAt.Valid {
		return ""
	}
	storagePath := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.FileFileName.String)
	if storagePath == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/files/%s", localDomain, storagePath)
}

func localAttachmentThumbnailURL(localDomain string, attachment db.MediaAttachment) string {
	if localDomain == "" {
		return ""
	}
	if !attachment.AccountID.Valid || attachment.AccountID.Int64 == 0 {
		return ""
	}
	if !attachment.ThumbnailFileName.Valid || attachment.ThumbnailFileName.String == "" {
		return ""
	}
	if !attachment.CreatedAt.Valid {
		return ""
	}
	storagePath := mediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, attachment.ThumbnailFileName.String)
	if storagePath == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/files/%s", localDomain, storagePath)
}

func mediaStoragePath(accountID int64, createdAt time.Time, filename string) string {
	if accountID <= 0 || filename == "" {
		return ""
	}
	return path.Join(
		"media_attachments",
		"files",
		fmt.Sprintf("%d", accountID%1000),
		fmt.Sprintf("%d", accountID),
		createdAt.UTC().Format("2006/01/02"),
		filename,
	)
}

func mapKeys(inboxes map[string]struct{}) []string {
	keys := make([]string, 0, len(inboxes))
	for key := range inboxes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
