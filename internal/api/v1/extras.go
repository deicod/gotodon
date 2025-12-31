// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// BookmarksHandler handles bookmarks endpoints
type BookmarksHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewBookmarksHandler creates a new bookmarks handler
func NewBookmarksHandler(queries db.Querier, cfg *config.Config) *BookmarksHandler {
	return &BookmarksHandler{queries: queries, cfg: cfg}
}

func (h *BookmarksHandler) useDB() bool {
	return h.queries != nil
}

// List handles GET /api/v1/bookmarks
func (h *BookmarksHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	limit := parseBookmarksLimit(web.QueryParam(r, "limit"), 20, 40)

	if h.useDB() {
		bookmarks, err := h.queries.ListBookmarks(r.Context(), db.ListBookmarksParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}

		statusIDs := make([]int64, len(bookmarks))
		for i, b := range bookmarks {
			statusIDs[i] = b.StatusID
		}

		statuses := []Status{}
		if len(statusIDs) > 0 {
			dbStatuses, err := h.queries.GetStatusesByIDs(r.Context(), statusIDs)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to fetch statuses")
				return
			}
			statuses = SerializeStatuses(r.Context(), h.queries, h.cfg, dbStatuses)
		}
		web.JSON(w, http.StatusOK, statuses)
		return
	}

	web.JSON(w, http.StatusOK, []Status{})
}

func parseBookmarksLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// RegisterRoutes registers bookmarks routes
func (h *BookmarksHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/bookmarks", h.List)
}

// FavouritesHandler handles favourites endpoints
type FavouritesHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewFavouritesHandler creates a new favourites handler
func NewFavouritesHandler(queries db.Querier, cfg *config.Config) *FavouritesHandler {
	return &FavouritesHandler{queries: queries, cfg: cfg}
}

func (h *FavouritesHandler) useDB() bool {
	return h.queries != nil
}

// List handles GET /api/v1/favourites
func (h *FavouritesHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		limit := parseBookmarksLimit(web.QueryParam(r, "limit"), 20, 40)
		dbStatuses, err := h.queries.ListFavourites(r.Context(), db.ListFavouritesParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}

		// Optimize: Use SerializeStatuses directly on the results
		statuses := SerializeStatuses(r.Context(), h.queries, h.cfg, dbStatuses)
		web.JSON(w, http.StatusOK, statuses)
		return
	}
	web.JSON(w, http.StatusOK, []Status{})
}

// RegisterRoutes registers favourites routes
func (h *FavouritesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/favourites", h.List)
}

// MutesHandler handles mutes endpoints
type MutesHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewMutesHandler creates a new mutes handler
func NewMutesHandler(queries db.Querier, cfg *config.Config) *MutesHandler {
	return &MutesHandler{queries: queries, cfg: cfg}
}

func (h *MutesHandler) useDB() bool {
	return h.queries != nil
}

// List handles GET /api/v1/mutes
func (h *MutesHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		dbAccounts, err := h.queries.ListMutes(r.Context(), db.ListMutesParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Account{})
			return
		}

		accountsMap := serializeAccounts(r.Context(), h.queries, h.cfg, dbAccounts, true)
		accounts := make([]Account, 0, len(dbAccounts))
		for _, a := range dbAccounts {
			if acc, ok := accountsMap[a.ID]; ok {
				accounts = append(accounts, acc)
			}
		}
		web.JSON(w, http.StatusOK, accounts)
		return
	}
	web.JSON(w, http.StatusOK, []Account{})
}

// RegisterRoutes registers mutes routes
func (h *MutesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/mutes", h.List)
}

// BlocksHandler handles blocks endpoints
type BlocksHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewBlocksHandler creates a new blocks handler
func NewBlocksHandler(queries db.Querier, cfg *config.Config) *BlocksHandler {
	return &BlocksHandler{queries: queries, cfg: cfg}
}

func (h *BlocksHandler) useDB() bool {
	return h.queries != nil
}

// List handles GET /api/v1/blocks
func (h *BlocksHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if h.useDB() {
		dbAccounts, err := h.queries.ListBlocks(r.Context(), db.ListBlocksParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Account{})
			return
		}

		accountsMap := serializeAccounts(r.Context(), h.queries, h.cfg, dbAccounts, true)
		accounts := make([]Account, 0, len(dbAccounts))
		for _, a := range dbAccounts {
			if acc, ok := accountsMap[a.ID]; ok {
				accounts = append(accounts, acc)
			}
		}
		web.JSON(w, http.StatusOK, accounts)
		return
	}
	web.JSON(w, http.StatusOK, []Account{})
}

// RegisterRoutes registers blocks routes
func (h *BlocksHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/blocks", h.List)
}

// CustomEmojisHandler handles custom emojis endpoints
type CustomEmojisHandler struct {
	queries db.Querier
}

// NewCustomEmojisHandler creates a new custom emojis handler
func NewCustomEmojisHandler(queries db.Querier) *CustomEmojisHandler {
	return &CustomEmojisHandler{queries: queries}
}

// CustomEmoji represents a custom emoji
type CustomEmoji struct {
	Shortcode       string `json:"shortcode"`
	URL             string `json:"url"`
	StaticURL       string `json:"static_url"`
	VisibleInPicker bool   `json:"visible_in_picker"`
	Category        string `json:"category,omitempty"`
}

// List handles GET /api/v1/custom_emojis
func (h *CustomEmojisHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []CustomEmoji{})
		return
	}

	emojis, err := h.queries.ListCustomEmojis(r.Context())
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]CustomEmoji, len(emojis))
	for i, e := range emojis {
		resp[i] = CustomEmoji{
			Shortcode:       e.Shortcode,
			URL:             GetEmojiURL(e),
			StaticURL:       GetEmojiURL(e),
			VisibleInPicker: e.VisibleInPicker,
		}
	}

	web.JSON(w, http.StatusOK, resp)
}

// RegisterRoutes registers custom emojis routes
func (h *CustomEmojisHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/custom_emojis", h.List)
}

// ConversationsHandler handles conversations endpoints
type ConversationsHandler struct {
	queries db.Querier
	cfg     *config.Config
}

// NewConversationsHandler creates a new conversations handler
func NewConversationsHandler(queries db.Querier, cfg *config.Config) *ConversationsHandler {
	return &ConversationsHandler{queries: queries, cfg: cfg}
}

func (h *ConversationsHandler) useDB() bool {
	return h.queries != nil
}

// Conversation represents a direct message conversation
type Conversation struct {
	ID         string    `json:"id"`
	Unread     bool      `json:"unread"`
	Accounts   []Account `json:"accounts"`
	LastStatus *Status   `json:"last_status,omitempty"`
}

// List handles GET /api/v1/conversations
func (h *ConversationsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()
	scopes := middleware.GetScopes(ctx)
	if scopes != "" && !oauth.HasScope(scopes, "read:statuses") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	limit := parseBookmarksLimit(web.QueryParam(r, "limit"), 20, 40)

	if !h.useDB() {
		web.JSON(w, http.StatusOK, []Conversation{})
		return
	}

	dbConversations, err := h.queries.ListAccountConversations(ctx, db.ListAccountConversationsParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    0,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if len(dbConversations) == 0 {
		web.JSON(w, http.StatusOK, []Conversation{})
		return
	}

	statusIDs := make([]int64, 0, len(dbConversations))
	statusIDSeen := make(map[int64]bool)
	accountIDSeen := make(map[int64]bool)
	for _, c := range dbConversations {
		if c.LastStatusID.Valid {
			statusID := c.LastStatusID.Int64
			if !statusIDSeen[statusID] {
				statusIDs = append(statusIDs, statusID)
				statusIDSeen[statusID] = true
			}
		}
		for _, pid := range c.ParticipantAccountIds {
			if pid != accountID {
				accountIDSeen[pid] = true
			}
		}
	}

	statusMap := make(map[int64]db.Status)
	if len(statusIDs) > 0 {
		statuses, err := h.queries.GetStatusesByIDs(ctx, statusIDs)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch conversation statuses")
			return
		}
		for _, s := range statuses {
			statusMap[s.ID] = s
			accountIDSeen[s.AccountID] = true
			if s.InReplyToAccountID.Valid {
				accountIDSeen[s.InReplyToAccountID.Int64] = true
			}
		}
	}

	mentionsMap := make(map[int64][]db.Mention)
	if len(statusIDs) > 0 {
		mentions, err := h.queries.GetMentionsByStatusIDs(ctx, statusIDs)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch conversation mentions")
			return
		}
		for _, m := range mentions {
			mentionsMap[m.StatusID] = append(mentionsMap[m.StatusID], m)
			if m.AccountID != accountID {
				accountIDSeen[m.AccountID] = true
			}
		}
	}

	accountIDs := make([]int64, 0, len(accountIDSeen))
	for id := range accountIDSeen {
		accountIDs = append(accountIDs, id)
	}
	accountMap := make(map[int64]Account)
	if len(accountIDs) > 0 {
		accounts, err := h.queries.GetAccountsByIDs(ctx, accountIDs)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch conversation accounts")
			return
		}
		accountMap = serializeAccounts(ctx, h.queries, h.cfg, accounts, true)
	}

	conversations := make([]Conversation, 0, len(dbConversations))
	for _, c := range dbConversations {
		var dbStatus db.Status
		var lastStatus *Status
		if c.LastStatusID.Valid {
			if status, ok := statusMap[c.LastStatusID.Int64]; ok {
				dbStatus = status
				apiStatus := SerializeStatus(ctx, h.queries, h.cfg, status)
				lastStatus = &apiStatus
			}
		}

		accounts := []Account{}
		seen := make(map[int64]bool)
		for _, pid := range c.ParticipantAccountIds {
			if pid == accountID || seen[pid] {
				continue
			}
			seen[pid] = true
			if acc, ok := accountMap[pid]; ok {
				accounts = append(accounts, acc)
			}
		}

		if len(accounts) == 0 && lastStatus != nil {
			if dbStatus.AccountID != accountID {
				if acc, ok := accountMap[dbStatus.AccountID]; ok {
					accounts = append(accounts, acc)
				}
			} else if dbStatus.InReplyToAccountID.Valid {
				if acc, ok := accountMap[dbStatus.InReplyToAccountID.Int64]; ok {
					accounts = append(accounts, acc)
				}
			} else {
				for _, mention := range mentionsMap[dbStatus.ID] {
					if mention.AccountID == accountID {
						continue
					}
					if acc, ok := accountMap[mention.AccountID]; ok {
						accounts = append(accounts, acc)
						break
					}
				}
			}
		}

		conversations = append(conversations, Conversation{
			ID:         strconv.FormatInt(c.ID, 10),
			Unread:     c.Unread,
			Accounts:   accounts,
			LastStatus: lastStatus,
		})
	}

	web.JSON(w, http.StatusOK, conversations)
}

// Delete handles DELETE /api/v1/conversations/:id
func (h *ConversationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if !oauth.HasScope(scopes, "write:conversations") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}

	if h.useDB() {
		rows, err := h.queries.DeleteAccountConversation(r.Context(), db.DeleteAccountConversationParams{
			AccountID: accountID,
			ID:        id,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to delete conversation")
			return
		}
		if rows == 0 {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// MarkRead handles POST /api/v1/conversations/:id/read
func (h *ConversationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if !oauth.HasScope(scopes, "write:conversations") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}

	if !h.useDB() {
		web.JSON(w, http.StatusOK, Conversation{
			ID:         strconv.FormatInt(id, 10),
			Unread:     false,
			Accounts:   []Account{},
			LastStatus: nil,
		})
		return
	}

	ctx := r.Context()
	rows, err := h.queries.MarkConversationRead(ctx, db.MarkConversationReadParams{
		AccountID: accountID,
		ID:        id,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to update conversation")
		return
	}
	if rows == 0 {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	conv, err := h.queries.GetAccountConversation(ctx, db.GetAccountConversationParams{
		AccountID: accountID,
		ID:        id,
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	resp, err := h.buildConversation(ctx, accountID, conv)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load conversation")
		return
	}

	web.JSON(w, http.StatusOK, resp)
}

// MarkUnread handles POST /api/v1/conversations/:id/unread
func (h *ConversationsHandler) MarkUnread(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if !oauth.HasScope(scopes, "write:conversations") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}

	if !h.useDB() {
		web.JSON(w, http.StatusOK, Conversation{
			ID:         strconv.FormatInt(id, 10),
			Unread:     true,
			Accounts:   []Account{},
			LastStatus: nil,
		})
		return
	}

	ctx := r.Context()
	rows, err := h.queries.MarkConversationUnread(ctx, db.MarkConversationUnreadParams{
		AccountID: accountID,
		ID:        id,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to update conversation")
		return
	}
	if rows == 0 {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	conv, err := h.queries.GetAccountConversation(ctx, db.GetAccountConversationParams{
		AccountID: accountID,
		ID:        id,
	})
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	resp, err := h.buildConversation(ctx, accountID, conv)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load conversation")
		return
	}

	web.JSON(w, http.StatusOK, resp)
}

// RegisterRoutes registers conversations routes
func (h *ConversationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/conversations", h.List)
	mux.HandleFunc("DELETE /api/v1/conversations/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/conversations/{id}/read", h.MarkRead)
	mux.HandleFunc("POST /api/v1/conversations/{id}/unread", h.MarkUnread)
}

func (h *ConversationsHandler) buildConversation(ctx context.Context, accountID int64, conv db.AccountConversation) (Conversation, error) {
	var dbStatus db.Status
	var lastStatus *Status
	if conv.LastStatusID.Valid {
		status, err := h.queries.GetStatusByID(ctx, conv.LastStatusID.Int64)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, err
		}
		if err == nil {
			dbStatus = status
			apiStatus := SerializeStatus(ctx, h.queries, h.cfg, status)
			lastStatus = &apiStatus
		}
	}

	uniqueIDs := make(map[int64]bool)
	accountIDs := make([]int64, 0, len(conv.ParticipantAccountIds))
	for _, pid := range conv.ParticipantAccountIds {
		if pid == accountID || uniqueIDs[pid] {
			continue
		}
		uniqueIDs[pid] = true
		accountIDs = append(accountIDs, pid)
	}

	if len(accountIDs) == 0 && lastStatus != nil {
		if dbStatus.AccountID != accountID {
			accountIDs = append(accountIDs, dbStatus.AccountID)
		} else if dbStatus.InReplyToAccountID.Valid {
			accountIDs = append(accountIDs, dbStatus.InReplyToAccountID.Int64)
		} else {
			mentions, err := h.queries.GetMentionsByStatusID(ctx, dbStatus.ID)
			if err != nil {
				return Conversation{}, err
			}
			for _, mention := range mentions {
				if mention.AccountID != accountID {
					accountIDs = append(accountIDs, mention.AccountID)
					break
				}
			}
		}
	}

	accounts := []Account{}
	if len(accountIDs) > 0 {
		dbAccounts, err := h.queries.GetAccountsByIDs(ctx, accountIDs)
		if err != nil {
			return Conversation{}, err
		}
		accountMap := serializeAccounts(ctx, h.queries, h.cfg, dbAccounts, true)
		for _, pid := range accountIDs {
			if acc, ok := accountMap[pid]; ok {
				accounts = append(accounts, acc)
			}
		}
	}

	return Conversation{
		ID:         strconv.FormatInt(conv.ID, 10),
		Unread:     conv.Unread,
		Accounts:   accounts,
		LastStatus: lastStatus,
	}, nil
}

// GetEmojiURL returns the URL for a custom emoji
func GetEmojiURL(emoji db.CustomEmoji) string {
	if emoji.ImageRemoteUrl.Valid {
		return emoji.ImageRemoteUrl.String
	}
	// Assuming local storage URL construction
	return "/files/" + emoji.ImageFileName.String
}
