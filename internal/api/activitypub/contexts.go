package activitypub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const contextDescendantsLimit = 60

// GetContext handles GET /contexts/:id
func (h *FederationHandler) GetContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conversation, parentAccountID, parentStatusID, err := h.loadContextConversation(ctx, web.Param(r, "id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Context not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to load context")
		return
	}

	minID := h.contextMinID(r)
	items, nextID, err := h.contextItems(ctx, conversation, minID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load context")
		return
	}

	baseURL := h.contextURL(parentAccountID, parentStatusID)
	itemsBaseURL := h.contextItemsURL(parentAccountID, parentStatusID, false, 0)
	pageID := h.contextItemsURL(parentAccountID, parentStatusID, false, minID)
	page := activitypub.CollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      pageID,
				Type:    activitypub.TypeCollectionPage,
			},
			Items: items,
		},
		PartOf: itemsBaseURL,
		Next:   h.contextNextURL(parentAccountID, parentStatusID, nextID),
	}

	var attributedTo any
	if h.useDB() {
		parentAccount, err := h.store.GetAccountByID(ctx, parentAccountID)
		if err == nil {
			if uri := actorURIForAccount(h.domain, parentAccount); uri != "" {
				attributedTo = uri
			}
		}
	}

	collection := activitypub.Collection{
		Object: activitypub.Object{
			Context:      activitypub.DefaultContext(),
			ID:           baseURL,
			Type:         activitypub.TypeCollection,
			AttributedTo: attributedTo,
		},
		First: page,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

// GetContextItems handles GET /contexts/:id/items
func (h *FederationHandler) GetContextItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conversation, parentAccountID, parentStatusID, err := h.loadContextConversation(ctx, web.Param(r, "id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Context not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to load context")
		return
	}

	minID := h.contextMinID(r)
	items, nextID, err := h.contextItems(ctx, conversation, minID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load context")
		return
	}

	pageRequested := contextPageRequested(r)
	baseURL := h.contextItemsURL(parentAccountID, parentStatusID, false, 0)
	pageID := h.contextItemsURL(parentAccountID, parentStatusID, pageRequested, minID)
	page := activitypub.CollectionPage{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      pageID,
				Type:    activitypub.TypeCollectionPage,
			},
			Items: items,
		},
		PartOf: baseURL,
		Next:   h.contextNextURL(parentAccountID, parentStatusID, nextID),
	}

	if pageRequested {
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		web.JSON(w, http.StatusOK, page)
		return
	}

	collection := activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      baseURL,
			Type:    activitypub.TypeCollection,
		},
		First: page,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) loadContextConversation(ctx context.Context, rawID string) (db.Conversation, int64, int64, error) {
	parentAccountID, parentStatusID, ok := parseContextID(rawID)
	if !ok || h.domain == "" {
		return db.Conversation{}, 0, 0, pgx.ErrNoRows
	}
	if !h.useDB() {
		return db.Conversation{ID: 0}, parentAccountID, parentStatusID, nil
	}

	conversation, err := h.store.GetConversationByParentAccountAndStatus(ctx, db.GetConversationByParentAccountAndStatusParams{
		ParentAccountID: pgtype.Int8{Int64: parentAccountID, Valid: true},
		ParentStatusID:  pgtype.Int8{Int64: parentStatusID, Valid: true},
	})
	if err != nil {
		return db.Conversation{}, 0, 0, err
	}
	return conversation, parentAccountID, parentStatusID, nil
}

func (h *FederationHandler) contextItems(ctx context.Context, conversation db.Conversation, minID int64) ([]any, int64, error) {
	if !h.useDB() || conversation.ID == 0 {
		return []any{}, 0, nil
	}

	statuses, err := h.store.ListConversationStatuses(ctx, db.ListConversationStatusesParams{
		ConversationID: pgtype.Int8{Int64: conversation.ID, Valid: true},
		MinID:          minID,
		Limit:          int32(contextDescendantsLimit),
	})
	if err != nil {
		return nil, 0, err
	}

	accountIDs := make([]int64, 0, len(statuses))
	seen := make(map[int64]bool)
	for _, status := range statuses {
		if seen[status.AccountID] {
			continue
		}
		seen[status.AccountID] = true
		accountIDs = append(accountIDs, status.AccountID)
	}

	accounts, err := h.store.GetAccountsByIDs(ctx, accountIDs)
	if err != nil {
		return nil, 0, err
	}
	accountsByID := make(map[int64]db.Account, len(accounts))
	for _, account := range accounts {
		accountsByID[account.ID] = account
	}

	items := make([]any, 0, len(statuses))
	for _, status := range statuses {
		author, ok := accountsByID[status.AccountID]
		uri := contextItemURI(h.domain, status, author, ok)
		if uri == "" {
			continue
		}
		items = append(items, uri)
	}

	if len(statuses) < contextDescendantsLimit {
		return items, 0, nil
	}
	return items, statuses[len(statuses)-1].ID, nil
}

func (h *FederationHandler) contextMinID(r *http.Request) int64 {
	minID, _ := parseMinID(web.QueryParam(r, "min_id"))
	return minID
}

func (h *FederationHandler) contextURL(parentAccountID, parentStatusID int64) string {
	if h.domain == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/contexts/%d-%d", h.domain, parentAccountID, parentStatusID)
}

func (h *FederationHandler) contextItemsURL(parentAccountID, parentStatusID int64, page bool, minID int64) string {
	base := fmt.Sprintf("https://%s/contexts/%d-%d/items", h.domain, parentAccountID, parentStatusID)
	params := url.Values{}
	if page {
		params.Set("page", "true")
	}
	if minID > 0 {
		params.Set("min_id", strconv.FormatInt(minID, 10))
	}
	encoded := params.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

func (h *FederationHandler) contextNextURL(parentAccountID, parentStatusID, nextID int64) string {
	if nextID == 0 {
		return ""
	}
	return h.contextItemsURL(parentAccountID, parentStatusID, true, nextID)
}

func contextPageRequested(r *http.Request) bool {
	value := strings.TrimSpace(web.QueryParam(r, "page"))
	if value == "" {
		return false
	}
	return !strings.EqualFold(value, "false")
}

func parseContextID(raw string) (int64, int64, bool) {
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	accountID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || accountID <= 0 {
		return 0, 0, false
	}
	statusID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || statusID <= 0 {
		return 0, 0, false
	}
	return accountID, statusID, true
}

func parseMinID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func contextItemURI(localDomain string, status db.Status, author db.Account, authorOK bool) string {
	if status.Local.Valid && status.Local.Bool {
		if authorOK {
			return statusURI(localDomain, author, status)
		}
		return ""
	}
	if status.Uri.Valid && status.Uri.String != "" {
		return status.Uri.String
	}
	if status.Url.Valid && status.Url.String != "" {
		return status.Url.String
	}
	if authorOK {
		return statusURI(localDomain, author, status)
	}
	return ""
}
