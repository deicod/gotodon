// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/pkg/oauth"
)

// StatusesHandler handles status-related endpoints
type StatusesHandler struct {
	// TODO: Add database queries dependency
}

// NewStatusesHandler creates a new statuses handler
func NewStatusesHandler() *StatusesHandler {
	return &StatusesHandler{}
}

// Status represents a Mastodon status in API responses
type Status struct {
	ID                 string         `json:"id"`
	CreatedAt          time.Time      `json:"created_at"`
	InReplyToID        *string        `json:"in_reply_to_id"`
	InReplyToAccountID *string        `json:"in_reply_to_account_id"`
	Sensitive          bool           `json:"sensitive"`
	SpoilerText        string         `json:"spoiler_text"`
	Visibility         string         `json:"visibility"`
	Language           *string        `json:"language"`
	URI                string         `json:"uri"`
	URL                *string        `json:"url"`
	RepliesCount       int64          `json:"replies_count"`
	ReblogsCount       int64          `json:"reblogs_count"`
	FavouritesCount    int64          `json:"favourites_count"`
	EditedAt           *time.Time     `json:"edited_at"`
	Content            string         `json:"content"`
	Reblog             *Status        `json:"reblog"`
	Application        *StatusApp     `json:"application,omitempty"`
	Account            Account        `json:"account"`
	MediaAttachments   []MediaAttach  `json:"media_attachments"`
	Mentions           []Mention      `json:"mentions"`
	Tags               []Tag          `json:"tags"`
	Emojis             []any          `json:"emojis"`
	Card               *PreviewCard   `json:"card"`
	Poll               *Poll          `json:"poll"`
	Favourited         bool           `json:"favourited"`
	Reblogged          bool           `json:"reblogged"`
	Muted              bool           `json:"muted"`
	Bookmarked         bool           `json:"bookmarked"`
	Pinned             bool           `json:"pinned,omitempty"`
	Filtered           []FilterResult `json:"filtered,omitempty"`
}

// StatusApp represents the application that posted a status
type StatusApp struct {
	Name    string  `json:"name"`
	Website *string `json:"website"`
}

// MediaAttach represents a media attachment
type MediaAttach struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"` // image, gifv, video, audio, unknown
	URL         string  `json:"url"`
	PreviewURL  string  `json:"preview_url"`
	RemoteURL   *string `json:"remote_url"`
	Description *string `json:"description"`
	Blurhash    *string `json:"blurhash"`
}

// Mention represents a mention in a status
type Mention struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	URL      string `json:"url"`
	Acct     string `json:"acct"`
}

// Tag represents a hashtag in a status
type Tag struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// PreviewCard represents a link preview card
type PreviewCard struct {
	URL          string  `json:"url"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Type         string  `json:"type"` // link, photo, video, rich
	AuthorName   string  `json:"author_name"`
	AuthorURL    string  `json:"author_url"`
	ProviderName string  `json:"provider_name"`
	ProviderURL  string  `json:"provider_url"`
	HTML         string  `json:"html"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Image        *string `json:"image"`
	Blurhash     *string `json:"blurhash"`
}

// Poll represents a poll attached to a status
type Poll struct {
	ID          string       `json:"id"`
	ExpiresAt   *time.Time   `json:"expires_at"`
	Expired     bool         `json:"expired"`
	Multiple    bool         `json:"multiple"`
	VotesCount  int          `json:"votes_count"`
	VotersCount *int         `json:"voters_count"`
	Voted       bool         `json:"voted"`
	OwnVotes    []int        `json:"own_votes"`
	Options     []PollOption `json:"options"`
	Emojis      []any        `json:"emojis"`
}

// PollOption represents a poll option
type PollOption struct {
	Title      string `json:"title"`
	VotesCount *int   `json:"votes_count"`
}

// FilterResult represents a filter match
type FilterResult struct {
	Filter         Filter   `json:"filter"`
	KeywordMatches []string `json:"keyword_matches,omitempty"`
	StatusMatches  []string `json:"status_matches,omitempty"`
}

// Filter represents a content filter
type Filter struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Context      []string `json:"context"`
	ExpiresAt    *string  `json:"expires_at"`
	FilterAction string   `json:"filter_action"`
}

// CreateStatusRequest is the request for POST /api/v1/statuses
type CreateStatusRequest struct {
	Status      string   `json:"status" form:"status"`
	InReplyToID *string  `json:"in_reply_to_id" form:"in_reply_to_id"`
	MediaIDs    []string `json:"media_ids" form:"media_ids[]"`
	Sensitive   bool     `json:"sensitive" form:"sensitive"`
	SpoilerText string   `json:"spoiler_text" form:"spoiler_text"`
	Visibility  string   `json:"visibility" form:"visibility"` // public, unlisted, private, direct
	Language    *string  `json:"language" form:"language"`
	Poll        *PollReq `json:"poll" form:"poll"`
}

// PollReq is the poll creation request
type PollReq struct {
	Options    []string `json:"options" form:"options[]"`
	ExpiresIn  int      `json:"expires_in" form:"expires_in"`
	Multiple   bool     `json:"multiple" form:"multiple"`
	HideTotals bool     `json:"hide_totals" form:"hide_totals"`
}

// Create handles POST /api/v1/statuses
func (h *StatusesHandler) Create(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Check scope
	scopes := middleware.GetScopes(c)
	if !oauth.HasScope(scopes, "write:statuses") {
		return echo.NewHTTPError(http.StatusForbidden, map[string]string{
			"error": "This action is outside the authorized scopes",
		})
	}

	var req CreateStatusRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate
	if req.Status == "" && len(req.MediaIDs) == 0 && req.Poll == nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Text can't be blank without media or poll",
		})
	}

	// Default visibility
	if req.Visibility == "" {
		req.Visibility = "public"
	}

	// Validate visibility
	validVisibilities := map[string]bool{"public": true, "unlisted": true, "private": true, "direct": true}
	if !validVisibilities[req.Visibility] {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Visibility is not included in the list",
		})
	}

	// TODO: Create status in database
	_ = accountID

	// Mock response
	now := time.Now()
	statusID := strconv.FormatInt(now.UnixNano(), 10)
	discoverable := true

	return c.JSON(http.StatusOK, Status{
		ID:              statusID,
		CreatedAt:       now,
		Sensitive:       req.Sensitive,
		SpoilerText:     req.SpoilerText,
		Visibility:      req.Visibility,
		Language:        req.Language,
		URI:             "https://example.com/users/user/statuses/" + statusID,
		URL:             ptrStr("https://example.com/@user/" + statusID),
		RepliesCount:    0,
		ReblogsCount:    0,
		FavouritesCount: 0,
		Content:         "<p>" + req.Status + "</p>",
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	})
}

// Get handles GET /api/v1/statuses/:id
func (h *StatusesHandler) Get(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// TODO: Fetch from database
	now := time.Now()
	discoverable := true

	return c.JSON(http.StatusOK, Status{
		ID:              id,
		CreatedAt:       now.Add(-time.Hour),
		Visibility:      "public",
		URI:             "https://example.com/users/user/statuses/" + id,
		URL:             ptrStr("https://example.com/@user/" + id),
		RepliesCount:    0,
		ReblogsCount:    0,
		FavouritesCount: 0,
		Content:         "<p>Hello, world!</p>",
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	})
}

// Delete handles DELETE /api/v1/statuses/:id
func (h *StatusesHandler) Delete(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Verify ownership and delete
	_ = accountID
	_ = id

	// Return deleted status with text/media for undo
	return c.JSON(http.StatusOK, map[string]any{
		"id":                id,
		"text":              "Hello, world!",
		"spoiler_text":      "",
		"visibility":        "public",
		"media_attachments": []any{},
		"poll":              nil,
	})
}

// Favourite handles POST /api/v1/statuses/:id/favourite
func (h *StatusesHandler) Favourite(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Add favourite in database
	_ = accountID

	// Return status with favourited=true
	status, _ := h.getStatusByID(id)
	status.Favourited = true
	status.FavouritesCount++

	return c.JSON(http.StatusOK, status)
}

// Unfavourite handles POST /api/v1/statuses/:id/unfavourite
func (h *StatusesHandler) Unfavourite(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	_ = accountID

	status, _ := h.getStatusByID(id)
	status.Favourited = false

	return c.JSON(http.StatusOK, status)
}

// Reblog handles POST /api/v1/statuses/:id/reblog
func (h *StatusesHandler) Reblog(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// TODO: Create reblog in database
	_ = accountID

	// Return the reblog status (wrapper)
	originalStatus, _ := h.getStatusByID(id)
	originalStatus.ReblogsCount++

	now := time.Now()
	reblogID := strconv.FormatInt(now.UnixNano(), 10)
	discoverable := true

	return c.JSON(http.StatusOK, Status{
		ID:         reblogID,
		CreatedAt:  now,
		Visibility: "public",
		URI:        "https://example.com/users/user/statuses/" + reblogID,
		Reblog:     &originalStatus,
		Reblogged:  true,
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	})
}

// Unreblog handles POST /api/v1/statuses/:id/unreblog
func (h *StatusesHandler) Unreblog(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	_ = accountID

	// Return original status with reblogged=false
	status, _ := h.getStatusByID(id)
	status.Reblogged = false

	return c.JSON(http.StatusOK, status)
}

// Bookmark handles POST /api/v1/statuses/:id/bookmark
func (h *StatusesHandler) Bookmark(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	_ = accountID

	status, _ := h.getStatusByID(id)
	status.Bookmarked = true

	return c.JSON(http.StatusOK, status)
}

// Unbookmark handles POST /api/v1/statuses/:id/unbookmark
func (h *StatusesHandler) Unbookmark(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	_ = accountID

	status, _ := h.getStatusByID(id)
	status.Bookmarked = false

	return c.JSON(http.StatusOK, status)
}

// Context handles GET /api/v1/statuses/:id/context
func (h *StatusesHandler) Context(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// TODO: Fetch ancestors and descendants from database
	return c.JSON(http.StatusOK, map[string][]Status{
		"ancestors":   {},
		"descendants": {},
	})
}

// getStatusByID is a helper to get a status (mock for now)
func (h *StatusesHandler) getStatusByID(id string) (Status, error) {
	now := time.Now()
	discoverable := true

	return Status{
		ID:              id,
		CreatedAt:       now.Add(-time.Hour),
		Visibility:      "public",
		URI:             "https://example.com/users/user/statuses/" + id,
		URL:             ptrStr("https://example.com/@user/" + id),
		RepliesCount:    0,
		ReblogsCount:    0,
		FavouritesCount: 0,
		Content:         "<p>Hello, world!</p>",
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []any{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	}, nil
}

func ptrStr(s string) *string {
	return &s
}

// RegisterRoutes registers the statuses routes
func (h *StatusesHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/statuses/:id", h.Get)
	g.GET("/statuses/:id/context", h.Context)
	// Authenticated routes are registered separately with middleware
}
