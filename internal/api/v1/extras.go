// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
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

		accounts := make([]Account, len(dbAccounts))
		for i, a := range dbAccounts {
			accounts[i] = SerializeAccount(h.cfg, a)
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

		accounts := make([]Account, len(dbAccounts))
		for i, a := range dbAccounts {
			accounts[i] = SerializeAccount(h.cfg, a)
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
	limit := parseBookmarksLimit(web.QueryParam(r, "limit"), 20, 40)

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []Conversation{})
		return
	}

	dbConversations, err := h.queries.ListConversations(ctx, db.ListConversationsParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    0,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Bulk fetch statuses
	statusIDs := make([]int64, 0, len(dbConversations))
	for _, c := range dbConversations {
		statusIDs = append(statusIDs, c.LastStatusID)
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
		}
	}

	// Account fetching logic is now handled by the getAccount helper and on-demand fetching
	// to avoid complex pre-fetching logic without a dedicated GetAccountsByIDs query.

	// Local cache for accounts to minimize DB hits
	cachedAccounts := make(map[int64]Account)
	getAccount := func(id int64) (Account, error) {
		if acc, ok := cachedAccounts[id]; ok {
			return acc, nil
		}
		dbAcc, err := h.queries.GetAccountByID(ctx, id)
		if err != nil {
			return Account{}, err
		}
		acc := SerializeAccount(h.cfg, dbAcc)
		cachedAccounts[id] = acc
		return acc, nil
	}

	conversations := make([]Conversation, 0, len(dbConversations))
	for _, c := range dbConversations {
		status, ok := statusMap[c.LastStatusID]
		if !ok {
			continue
		}

		apiStatus := SerializeStatus(ctx, h.queries, h.cfg, status)

		accounts := []Account{}
		if c.LastStatusAccountID != accountID {
			// Sender is the other person
			if acc, err := getAccount(c.LastStatusAccountID); err == nil {
				accounts = append(accounts, acc)
			}
		} else {
			// I am the sender, find who I'm talking to
			if status.InReplyToAccountID.Valid {
				if acc, err := getAccount(status.InReplyToAccountID.Int64); err == nil {
					accounts = append(accounts, acc)
				}
			} else {
				// Check mentions
				mentions, err := h.queries.GetMentionsByStatusID(ctx, status.ID)
				if err == nil && len(mentions) > 0 {
					// Just take the first mention for now as per Mastodon simplified logic often used
					if acc, err := getAccount(mentions[0].AccountID); err == nil {
						accounts = append(accounts, acc)
					}
				}
			}
		}

		conversations = append(conversations, Conversation{
			ID:         strconv.FormatInt(c.ID, 10),
			Unread:     false,
			Accounts:   accounts,
			LastStatus: &apiStatus,
		})
	}

	web.JSON(w, http.StatusOK, conversations)
}

// Delete handles DELETE /api/v1/conversations/:id
func (h *ConversationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid ID")
		return
	}

	if h.queries != nil {
		// Just delete the conversation entry for now. In Mastodon this hides it.
		_ = h.queries.DeleteConversation(r.Context(), id)
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// MarkRead handles POST /api/v1/conversations/:id/read
func (h *ConversationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// Return empty conversation to indicate marked as read
	web.JSON(w, http.StatusOK, Conversation{
		ID:       web.Param(r, "id"),
		Unread:   false,
		Accounts: []Account{},
	})
}

// RegisterRoutes registers conversations routes
func (h *ConversationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/conversations", h.List)
	mux.HandleFunc("DELETE /api/v1/conversations/{id}", h.Delete)
	mux.HandleFunc("POST /api/v1/conversations/{id}/read", h.MarkRead)
}

// GetEmojiURL returns the URL for a custom emoji
func GetEmojiURL(emoji db.CustomEmoji) string {
	if emoji.ImageRemoteUrl.Valid {
		return emoji.ImageRemoteUrl.String
	}
	// Assuming local storage URL construction
	return "/files/" + emoji.ImageFileName.String
}
