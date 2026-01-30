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
	username := accountParam(r)
	actorURI := h.actorURIFromUsername(username)

	pageRequested, limit, maxID, minID, sinceID := parseOutboxRequest(r)
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Outbox unavailable")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	actorURI = actorURIForAccount(h.domain, account)
	h.writeOutboxForAccount(w, r, account, actorURI, pageRequested, limit, maxID, minID, sinceID)
}

func (h *FederationHandler) writeOutboxForAccount(w http.ResponseWriter, r *http.Request, account db.Account, actorURI string, pageRequested bool, limit int, maxID, minID, sinceID int64) {
	base := fmt.Sprintf("%s/outbox", actorURI)

	if !pageRequested {
		total, err := h.store.CountAccountOutboxStatuses(r.Context(), account.ID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to load outbox")
			return
		}
		first := outboxPageURL(base, limit, 0, 0, 0)
		last := outboxLastURL(base, limit)
		collection := activitypub.OrderedCollection{
			Collection: activitypub.Collection{
				Object: activitypub.Object{
					Context: activitypub.DefaultContext(),
					ID:      base,
					Type:    activitypub.TypeOrderedCollection,
				},
				TotalItems: int(total),
				First:      first,
				Last:       last,
			},
		}
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		web.JSON(w, http.StatusOK, collection)
		return
	}

	statuses, err := h.store.GetAccountOutboxStatuses(r.Context(), db.GetAccountOutboxStatusesParams{
		AccountID: account.ID,
		Limit:     int32(limit),
		MaxID:     optionalID(maxID),
		MinID:     optionalID(minID),
		SinceID:   optionalID(sinceID),
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

	var next string
	var prev string
	if len(statuses) == limit {
		lastID := statuses[len(statuses)-1].ID
		next = outboxPageURL(base, limit, lastID, 0, 0)
	}
	if len(statuses) > 0 {
		firstID := statuses[0].ID
		prev = outboxPageURL(base, limit, 0, firstID, 0)
	}

	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      outboxPageURL(base, limit, maxID, minID, sinceID),
				Type:    activitypub.TypeOrderedCollectionPage,
			},
			TotalItems: len(statuses),
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
	username := accountParam(r)
	actorURI := h.actorURIFromUsername(username)
	base := fmt.Sprintf("%s/%s", actorURI, kind)

	isPage, limit, offset := parsePageRequest(r)
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Collection unavailable")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
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
	username := accountParam(r)
	actorURI := h.actorURIFromUsername(username)
	base := fmt.Sprintf("%s/collections/featured", actorURI)

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Collection unavailable")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	if account.HideCollections.Valid && account.HideCollections.Bool {
		h.renderEmptyOrderedItemsCollection(w, base)
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

	ctx, err := h.buildStatusContext(r.Context(), statuses)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render collection")
		return
	}
	notes, err := ctx.notesForStatuses(account, statuses)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render collection")
		return
	}

	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      base,
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: len(notes),
		},
		OrderedItems: notes,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

// GetFeaturedTags handles GET /users/:username/collections/tags.
func (h *FederationHandler) GetFeaturedTags(w http.ResponseWriter, r *http.Request) {
	username := accountParam(r)
	actorURI := h.actorURIFromUsername(username)
	base := fmt.Sprintf("%s/collections/tags", actorURI)

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Collection unavailable")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}
	if account.HideCollections.Valid && account.HideCollections.Bool {
		h.renderEmptyCollection(w, base)
		return
	}

	actorURI = actorURIForAccount(h.domain, account)
	base = fmt.Sprintf("%s/collections/tags", actorURI)

	tags, err := h.store.ListFeaturedTags(r.Context(), account.ID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load collection")
		return
	}

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

	collection := activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      base,
			Type:    activitypub.TypeCollection,
		},
		TotalItems: len(items),
		Items:      items,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) renderEmptyOrderedItemsCollection(w http.ResponseWriter, base string) {
	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      base,
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: 0,
		},
		OrderedItems: []any{},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) renderEmptyCollection(w http.ResponseWriter, base string) {
	collection := activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      base,
			Type:    activitypub.TypeCollection,
		},
		TotalItems: 0,
		Items:      []any{},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
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

func (h *FederationHandler) renderEmptyOutboxCollection(w http.ResponseWriter, base string, limit int) {
	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      base,
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: 0,
			First:      outboxPageURL(base, limit, 0, 0, 0),
			Last:       outboxLastURL(base, limit),
		},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) renderEmptyOutboxPage(w http.ResponseWriter, base string, limit int, maxID, minID, sinceID int64) {
	page := activitypub.OrderedCollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      outboxPageURL(base, limit, maxID, minID, sinceID),
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

func parseOutboxRequest(r *http.Request) (bool, int, int64, int64, int64) {
	pageRequested := truthyParam(web.QueryParam(r, "page"))
	if !pageRequested {
		return false, 0, 0, 0, 0
	}
	limit := parseCollectionLimit(web.QueryParam(r, "limit"))
	maxID := parseCollectionID(web.QueryParam(r, "max_id"))
	minID := parseCollectionID(web.QueryParam(r, "min_id"))
	sinceID := parseCollectionID(web.QueryParam(r, "since_id"))
	return true, limit, maxID, minID, sinceID
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

func parseCollectionID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func optionalID(value int64) pgtype.Int8 {
	if value <= 0 {
		return pgtype.Int8{Valid: false}
	}
	return pgtype.Int8{Int64: value, Valid: true}
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

func outboxPageURL(base string, limit int, maxID, minID, sinceID int64) string {
	values := url.Values{}
	values.Set("page", "true")
	if limit > 0 && limit != defaultCollectionLimit {
		values.Set("limit", strconv.Itoa(limit))
	}
	if maxID > 0 {
		values.Set("max_id", strconv.FormatInt(maxID, 10))
	}
	if minID > 0 {
		values.Set("min_id", strconv.FormatInt(minID, 10))
	}
	if sinceID > 0 {
		values.Set("since_id", strconv.FormatInt(sinceID, 10))
	}
	return base + "?" + values.Encode()
}

func outboxLastURL(base string, limit int) string {
	values := url.Values{}
	values.Set("page", "true")
	values.Set("min_id", "0")
	if limit > 0 && limit != defaultCollectionLimit {
		values.Set("limit", strconv.Itoa(limit))
	}
	return base + "?" + values.Encode()
}

func statusRepliesURLLocal(domain, username string, statusID int64, page bool, minID int64, onlyOtherAccounts bool) string {
	if domain == "" || username == "" {
		return ""
	}
	base := fmt.Sprintf("https://%s/users/%s/statuses/%d/replies", domain, username, statusID)
	params := url.Values{}
	if page {
		params.Set("page", "true")
	}
	if minID > 0 {
		params.Set("min_id", strconv.FormatInt(minID, 10))
	}
	if onlyOtherAccounts {
		params.Set("only_other_accounts", "true")
	}
	encoded := params.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

func statusCounterURLLocal(domain, username string, statusID int64, kind string) string {
	if domain == "" || username == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s/statuses/%d/%s", domain, username, statusID, kind)
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
	ctx                 context.Context
	store               Store
	localDomain         string
	mentionsByStatus    map[int64][]db.Account
	tagsByStatus        map[int64][]db.GetTagsByStatusIDsRow
	attachmentsByStatus map[int64][]db.MediaAttachment
	relatedStatuses     map[int64]db.Status
	relatedAccounts     map[int64]db.Account
	statsByStatus       map[int64]db.StatusStat
	conversationsByID   map[int64]db.Conversation
}

func (h *FederationHandler) buildStatusContext(ctx context.Context, statuses []db.Status) (*statusContext, error) {
	context := &statusContext{
		ctx:                 ctx,
		store:               h.store,
		localDomain:         h.domain,
		mentionsByStatus:    make(map[int64][]db.Account),
		tagsByStatus:        make(map[int64][]db.GetTagsByStatusIDsRow),
		attachmentsByStatus: make(map[int64][]db.MediaAttachment),
		relatedStatuses:     make(map[int64]db.Status),
		relatedAccounts:     make(map[int64]db.Account),
		statsByStatus:       make(map[int64]db.StatusStat),
		conversationsByID:   make(map[int64]db.Conversation),
	}

	if h.store == nil || len(statuses) == 0 {
		return context, nil
	}

	statusIDs := uniqueStatusIDs(statuses)
	conversationIDs := uniqueConversationIDs(statuses)
	if h.store != nil && len(statusIDs) > 0 {
		stats, err := h.store.GetStatusStatsByIDs(ctx, statusIDs)
		if err != nil {
			return nil, err
		}
		for _, stat := range stats {
			context.statsByStatus[stat.StatusID] = stat
		}
	}
	if h.store != nil && len(conversationIDs) > 0 {
		conversations, err := h.store.GetConversationsByIDs(ctx, conversationIDs)
		if err != nil {
			return nil, err
		}
		for _, conversation := range conversations {
			context.conversationsByID[conversation.ID] = conversation
		}
	}
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

func uniqueConversationIDs(statuses []db.Status) []int64 {
	ids := make([]int64, 0, len(statuses))
	seen := make(map[int64]struct{})
	for _, status := range statuses {
		if !status.ConversationID.Valid || status.ConversationID.Int64 <= 0 {
			continue
		}
		if _, ok := seen[status.ConversationID.Int64]; ok {
			continue
		}
		seen[status.ConversationID.Int64] = struct{}{}
		ids = append(ids, status.ConversationID.Int64)
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

	if c.isLocalStatus(status) {
		note.Replies = c.repliesCollection(author, status)
		note.Likes = c.likesCollection(author, status)
		note.Shares = c.sharesCollection(author, status)
	}

	if conversation, ok := c.conversationsByID[status.ConversationID.Int64]; ok {
		conversationURI := conversationURI(c.localDomain, conversation)
		if conversationURI != "" {
			note.Conversation = conversationURI
		}
		contextURI := contextURI(c.localDomain, conversation)
		if contextURI != "" {
			note.ContextURI = contextURI
		}
	}

	return note, nil
}

func (c *statusContext) repliesCollection(author db.Account, status db.Status) *activitypub.Collection {
	if c.store == nil || c.ctx == nil || author.Username == "" {
		return nil
	}
	baseURL := statusRepliesURLLocal(c.localDomain, author.Username, status.ID, false, 0, false)
	pageURL := statusRepliesURLLocal(c.localDomain, author.Username, status.ID, true, 0, false)
	if baseURL == "" || pageURL == "" {
		return nil
	}

	params := db.ListStatusRepliesParams{
		StatusID:  pgtype.Int8{Int64: status.ID, Valid: true},
		AccountID: pgtype.Int8{Int64: author.ID, Valid: true},
		Limit:     5,
	}
	replies, err := c.store.ListStatusReplies(c.ctx, params)
	if err != nil {
		replies = nil
	}
	items := make([]any, 0, len(replies))
	for _, reply := range replies {
		uri := replyItemURI(c.localDomain, reply, author, true)
		if uri == "" {
			continue
		}
		items = append(items, uri)
	}

	next := ""
	if len(replies) > 0 {
		next = statusRepliesURLLocal(c.localDomain, author.Username, status.ID, true, replies[len(replies)-1].ID, false)
	} else {
		next = statusRepliesURLLocal(c.localDomain, author.Username, status.ID, true, 0, true)
	}

	firstPage := activitypub.CollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      pageURL,
				Type:    activitypub.TypeCollectionPage,
			},
			Items: items,
		},
		PartOf: baseURL,
		Next:   next,
	}

	return &activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      baseURL,
			Type:    activitypub.TypeCollection,
		},
		First: firstPage,
	}
}

func (c *statusContext) likesCollection(author db.Account, status db.Status) *activitypub.Collection {
	if author.Username == "" {
		return nil
	}
	collectionID := statusCounterURLLocal(c.localDomain, author.Username, status.ID, "likes")
	if collectionID == "" {
		return nil
	}
	count := 0
	if stat, ok := c.statsByStatus[status.ID]; ok {
		count = int(stat.FavouritesCount)
	}
	return &activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      collectionID,
			Type:    activitypub.TypeCollection,
		},
		TotalItems: count,
	}
}

func (c *statusContext) sharesCollection(author db.Account, status db.Status) *activitypub.Collection {
	if author.Username == "" {
		return nil
	}
	collectionID := statusCounterURLLocal(c.localDomain, author.Username, status.ID, "shares")
	if collectionID == "" {
		return nil
	}
	count := 0
	if stat, ok := c.statsByStatus[status.ID]; ok {
		count = int(stat.ReblogsCount)
	}
	return &activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      collectionID,
			Type:    activitypub.TypeCollection,
		},
		TotalItems: count,
	}
}

func (c *statusContext) isLocalStatus(status db.Status) bool {
	return status.Local.Valid && status.Local.Bool
}

func conversationURI(domain string, conversation db.Conversation) string {
	if conversation.Uri.Valid && strings.TrimSpace(conversation.Uri.String) != "" {
		return conversation.Uri.String
	}
	return contextURI(domain, conversation)
}

func contextURI(domain string, conversation db.Conversation) string {
	if domain == "" {
		return ""
	}
	if !conversation.ParentAccountID.Valid || !conversation.ParentStatusID.Valid {
		return ""
	}
	return fmt.Sprintf("https://%s/contexts/%d-%d", domain, conversation.ParentAccountID.Int64, conversation.ParentStatusID.Int64)
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
