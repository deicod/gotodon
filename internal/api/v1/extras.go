// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// BookmarksHandler handles bookmarks endpoints
type BookmarksHandler struct {
	queries *db.Queries
}

// NewBookmarksHandler creates a new bookmarks handler
func NewBookmarksHandler(queries *db.Queries) *BookmarksHandler {
	return &BookmarksHandler{queries: queries}
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

		statuses := make([]Status, 0, len(bookmarks))
		for _, b := range bookmarks {
			dbStatus, err := h.queries.GetStatusByID(r.Context(), b.StatusID)
			if err != nil {
				continue
			}
			dbAcc, err := h.queries.GetAccountByID(r.Context(), dbStatus.AccountID)
			if err != nil {
				continue
			}
			statuses = append(statuses, dbStatusToBasicAPI(dbStatus, dbAcc))
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
	queries *db.Queries
}

// NewFavouritesHandler creates a new favourites handler
func NewFavouritesHandler(queries *db.Queries) *FavouritesHandler {
	return &FavouritesHandler{queries: queries}
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
		dbStatuses, err := h.queries.ListFavourites(r.Context(), db.ListFavouritesParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}

		statuses := make([]Status, 0, len(dbStatuses))
		for _, s := range dbStatuses {
			dbAcc, err := h.queries.GetAccountByID(r.Context(), s.AccountID)
			if err != nil {
				continue
			}
			statuses = append(statuses, dbStatusToBasicAPI(s, dbAcc))
		}
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
	queries *db.Queries
}

// NewMutesHandler creates a new mutes handler
func NewMutesHandler(queries *db.Queries) *MutesHandler {
	return &MutesHandler{queries: queries}
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
			accounts[i] = dbAccountToBasicAPI(a)
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
	queries *db.Queries
}

// NewBlocksHandler creates a new blocks handler
func NewBlocksHandler(queries *db.Queries) *BlocksHandler {
	return &BlocksHandler{queries: queries}
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
			accounts[i] = dbAccountToBasicAPI(a)
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
	queries *db.Queries
}

// NewCustomEmojisHandler creates a new custom emojis handler
func NewCustomEmojisHandler(queries *db.Queries) *CustomEmojisHandler {
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
		url := e.ImageRemoteUrl.String
		if url == "" {
			url = "/emoji/" + e.ImageFileName.String // Fallback or local path
		}
		resp[i] = CustomEmoji{
			Shortcode:       e.Shortcode,
			URL:             url,
			StaticURL:       url,
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
	queries *db.Queries
}

// NewConversationsHandler creates a new conversations handler
func NewConversationsHandler(queries *db.Queries) *ConversationsHandler {
	return &ConversationsHandler{queries: queries}
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

	limit := 20
	if l := web.QueryParam(r, "limit"); l != "" {
		if i, err := strconv.Atoi(l); err == nil && i > 0 {
			limit = i
		}
	}

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []Conversation{})
		return
	}

	dbConversations, err := h.queries.ListConversations(r.Context(), db.ListConversationsParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    0,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	conversations := make([]Conversation, 0, len(dbConversations))
	for _, c := range dbConversations {
		// Fetch last status
		status, err := h.queries.GetStatusByID(context.Background(), c.LastStatusID)
		if err != nil {
			continue
		}
		statusAccount, err := h.queries.GetAccountByID(context.Background(), c.LastStatusAccountID)
		if err != nil {
			continue
		}

		apiStatus := dbStatusToBasicAPI(status, statusAccount)

		// Determine participants (simplified: just the other person in the last status)
		accounts := []Account{}
		if c.LastStatusAccountID != accountID {
			accounts = append(accounts, dbAccountToBasicAPI(statusAccount))
		} else {
			// If I am the author, try to find who I replied to or mentioned
			// This is a simplification. Real implementation would query mentions.
		}

		conversations = append(conversations, Conversation{
			ID:         strconv.FormatInt(c.ID, 10),
			Unread:     false, // TODO: Implement unread logic
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

// Helper to convert db.Status to basic API Status
// Helper to convert db.Status to basic API Status
func dbStatusToBasicAPI(s db.Status, a db.Account) Status {
	visibility := "public"
	switch s.Visibility {
	case 1:
		visibility = "unlisted"
	case 2:
		visibility = "private"
	case 3:
		visibility = "direct"
	}

	return Status{
		ID:               strconv.FormatInt(s.ID, 10),
		Content:          s.Text,
		Visibility:       visibility,
		URI:              s.Uri.String,
		Account:          dbAccountToBasicAPI(a),
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	}
}

// Helper to convert db.Account to basic API Account
func dbAccountToBasicAPI(a db.Account) Account {
	discoverable := a.Discoverable.Valid && a.Discoverable.Bool
	return Account{
		ID:           strconv.FormatInt(a.ID, 10),
		Username:     a.Username,
		Acct:         a.Username,
		DisplayName:  a.DisplayName,
		Note:         a.Note,
		Locked:       a.Locked,
		Discoverable: &discoverable,
		Emojis:       []any{},
		Fields:       []Field{},
	}
}
