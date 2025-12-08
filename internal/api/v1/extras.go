// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
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
func (h *BookmarksHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	limit := parseBookmarksLimit(c.QueryParam("limit"), 20, 40)

	if h.useDB() {
		bookmarks, err := h.queries.ListBookmarks(c.Request().Context(), db.ListBookmarksParams{
			AccountID: accountID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Status{})
		}

		statuses := make([]Status, 0, len(bookmarks))
		for _, b := range bookmarks {
			dbStatus, err := h.queries.GetStatusByID(c.Request().Context(), b.StatusID)
			if err != nil {
				continue
			}
			dbAcc, err := h.queries.GetAccountByID(c.Request().Context(), dbStatus.AccountID)
			if err != nil {
				continue
			}
			statuses = append(statuses, dbStatusToBasicAPI(dbStatus, dbAcc))
		}
		return c.JSON(http.StatusOK, statuses)
	}

	return c.JSON(http.StatusOK, []Status{})
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
func (h *BookmarksHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/bookmarks", h.List)
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
func (h *FavouritesHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	if h.useDB() {
		dbStatuses, err := h.queries.ListFavourites(c.Request().Context(), db.ListFavouritesParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Status{})
		}

		statuses := make([]Status, 0, len(dbStatuses))
		for _, s := range dbStatuses {
			dbAcc, err := h.queries.GetAccountByID(c.Request().Context(), s.AccountID)
			if err != nil {
				continue
			}
			statuses = append(statuses, dbStatusToBasicAPI(s, dbAcc))
		}
		return c.JSON(http.StatusOK, statuses)
	}
	return c.JSON(http.StatusOK, []Status{})
}

// RegisterRoutes registers favourites routes
func (h *FavouritesHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/favourites", h.List)
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
func (h *MutesHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	if h.useDB() {
		dbAccounts, err := h.queries.ListMutes(c.Request().Context(), db.ListMutesParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Account{})
		}

		accounts := make([]Account, len(dbAccounts))
		for i, a := range dbAccounts {
			accounts[i] = dbAccountToBasicAPI(a)
		}
		return c.JSON(http.StatusOK, accounts)
	}
	return c.JSON(http.StatusOK, []Account{})
}

// RegisterRoutes registers mutes routes
func (h *MutesHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/mutes", h.List)
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
func (h *BlocksHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	if h.useDB() {
		dbAccounts, err := h.queries.ListBlocks(c.Request().Context(), db.ListBlocksParams{
			AccountID: accountID,
			Limit:     40,
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Account{})
		}

		accounts := make([]Account, len(dbAccounts))
		for i, a := range dbAccounts {
			accounts[i] = dbAccountToBasicAPI(a)
		}
		return c.JSON(http.StatusOK, accounts)
	}
	return c.JSON(http.StatusOK, []Account{})
}

// RegisterRoutes registers blocks routes
func (h *BlocksHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/blocks", h.List)
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
func (h *CustomEmojisHandler) List(c echo.Context) error {
	if h.queries == nil {
		return c.JSON(http.StatusOK, []CustomEmoji{})
	}

	emojis, err := h.queries.ListCustomEmojis(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
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

	return c.JSON(http.StatusOK, resp)
}

// RegisterRoutes registers custom emojis routes
func (h *CustomEmojisHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/custom_emojis", h.List)
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
func (h *ConversationsHandler) List(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	limit := 20
	if l := c.QueryParam("limit"); l != "" {
		if i, err := strconv.Atoi(l); err == nil && i > 0 {
			limit = i
		}
	}

	if h.queries == nil {
		return c.JSON(http.StatusOK, []Conversation{})
	}

	dbConversations, err := h.queries.ListConversations(c.Request().Context(), db.ListConversationsParams{
		AccountID: accountID,
		Limit:     int32(limit),
		Offset:    0,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
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

	return c.JSON(http.StatusOK, conversations)
}

// Delete handles DELETE /api/v1/conversations/:id
func (h *ConversationsHandler) Delete(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid ID"})
	}

	if h.queries != nil {
		// Just delete the conversation entry for now. In Mastodon this hides it.
		_ = h.queries.DeleteConversation(c.Request().Context(), id)
	}

	return c.JSON(http.StatusOK, map[string]any{})
}

// MarkRead handles POST /api/v1/conversations/:id/read
func (h *ConversationsHandler) MarkRead(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// Return empty conversation to indicate marked as read
	return c.JSON(http.StatusOK, Conversation{
		ID:       c.Param("id"),
		Unread:   false,
		Accounts: []Account{},
	})
}

// RegisterRoutes registers conversations routes
func (h *ConversationsHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/conversations", h.List)
	g.DELETE("/conversations/:id", h.Delete)
	g.POST("/conversations/:id/read", h.MarkRead)
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
