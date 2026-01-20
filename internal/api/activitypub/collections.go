package activitypub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	visibilityPublic   = 0
	visibilityUnlisted = 1
	visibilityPrivate  = 2
	visibilityDirect   = 3
)

const (
	defaultCollectionLimit = 20
	maxCollectionLimit     = 80
)

func (h *FederationHandler) writeOutbox(w http.ResponseWriter, r *http.Request) {
	username := web.Param(r, "username")
	actorURI := h.actorURIFromUsername(username)

	isPage, limit, offset := parsePageRequest(r)
	if !h.useDB() {
		if isPage {
			h.renderEmptyOrderedPage(w, fmt.Sprintf("%s/outbox", actorURI), limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, fmt.Sprintf("%s/outbox", actorURI))
		return
	}

	account, err := h.store.GetAccountByUsername(r.Context(), username)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	actorURI = actorURIForAccount(h.domain, account)
	base := fmt.Sprintf("%s/outbox", actorURI)

	if !isPage {
		total, err := h.store.CountAccountOutboxStatuses(r.Context(), account.ID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to load outbox")
			return
		}
		collection := activitypub.OrderedCollection{
			Collection: activitypub.Collection{
				Object: activitypub.Object{
					Context: activitypub.DefaultContext(),
					ID:      base,
					Type:    activitypub.TypeOrderedCollection,
				},
				TotalItems: int(total),
				First:      collectionPageURL(base, limit, 0),
			},
		}
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		web.JSON(w, http.StatusOK, collection)
		return
	}

	statuses, err := h.store.GetAccountOutboxStatuses(r.Context(), db.GetAccountOutboxStatusesParams{
		AccountID: account.ID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load outbox")
		return
	}

	ctx, err := h.buildStatusContext(r.Context(), statuses)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render outbox")
		return
	}

	activities, err := ctx.activitiesForStatuses(account, statuses)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render outbox")
		return
	}

	total, err := h.store.CountAccountOutboxStatuses(r.Context(), account.ID)
	if err != nil {
		total = int64(len(statuses))
	}
	next, prev := collectionLinks(base, limit, offset, total)

	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      collectionPageURL(base, limit, offset),
				Type:    activitypub.TypeOrderedCollectionPage,
			},
			TotalItems: int(total),
		},
		OrderedItems: activities,
		PartOf:       base,
		Next:         next,
		Prev:         prev,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, page)
}

func (h *FederationHandler) writeFollowers(w http.ResponseWriter, r *http.Request) {
	h.writeFollowCollection(w, r, "followers")
}

func (h *FederationHandler) writeFollowing(w http.ResponseWriter, r *http.Request) {
	h.writeFollowCollection(w, r, "following")
}

func (h *FederationHandler) writeFollowCollection(w http.ResponseWriter, r *http.Request, kind string) {
	username := web.Param(r, "username")
	actorURI := h.actorURIFromUsername(username)
	base := fmt.Sprintf("%s/%s", actorURI, kind)

	isPage, limit, offset := parsePageRequest(r)
	if !h.useDB() {
		if isPage {
			h.renderEmptyOrderedPage(w, base, limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, base)
		return
	}

	account, err := h.store.GetAccountByUsername(r.Context(), username)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	actorURI = actorURIForAccount(h.domain, account)
	base = fmt.Sprintf("%s/%s", actorURI, kind)

	if account.HideCollections.Valid && account.HideCollections.Bool {
		if isPage {
			h.renderEmptyOrderedPage(w, base, limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, base)
		return
	}

	var total int64
	switch kind {
	case "followers":
		total, err = h.store.CountFollowers(r.Context(), account.ID)
	default:
		total, err = h.store.CountFollowing(r.Context(), account.ID)
	}
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load collection")
		return
	}

	if !isPage {
		collection := activitypub.OrderedCollection{
			Collection: activitypub.Collection{
				Object: activitypub.Object{
					Context: activitypub.DefaultContext(),
					ID:      base,
					Type:    activitypub.TypeOrderedCollection,
				},
				TotalItems: int(total),
				First:      collectionPageURL(base, limit, 0),
			},
		}
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		web.JSON(w, http.StatusOK, collection)
		return
	}

	var accounts []db.Account
	switch kind {
	case "followers":
		accounts, err = h.store.GetFollowers(r.Context(), db.GetFollowersParams{
			TargetAccountID: account.ID,
			Limit:           int32(limit),
			Offset:          int32(offset),
		})
	default:
		accounts, err = h.store.GetFollowing(r.Context(), db.GetFollowingParams{
			AccountID: account.ID,
			Limit:     int32(limit),
			Offset:    int32(offset),
		})
	}
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load collection")
		return
	}

	items := make([]any, 0, len(accounts))
	for _, acc := range accounts {
		if uri := actorURIForAccount(h.domain, acc); uri != "" {
			items = append(items, uri)
		}
	}

	next, prev := collectionLinks(base, limit, offset, total)
	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      collectionPageURL(base, limit, offset),
				Type:    activitypub.TypeOrderedCollectionPage,
			},
			TotalItems: int(total),
		},
		OrderedItems: items,
		PartOf:       base,
		Next:         next,
		Prev:         prev,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, page)
}

// GetFeatured handles GET /users/:username/collections/featured.
func (h *FederationHandler) GetFeatured(w http.ResponseWriter, r *http.Request) {
	username := web.Param(r, "username")
	actorURI := h.actorURIFromUsername(username)
	base := fmt.Sprintf("%s/collections/featured", actorURI)

	isPage, limit, offset := parsePageRequest(r)
	if !h.useDB() {
		if isPage {
			h.renderEmptyOrderedPage(w, base, limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, base)
		return
	}

	account, err := h.store.GetAccountByUsername(r.Context(), username)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	if account.HideCollections.Valid && account.HideCollections.Bool {
		if isPage {
			h.renderEmptyOrderedPage(w, base, limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, base)
		return
	}

	actorURI = actorURIForAccount(h.domain, account)
	base = fmt.Sprintf("%s/collections/featured", actorURI)

	statuses, err := h.store.GetPinnedStatuses(r.Context(), account.ID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load collection")
		return
	}
	statuses = filterPublicStatuses(statuses)

	total := len(statuses)
	if !isPage {
		collection := activitypub.OrderedCollection{
			Collection: activitypub.Collection{
				Object: activitypub.Object{
					Context: activitypub.DefaultContext(),
					ID:      base,
					Type:    activitypub.TypeOrderedCollection,
				},
				TotalItems: total,
				First:      collectionPageURL(base, limit, 0),
			},
		}
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		web.JSON(w, http.StatusOK, collection)
		return
	}

	pageStatuses := sliceStatuses(statuses, limit, offset)
	ctx, err := h.buildStatusContext(r.Context(), pageStatuses)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render collection")
		return
	}
	notes, err := ctx.notesForStatuses(account, pageStatuses)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render collection")
		return
	}
	next, prev := collectionLinks(base, limit, offset, int64(total))

	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      collectionPageURL(base, limit, offset),
				Type:    activitypub.TypeOrderedCollectionPage,
			},
			TotalItems: total,
		},
		OrderedItems: notes,
		PartOf:       base,
		Next:         next,
		Prev:         prev,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, page)
}

// GetFeaturedTags handles GET /users/:username/collections/tags.
func (h *FederationHandler) GetFeaturedTags(w http.ResponseWriter, r *http.Request) {
	username := web.Param(r, "username")
	actorURI := h.actorURIFromUsername(username)
	base := fmt.Sprintf("%s/collections/tags", actorURI)

	isPage, limit, offset := parsePageRequest(r)
	if !h.useDB() {
		if isPage {
			h.renderEmptyOrderedPage(w, base, limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, base)
		return
	}

	account, err := h.store.GetAccountByUsername(r.Context(), username)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	if account.HideCollections.Valid && account.HideCollections.Bool {
		if isPage {
			h.renderEmptyOrderedPage(w, base, limit, offset)
			return
		}
		h.renderEmptyOrderedCollection(w, base)
		return
	}

	actorURI = actorURIForAccount(h.domain, account)
	base = fmt.Sprintf("%s/collections/tags", actorURI)

	tags, err := h.store.ListFeaturedTags(r.Context(), account.ID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load collection")
		return
	}

	total := len(tags)
	if !isPage {
		collection := activitypub.OrderedCollection{
			Collection: activitypub.Collection{
				Object: activitypub.Object{
					Context: activitypub.DefaultContext(),
					ID:      base,
					Type:    activitypub.TypeOrderedCollection,
				},
				TotalItems: total,
				First:      collectionPageURL(base, limit, 0),
			},
		}
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		web.JSON(w, http.StatusOK, collection)
		return
	}

	tags = sliceFeaturedTags(tags, limit, offset)
	items := make([]any, 0, len(tags))
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Name)
		if name == "" {
			continue
		}
		items = append(items, activitypub.Tag{
			Type: "Hashtag",
			Name: "#" + name,
			Href: fmt.Sprintf("https://%s/tags/%s", h.domain, name),
		})
	}

	next, prev := collectionLinks(base, limit, offset, int64(total))
	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      collectionPageURL(base, limit, offset),
				Type:    activitypub.TypeOrderedCollectionPage,
			},
			TotalItems: total,
		},
		OrderedItems: items,
		PartOf:       base,
		Next:         next,
		Prev:         prev,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, page)
}

func (h *FederationHandler) renderEmptyOrderedCollection(w http.ResponseWriter, base string) {
	h.renderEmptyOrderedCollectionWithPage(w, base, defaultCollectionLimit, 0)
}

func (h *FederationHandler) renderEmptyOrderedCollectionWithPage(w http.ResponseWriter, base string, limit, offset int) {
	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      base,
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: 0,
			First:      collectionPageURL(base, limit, 0),
		},
	}
	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) renderEmptyOrderedPage(w http.ResponseWriter, base string, limit, offset int) {
	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      collectionPageURL(base, limit, offset),
				Type:    activitypub.TypeOrderedCollectionPage,
			},
			TotalItems: 0,
		},
		OrderedItems: []any{},
		PartOf:       base,
	}
	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, page)
}

func (h *FederationHandler) actorURIFromUsername(username string) string {
	if h.domain == "" || username == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s", h.domain, username)
}

func actorURIForAccount(localDomain string, account db.Account) string {
	if account.Uri != "" {
		return account.Uri
	}
	domain := localDomain
	if account.Domain.Valid && account.Domain.String != "" {
		domain = account.Domain.String
	}
	if domain == "" || account.Username == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s", domain, account.Username)
}

func parsePageRequest(r *http.Request) (bool, int, int) {
	rawPage := strings.TrimSpace(web.QueryParam(r, "page"))
	if rawPage == "" || strings.EqualFold(rawPage, "false") {
		return false, 0, 0
	}

	limit := parseCollectionLimit(web.QueryParam(r, "limit"))
	offset := parseCollectionOffset(web.QueryParam(r, "offset"))

	if offset == 0 {
		if pageNum, err := strconv.Atoi(rawPage); err == nil && pageNum > 1 {
			offset = (pageNum - 1) * limit
		}
	}

	return true, limit, offset
}

func parseCollectionLimit(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultCollectionLimit
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return defaultCollectionLimit
	}
	if value > maxCollectionLimit {
		return maxCollectionLimit
	}
	return value
}

func parseCollectionOffset(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func collectionPageURL(base string, limit, offset int) string {
	values := url.Values{}
	values.Set("page", "true")
	if limit > 0 && limit != defaultCollectionLimit {
		values.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		values.Set("offset", strconv.Itoa(offset))
	}
	return base + "?" + values.Encode()
}

func collectionLinks(base string, limit, offset int, total int64) (string, string) {
	var next string
	var prev string
	if total > 0 && int64(offset+limit) < total {
		next = collectionPageURL(base, limit, offset+limit)
	}
	if offset > 0 {
		prevOffset := offset - limit
		if prevOffset < 0 {
			prevOffset = 0
		}
		prev = collectionPageURL(base, limit, prevOffset)
	}
	return next, prev
}

func sliceStatuses(statuses []db.Status, limit, offset int) []db.Status {
	if len(statuses) == 0 {
		return statuses
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(statuses) {
		return []db.Status{}
	}
	end := offset + limit
	if end > len(statuses) || limit <= 0 {
		end = len(statuses)
	}
	return statuses[offset:end]
}

func sliceFeaturedTags(tags []db.FeaturedTag, limit, offset int) []db.FeaturedTag {
	if len(tags) == 0 {
		return tags
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(tags) {
		return []db.FeaturedTag{}
	}
	end := offset + limit
	if end > len(tags) || limit <= 0 {
		end = len(tags)
	}
	return tags[offset:end]
}

func filterPublicStatuses(statuses []db.Status) []db.Status {
	filtered := make([]db.Status, 0, len(statuses))
	for _, status := range statuses {
		if status.Visibility == visibilityPublic || status.Visibility == visibilityUnlisted {
			filtered = append(filtered, status)
		}
	}
	return filtered
}

type statusContext struct {
	localDomain         string
	mentionsByStatus    map[int64][]db.Account
	tagsByStatus        map[int64][]db.GetTagsByStatusIDsRow
	attachmentsByStatus map[int64][]db.MediaAttachment
	relatedStatuses     map[int64]db.Status
	relatedAccounts     map[int64]db.Account
}

func (h *FederationHandler) buildStatusContext(ctx context.Context, statuses []db.Status) (*statusContext, error) {
	context := &statusContext{
		localDomain:         h.domain,
		mentionsByStatus:    make(map[int64][]db.Account),
		tagsByStatus:        make(map[int64][]db.GetTagsByStatusIDsRow),
		attachmentsByStatus: make(map[int64][]db.MediaAttachment),
		relatedStatuses:     make(map[int64]db.Status),
		relatedAccounts:     make(map[int64]db.Account),
	}

	if h.store == nil || len(statuses) == 0 {
		return context, nil
	}

	statusIDs := uniqueStatusIDs(statuses)
	mentions, err := h.store.GetMentionsByStatusIDs(ctx, statusIDs)
	if err != nil {
		return nil, err
	}
	mentionAccountIDs := uniqueMentionAccountIDs(mentions)
	mentionAccounts := map[int64]db.Account{}
	if len(mentionAccountIDs) > 0 {
		accounts, err := h.store.GetAccountsByIDs(ctx, mentionAccountIDs)
		if err != nil {
			return nil, err
		}
		for _, account := range accounts {
			mentionAccounts[account.ID] = account
		}
	}
	for _, mention := range mentions {
		account, ok := mentionAccounts[mention.AccountID]
		if !ok {
			continue
		}
		context.mentionsByStatus[mention.StatusID] = append(context.mentionsByStatus[mention.StatusID], account)
	}

	tags, err := h.store.GetTagsByStatusIDs(ctx, statusIDs)
	if err != nil {
		return nil, err
	}
	for _, tag := range tags {
		context.tagsByStatus[tag.StatusID] = append(context.tagsByStatus[tag.StatusID], tag)
	}

	attachments, err := h.store.GetMediaAttachmentsByStatusIDs(ctx, statusIDs)
	if err != nil {
		return nil, err
	}
	for _, attachment := range attachments {
		if !attachment.StatusID.Valid {
			continue
		}
		context.attachmentsByStatus[attachment.StatusID.Int64] = append(context.attachmentsByStatus[attachment.StatusID.Int64], attachment)
	}

	relatedIDs := uniqueRelatedStatusIDs(statuses)
	if len(relatedIDs) == 0 {
		return context, nil
	}
	relatedStatuses, err := h.store.GetStatusesByIDs(ctx, relatedIDs)
	if err != nil {
		return nil, err
	}
	relatedAccountIDs := make([]int64, 0, len(relatedStatuses))
	relatedSeen := make(map[int64]struct{})
	for _, status := range relatedStatuses {
		context.relatedStatuses[status.ID] = status
		if _, ok := relatedSeen[status.AccountID]; !ok {
			relatedSeen[status.AccountID] = struct{}{}
			relatedAccountIDs = append(relatedAccountIDs, status.AccountID)
		}
	}
	if len(relatedAccountIDs) == 0 {
		return context, nil
	}
	relatedAccounts, err := h.store.GetAccountsByIDs(ctx, relatedAccountIDs)
	if err != nil {
		return nil, err
	}
	for _, account := range relatedAccounts {
		context.relatedAccounts[account.ID] = account
	}

	return context, nil
}

func uniqueStatusIDs(statuses []db.Status) []int64 {
	ids := make([]int64, 0, len(statuses))
	seen := make(map[int64]struct{})
	for _, status := range statuses {
		if _, ok := seen[status.ID]; ok {
			continue
		}
		seen[status.ID] = struct{}{}
		ids = append(ids, status.ID)
	}
	return ids
}

func uniqueMentionAccountIDs(mentions []db.Mention) []int64 {
	ids := make([]int64, 0, len(mentions))
	seen := make(map[int64]struct{})
	for _, mention := range mentions {
		if mention.AccountID == 0 {
			continue
		}
		if _, ok := seen[mention.AccountID]; ok {
			continue
		}
		seen[mention.AccountID] = struct{}{}
		ids = append(ids, mention.AccountID)
	}
	return ids
}

func uniqueRelatedStatusIDs(statuses []db.Status) []int64 {
	ids := make([]int64, 0, len(statuses))
	seen := make(map[int64]struct{})
	for _, status := range statuses {
		if status.InReplyToID.Valid && status.InReplyToID.Int64 > 0 {
			if _, ok := seen[status.InReplyToID.Int64]; !ok {
				seen[status.InReplyToID.Int64] = struct{}{}
				ids = append(ids, status.InReplyToID.Int64)
			}
		}
		if status.ReblogOfID.Valid && status.ReblogOfID.Int64 > 0 {
			if _, ok := seen[status.ReblogOfID.Int64]; !ok {
				seen[status.ReblogOfID.Int64] = struct{}{}
				ids = append(ids, status.ReblogOfID.Int64)
			}
		}
	}
	return ids
}

func (c *statusContext) activitiesForStatuses(author db.Account, statuses []db.Status) ([]any, error) {
	items := make([]any, 0, len(statuses))
	for _, status := range statuses {
		activity, err := c.activityForStatus(author, status)
		if err != nil {
			return nil, err
		}
		if activity == nil {
			continue
		}
		items = append(items, activity)
	}
	return items, nil
}

func (c *statusContext) notesForStatuses(author db.Account, statuses []db.Status) ([]any, error) {
	items := make([]any, 0, len(statuses))
	for _, status := range statuses {
		if status.ReblogOfID.Valid {
			continue
		}
		note, err := c.noteForStatus(author, status)
		if err != nil {
			return nil, err
		}
		items = append(items, note)
	}
	return items, nil
}

func (c *statusContext) activityForStatus(author db.Account, status db.Status) (*activitypub.Activity, error) {
	if status.ReblogOfID.Valid {
		return c.announceActivity(author, status)
	}
	return c.createActivity(author, status)
}

func (c *statusContext) announceActivity(author db.Account, status db.Status) (*activitypub.Activity, error) {
	original, ok := c.relatedStatuses[status.ReblogOfID.Int64]
	if !ok {
		return nil, fmt.Errorf("missing reblogged status")
	}
	originalAuthor, ok := c.relatedAccounts[original.AccountID]
	if !ok {
		return nil, fmt.Errorf("missing reblogged author")
	}
	objectURI := statusObjectURI(c.localDomain, originalAuthor, original)
	if objectURI == "" {
		return nil, fmt.Errorf("missing reblog object uri")
	}

	actorURI := actorURIForAccount(c.localDomain, author)
	if actorURI == "" {
		return nil, fmt.Errorf("missing actor uri")
	}
	activityID := statusURI(c.localDomain, author, status)
	if activityID == "" {
		return nil, fmt.Errorf("missing announce id")
	}
	to, cc := statusRecipients(c.localDomain, author, status.Visibility, nil)

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeAnnounce,
		ID:             activityID,
		Actor:          actorURI,
		To:             to,
		Cc:             cc,
		ActivityObject: objectURI,
	}

	if status.CreatedAt.Valid {
		activity.Published = formatTimestamp(status.CreatedAt.Time)
	}
	return activity, nil
}

func (c *statusContext) createActivity(author db.Account, status db.Status) (*activitypub.Activity, error) {
	note, err := c.noteForStatus(author, status)
	if err != nil {
		return nil, err
	}
	noteID := note.ID
	if noteID == "" {
		return nil, fmt.Errorf("missing note id")
	}
	actorURI := actorURIForAccount(c.localDomain, author)
	if actorURI == "" {
		return nil, fmt.Errorf("missing actor uri")
	}

	published := status.CreatedAt
	activity := &activitypub.Activity{
		Context:        activitypub.MastodonContextFull(),
		Type:           activitypub.TypeCreate,
		ID:             statusActivityID(noteID, activitypub.TypeCreate, published),
		Actor:          actorURI,
		To:             note.To,
		Cc:             note.Cc,
		ActivityObject: note,
	}
	if published.Valid {
		activity.Published = formatTimestamp(published.Time)
	}
	return activity, nil
}

func (c *statusContext) noteForStatus(author db.Account, status db.Status) (activitypub.Object, error) {
	actorURI := actorURIForAccount(c.localDomain, author)
	if actorURI == "" {
		return activitypub.Object{}, fmt.Errorf("missing actor uri")
	}
	noteID := statusURI(c.localDomain, author, status)
	if noteID == "" {
		return activitypub.Object{}, fmt.Errorf("missing status uri")
	}
	mentions := c.mentionsByStatus[status.ID]
	tags := c.tagsByStatus[status.ID]
	attachments := c.attachmentsByStatus[status.ID]
	to, cc := statusRecipients(c.localDomain, author, status.Visibility, mentions)

	note := activitypub.Object{
		Context:      activitypub.MastodonContextFull(),
		ID:           noteID,
		Type:         activitypub.TypeNote,
		Content:      formatContent(status.Text),
		Summary:      status.SpoilerText,
		AttributedTo: actorURI,
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
		if parent, ok := c.relatedStatuses[status.InReplyToID.Int64]; ok {
			if parentAuthor, ok := c.relatedAccounts[parent.AccountID]; ok {
				if uri := statusObjectURI(c.localDomain, parentAuthor, parent); uri != "" {
					note.InReplyTo = uri
				}
			}
		}
	}

	note.URL = statusURL(noteID, status)
	note.Tag = buildTags(c.localDomain, mentions, tags, status.ID)

	if len(attachments) > 0 {
		note.Attachment = buildAttachments(c.localDomain, attachments)
	}

	return note, nil
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

func statusRecipients(localDomain string, author db.Account, visibility int32, mentions []db.Account) ([]string, []string) {
	actor := actorURIForAccount(localDomain, author)
	followers := ""
	if actor != "" {
		followers = fmt.Sprintf("%s/followers", actor)
	}
	mentionURIs := mentionRecipientURIs(localDomain, mentions)

	var to []string
	var cc []string

	switch visibility {
	case visibilityPublic:
		to = append(to, activitypub.PublicAudience)
		if followers != "" {
			cc = append(cc, followers)
		}
	case visibilityUnlisted:
		if followers != "" {
			to = append(to, followers)
		}
		cc = append(cc, activitypub.PublicAudience)
	case visibilityPrivate:
		if followers != "" {
			to = append(to, followers)
		}
	case visibilityDirect:
		// Direct messages are addressed to mentions only.
	}

	to = appendUnique(to, mentionURIs)
	return to, cc
}

func mentionRecipientURIs(localDomain string, mentions []db.Account) []string {
	uris := make([]string, 0, len(mentions))
	seen := make(map[string]bool)
	for _, mention := range mentions {
		uri := actorURIForAccount(localDomain, mention)
		if uri == "" || seen[uri] {
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
	result = append(result, mentionTags(localDomain, mentions)...)

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

func mentionTags(localDomain string, mentions []db.Account) []activitypub.Tag {
	tags := make([]activitypub.Tag, 0, len(mentions))
	seen := make(map[string]bool)
	for _, mention := range mentions {
		uri := actorURIForAccount(localDomain, mention)
		if uri == "" || seen[uri] {
			continue
		}
		seen[uri] = true
		name := "@" + mention.Username
		if mention.Domain.Valid && mention.Domain.String != "" {
			name = fmt.Sprintf("@%s@%s", mention.Username, mention.Domain.String)
		}
		tags = append(tags, activitypub.Tag{
			Type: "Mention",
			Href: uri,
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
