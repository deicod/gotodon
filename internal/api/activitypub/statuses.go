package activitypub

import (
	"context"
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

const statusRepliesLimit = 60

// GetStatus handles GET /users/:username/statuses/:statusID
func (h *FederationHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	account, status, err := h.loadAccountStatus(r)
	if !isActivityPubRequest(r) {
		if err == nil && account.Username != "" {
			redirectURL := fmt.Sprintf("https://%s/@%s/%d", h.domain, account.Username, status.ID)
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}
		username := accountParam(r)
		statusID := web.Param(r, "statusID")
		if username != "" && statusID != "" {
			redirectURL := fmt.Sprintf("https://%s/@%s/%s", h.domain, username, statusID)
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	if !statusDistributable(status) {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	if status.ReblogOfID.Valid {
		h.redirectToOriginalStatus(w, r, status)
		return
	}

	ctx, err := h.buildStatusContext(r.Context(), []db.Status{status})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render status")
		return
	}
	note, err := ctx.noteForStatus(account, status)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render status")
		return
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, note)
}

// GetStatusActivity handles GET /users/:username/statuses/:statusID/activity
func (h *FederationHandler) GetStatusActivity(w http.ResponseWriter, r *http.Request) {
	account, status, err := h.loadAccountStatus(r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}
	if !statusDistributable(status) {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	ctx, err := h.buildStatusContext(r.Context(), []db.Status{status})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render status")
		return
	}
	activities, err := ctx.activitiesForStatuses(account, []db.Status{status})
	if err != nil || len(activities) == 0 {
		web.JSONError(w, http.StatusInternalServerError, "Failed to render status")
		return
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, activities[0])
}

// GetStatusReplies handles GET /users/:username/statuses/:statusID/replies
func (h *FederationHandler) GetStatusReplies(w http.ResponseWriter, r *http.Request) {
	account, status, err := h.loadAccountStatus(r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}
	if !statusDistributable(status) {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	pageRequested := contextPageRequested(r)
	minID, _ := parseMinID(web.QueryParam(r, "min_id"))
	onlyOtherAccounts := truthyParam(web.QueryParam(r, "only_other_accounts"))

	params := db.ListStatusRepliesParams{
		StatusID: pgtype.Int8{Int64: status.ID, Valid: true},
		Limit:    statusRepliesLimit,
	}
	if minID > 0 {
		params.MinID = pgtype.Int8{Int64: minID, Valid: true}
	}
	if onlyOtherAccounts {
		params.ExcludeAccountID = pgtype.Int8{Int64: account.ID, Valid: true}
	} else {
		params.AccountID = pgtype.Int8{Int64: account.ID, Valid: true}
	}

	replies, err := h.store.ListStatusReplies(r.Context(), params)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load replies")
		return
	}

	items := h.replyItems(r.Context(), replies)
	baseURL := h.statusRepliesURL(account.Username, status.ID, false, 0, false)
	pageID := h.statusRepliesURL(account.Username, status.ID, pageRequested, minID, onlyOtherAccounts)
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
		Next:   h.repliesNextURL(account, status.ID, replies, onlyOtherAccounts),
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

// GetStatusLikes handles GET /users/:username/statuses/:statusID/likes
func (h *FederationHandler) GetStatusLikes(w http.ResponseWriter, r *http.Request) {
	h.renderStatusCounter(w, r, "likes")
}

// GetStatusShares handles GET /users/:username/statuses/:statusID/shares
func (h *FederationHandler) GetStatusShares(w http.ResponseWriter, r *http.Request) {
	h.renderStatusCounter(w, r, "shares")
}

func (h *FederationHandler) renderStatusCounter(w http.ResponseWriter, r *http.Request, kind string) {
	account, status, err := h.loadAccountStatus(r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}
	if !statusDistributable(status) {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	count := int64(0)
	if h.store != nil {
		stats, err := h.store.GetStatusStats(r.Context(), status.ID)
		if err == nil {
			switch kind {
			case "likes":
				count = stats.FavouritesCount
			case "shares":
				count = stats.ReblogsCount
			}
		}
	}

	baseURL := h.statusCounterURL(account.Username, status.ID, kind)
	collection := activitypub.Collection{
		Object: activitypub.Object{
			Context: activitypub.DefaultContext(),
			ID:      baseURL,
			Type:    activitypub.TypeCollection,
		},
		TotalItems: int(count),
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) loadAccountStatus(r *http.Request) (db.Account, db.Status, error) {
	if !h.useDB() {
		return db.Account{}, db.Status{}, pgx.ErrNoRows
	}
	statusID, err := web.Int64Param(r, "statusID")
	if err != nil {
		return db.Account{}, db.Status{}, err
	}
	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		return db.Account{}, db.Status{}, err
	}
	status, err := h.store.GetStatusByID(r.Context(), statusID)
	if err != nil {
		return db.Account{}, db.Status{}, err
	}
	if status.AccountID != account.ID {
		return db.Account{}, db.Status{}, pgx.ErrNoRows
	}
	return account, status, nil
}

func (h *FederationHandler) redirectToOriginalStatus(w http.ResponseWriter, r *http.Request, status db.Status) {
	if !status.ReblogOfID.Valid {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}
	original, err := h.store.GetStatusByID(r.Context(), status.ReblogOfID.Int64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}
	var originalAuthor db.Account
	if h.store != nil {
		if account, err := h.store.GetAccountByID(r.Context(), original.AccountID); err == nil {
			originalAuthor = account
		}
	}

	redirectURL := ""
	if originalAuthor.ID != 0 {
		redirectURL = statusObjectURI(h.domain, originalAuthor, original)
	}
	if redirectURL == "" {
		if original.Uri.Valid {
			redirectURL = original.Uri.String
		} else if original.Url.Valid {
			redirectURL = original.Url.String
		}
	}
	if redirectURL == "" {
		web.JSONError(w, http.StatusNotFound, "Status not found")
		return
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func statusDistributable(status db.Status) bool {
	return status.Visibility == visibilityPublic || status.Visibility == visibilityUnlisted
}

func (h *FederationHandler) replyItems(ctx context.Context, replies []db.Status) []any {
	if len(replies) == 0 {
		return []any{}
	}
	accountIDs := make([]int64, 0, len(replies))
	seen := make(map[int64]bool)
	for _, reply := range replies {
		if seen[reply.AccountID] {
			continue
		}
		seen[reply.AccountID] = true
		accountIDs = append(accountIDs, reply.AccountID)
	}
	accounts, err := h.store.GetAccountsByIDs(ctx, accountIDs)
	if err != nil {
		return []any{}
	}
	accountsByID := make(map[int64]db.Account, len(accounts))
	for _, account := range accounts {
		accountsByID[account.ID] = account
	}

	items := make([]any, 0, len(replies))
	for _, reply := range replies {
		author, ok := accountsByID[reply.AccountID]
		uri := replyItemURI(h.domain, reply, author, ok)
		if uri == "" {
			continue
		}
		items = append(items, uri)
	}
	return items
}

func (h *FederationHandler) repliesNextURL(account db.Account, statusID int64, replies []db.Status, onlyOtherAccounts bool) string {
	if onlyOtherAccounts {
		if len(replies) < statusRepliesLimit {
			return ""
		}
		lastID := replies[len(replies)-1].ID
		return h.statusRepliesURL(account.Username, statusID, true, lastID, true)
	}
	if len(replies) < statusRepliesLimit {
		return h.statusRepliesURL(account.Username, statusID, true, 0, true)
	}
	lastID := replies[len(replies)-1].ID
	return h.statusRepliesURL(account.Username, statusID, true, lastID, false)
}

func (h *FederationHandler) statusRepliesURL(username string, statusID int64, page bool, minID int64, onlyOtherAccounts bool) string {
	if h.domain == "" {
		return ""
	}
	base := fmt.Sprintf("https://%s/users/%s/statuses/%d/replies", h.domain, username, statusID)
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

func (h *FederationHandler) statusCounterURL(username string, statusID int64, kind string) string {
	if h.domain == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/users/%s/statuses/%d/%s", h.domain, username, statusID, kind)
}

func replyItemURI(localDomain string, status db.Status, author db.Account, authorOK bool) string {
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

func truthyParam(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return !strings.EqualFold(value, "false")
}
