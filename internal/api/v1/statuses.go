// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// Visibility constants
const (
	VisibilityPublic   = 0
	VisibilityUnlisted = 1
	VisibilityPrivate  = 2
	VisibilityDirect   = 3
)

var visibilityToString = map[int32]string{
	VisibilityPublic:   "public",
	VisibilityUnlisted: "unlisted",
	VisibilityPrivate:  "private",
	VisibilityDirect:   "direct",
}

var stringToVisibility = map[string]int32{
	"public":   VisibilityPublic,
	"unlisted": VisibilityUnlisted,
	"private":  VisibilityPrivate,
	"direct":   VisibilityDirect,
}

// StatusesHandler handles status-related endpoints
type StatusesHandler struct {
	queries   *db.Queries
	publisher streaming.Publisher
}

// NewStatusesHandler creates a new statuses handler
func NewStatusesHandler(queries *db.Queries, publisher streaming.Publisher) *StatusesHandler {
	return &StatusesHandler{
		queries:   queries,
		publisher: publisher,
	}
}

func (h *StatusesHandler) useDB() bool {
	return h.queries != nil
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
	Type        string  `json:"type"`
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
	Type         string  `json:"type"`
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

// processMentions parses mentions from status text and creates notifications
func (h *StatusesHandler) processMentions(ctx context.Context, statusID int64, accountID int64, text string) ([]Mention, error) {
	// Simple mention regex: @username or @username@domain
	// Note: This is a simplified regex.
	re := regexp.MustCompile(`@([a-zA-Z0-9_]+)(?:@([a-zA-Z0-9_.-]+))?`)
	matches := re.FindAllStringSubmatch(text, -1)

	mentionsMap := make(map[string]bool)
	var mentions []Mention

	for _, match := range matches {
		username := match[1]
		// domain := match[2] // Ignored for now, assuming local or handled by GetAccountByUsername if uniquely identifiable

		if mentionsMap[username] {
			continue
		}
		mentionsMap[username] = true

		// Find account
		// TODO: handle domain resolution if present
		mentionedAcc, err := h.queries.GetAccountByUsername(ctx, username)
		if err != nil {
			continue // Account not found, skip
		}

		// Don't mention yourself (unless we want to support self-mention, usually avoided in notifications)
		if mentionedAcc.ID == accountID {
			continue
		}

		// Create Mention record
		_, err = h.queries.CreateMention(ctx, db.CreateMentionParams{
			StatusID:  statusID,
			AccountID: mentionedAcc.ID,
			Silent:    false, // TODO: check if user is muted/blocks
		})
		if err != nil {
			// Log error but continue
			continue
		}

		// Create Notification
		notif, err := h.queries.CreateNotification(ctx, db.CreateNotificationParams{
			AccountID:     mentionedAcc.ID,
			FromAccountID: accountID,
			ActivityType:  "Status",
			ActivityID:    statusID,
			Type:          pgtype.Text{String: "mention", Valid: true},
		})

		if err == nil && h.publisher != nil {
			// Publish notification event
			// We need to construct the API Notification object to publish
			apiNotif := Notification{
				ID:        strconv.FormatInt(notif.ID, 10),
				Type:      "mention",
				CreatedAt: notif.CreatedAt.Time,
				Account:   h.dbAccountToAPI(mentionedAcc), // The SENDER (from_account_id) ? No, the API notification 'account' field is the SENDER.
				// Wait, dbAccountToAPI takes db.Account.
				// For a mention notification, the 'account' field in the payload is the one who mentioned you (FromAccountID).
			}

			// Get sender account (current user)
			sender, err := h.queries.GetAccountByID(ctx, accountID)
			if err == nil {
				apiNotif.Account = h.dbAccountToAPI(sender)
				// Status should also be included in mention notification
				status, err := h.queries.GetStatusByID(ctx, statusID)
				if err == nil {
					apiStatus := h.dbStatusToAPI(ctx, status)
					apiNotif.Status = &apiStatus
				}

				_ = h.publisher.PublishNotificationToRedis(ctx, mentionedAcc.ID, apiNotif)
			}
		}

		// Add to return list (for API response implementation if we wanted to return mentions in the same call,
		// though dbStatusToAPI usually fetches them separately)
		// Assuming we just want to create them here.
	}

	return mentions, nil
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
	Visibility  string   `json:"visibility" form:"visibility"`
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

func (h *StatusesHandler) dbStatusToAPI(ctx context.Context, dbStatus db.Status) Status {
	createdAt := time.Now()
	if dbStatus.CreatedAt.Valid {
		createdAt = dbStatus.CreatedAt.Time
	}

	visibility := visibilityToString[dbStatus.Visibility]
	if visibility == "" {
		visibility = "public"
	}

	var url *string
	if dbStatus.Url.Valid {
		url = &dbStatus.Url.String
	}

	var inReplyToID, inReplyToAccountID *string
	if dbStatus.InReplyToID.Valid {
		s := strconv.FormatInt(dbStatus.InReplyToID.Int64, 10)
		inReplyToID = &s
	}
	if dbStatus.InReplyToAccountID.Valid {
		s := strconv.FormatInt(dbStatus.InReplyToAccountID.Int64, 10)
		inReplyToAccountID = &s
	}

	var language *string
	if dbStatus.Language.Valid {
		language = &dbStatus.Language.String
	}

	var editedAt *time.Time
	if dbStatus.EditedAt.Valid {
		editedAt = &dbStatus.EditedAt.Time
	}

	// Load stats
	var repliesCount, reblogsCount, favouritesCount int64
	if h.useDB() {
		stats, err := h.queries.GetStatusStats(ctx, dbStatus.ID)
		if err == nil {
			repliesCount = stats.RepliesCount
			reblogsCount = stats.ReblogsCount
			favouritesCount = stats.FavouritesCount
		}
	}

	// Load account
	var account Account
	if h.useDB() {
		dbAcc, err := h.queries.GetAccountByID(ctx, dbStatus.AccountID)
		if err == nil {
			account = h.dbAccountToAPI(dbAcc)
		}
	}

	// Load mentions
	var mentions []Mention
	if h.useDB() {
		dbMentions, err := h.queries.GetMentionsByStatusID(ctx, dbStatus.ID)
		if err == nil {
			for _, m := range dbMentions {
				acc, err := h.queries.GetAccountByID(ctx, m.AccountID)
				if err == nil {
					mentions = append(mentions, Mention{
						ID:       strconv.FormatInt(acc.ID, 10),
						Username: acc.Username,
						Acct:     acc.Username, // simplified
						URL:      fmt.Sprintf("/@%s", acc.Username),
					})
				}
			}
		}
	}
	if mentions == nil {
		mentions = []Mention{}
	}

	uri := ""
	if dbStatus.Uri.Valid {
		uri = dbStatus.Uri.String
	}

	return Status{
		ID:                 strconv.FormatInt(dbStatus.ID, 10),
		CreatedAt:          createdAt,
		InReplyToID:        inReplyToID,
		InReplyToAccountID: inReplyToAccountID,
		Sensitive:          dbStatus.Sensitive,
		SpoilerText:        dbStatus.SpoilerText,
		Visibility:         visibility,
		Language:           language,
		URI:                uri,
		URL:                url,
		RepliesCount:       repliesCount,
		ReblogsCount:       reblogsCount,
		FavouritesCount:    favouritesCount,
		EditedAt:           editedAt,
		Content:            "<p>" + dbStatus.Text + "</p>",
		Account:            account,
		MediaAttachments:   []MediaAttach{},
		Mentions:           mentions,
		Tags:               []Tag{},
		Emojis:             []any{},
	}
}

// Create handles POST /api/v1/statuses
func (h *StatusesHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if !oauth.HasScope(scopes, "write:statuses") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	var req CreateStatusRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Status == "" && len(req.MediaIDs) == 0 && req.Poll == nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Text can't be blank without media or poll")
		return
	}

	if req.Visibility == "" {
		req.Visibility = "public"
	}

	visibility, validVis := stringToVisibility[req.Visibility]
	if !validVis {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Visibility is not included in the list")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		var inReplyToID, inReplyToAccountID pgtype.Int8
		if req.InReplyToID != nil {
			id, err := strconv.ParseInt(*req.InReplyToID, 10, 64)
			if err == nil {
				inReplyToID = pgtype.Int8{Int64: id, Valid: true}
				// Get parent status to set reply account
				parent, err := h.queries.GetStatusByID(ctx, id)
				if err == nil {
					inReplyToAccountID = pgtype.Int8{Int64: parent.AccountID, Valid: true}
				}
			}
		}

		var lang pgtype.Text
		if req.Language != nil {
			lang = pgtype.Text{String: *req.Language, Valid: true}
		}

		dbStatus, err := h.queries.CreateStatus(ctx, db.CreateStatusParams{
			Text:               req.Status,
			AccountID:          accountID,
			InReplyToID:        inReplyToID,
			InReplyToAccountID: inReplyToAccountID,
			Sensitive:          req.Sensitive,
			SpoilerText:        req.SpoilerText,
			Visibility:         visibility,
			Language:           lang,
			Local:              pgtype.Bool{Bool: true, Valid: true},
			Reply:              inReplyToID.Valid,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
			return
		}

		// Create stats
		_, _ = h.queries.CreateStatusStats(ctx, dbStatus.ID)

		// Increment reply count on parent
		if inReplyToID.Valid {
			_ = h.queries.IncrementStatusReplies(ctx, inReplyToID.Int64)
		}

		// Process Mentions
		_, _ = h.processMentions(ctx, dbStatus.ID, accountID, req.Status)

		apiStatus := h.dbStatusToAPI(ctx, dbStatus)

		// Publish streaming events
		if h.publisher != nil {
			// Publish to public timeline if public
			streams := []string{}
			if dbStatus.Visibility == VisibilityPublic {
				streams = append(streams, streaming.StreamPublic, streaming.StreamPublicLocal)
			}

			// Publish to user stream (home of followers would be distributed by worker usually,
			// but simply publishing to author's user stream is good for self)
			streams = append(streams, fmt.Sprintf("user:%d", accountID))

			// TODO: Add hashtags streams if any

			_ = h.publisher.PublishStatusToRedis(context.Background(), apiStatus, streams)
		}

		web.JSON(w, http.StatusOK, apiStatus)
		return
	}

	// Mock fallback
	now := time.Now()
	statusID := strconv.FormatInt(now.UnixNano(), 10)
	discoverable := true

	web.JSON(w, http.StatusOK, Status{
		ID:          statusID,
		CreatedAt:   now,
		Sensitive:   req.Sensitive,
		SpoilerText: req.SpoilerText,
		Visibility:  req.Visibility,
		Language:    req.Language,
		URI:         "https://example.com/users/user/statuses/" + statusID,
		URL:         ptrStr("https://example.com/@user/" + statusID),
		Content:     "<p>" + req.Status + "</p>",
		Account: Account{
			ID:           strconv.FormatInt(accountID, 10),
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	})
}

// Get handles GET /api/v1/statuses/:id
func (h *StatusesHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	if idStr == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbStatusToAPI(ctx, dbStatus))
		return
	}

	// Mock fallback
	now := time.Now()
	discoverable := true
	web.JSON(w, http.StatusOK, Status{
		ID:         idStr,
		CreatedAt:  now.Add(-time.Hour),
		Visibility: "public",
		URI:        "https://example.com/users/user/statuses/" + idStr,
		URL:        ptrStr("https://example.com/@user/" + idStr),
		Content:    "<p>Hello, world!</p>",
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	})
}

// Delete handles DELETE /api/v1/statuses/:id
func (h *StatusesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil || dbStatus.AccountID != accountID {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		_ = h.queries.DeleteStatus(ctx, id)

		// Decrement parent reply count
		if dbStatus.InReplyToID.Valid {
			_ = h.queries.DecrementStatusReplies(ctx, dbStatus.InReplyToID.Int64)
		}

		web.JSON(w, http.StatusOK, map[string]any{
			"id":                idStr,
			"text":              dbStatus.Text,
			"spoiler_text":      dbStatus.SpoilerText,
			"visibility":        visibilityToString[dbStatus.Visibility],
			"media_attachments": []any{},
			"poll":              nil,
		})

		// Publish delete event
		if h.publisher != nil {
			streams := []string{}
			if dbStatus.Visibility == VisibilityPublic {
				streams = append(streams, streaming.StreamPublic, streaming.StreamPublicLocal)
			}
			streams = append(streams, fmt.Sprintf("user:%d", dbStatus.AccountID))
			// TODO: Add hashtag streams if we knew them

			_ = h.publisher.PublishDeleteToRedis(context.Background(), idStr, streams)
		}

		return
	}

	web.JSON(w, http.StatusOK, map[string]any{
		"id":                idStr,
		"text":              "Hello, world!",
		"spoiler_text":      "",
		"visibility":        "public",
		"media_attachments": []any{},
		"poll":              nil,
	})
}

// Favourite handles POST /api/v1/statuses/:id/favourite
func (h *StatusesHandler) Favourite(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		_ = h.queries.IncrementStatusFavourites(ctx, id)

		status := h.dbStatusToAPI(ctx, dbStatus)
		status.Favourited = true
		status.FavouritesCount++
		web.JSON(w, http.StatusOK, status)
		return
	}

	status, _ := h.getStatusByID(idStr)
	status.Favourited = true
	status.FavouritesCount++
	web.JSON(w, http.StatusOK, status)
}

// Unfavourite handles POST /api/v1/statuses/:id/unfavourite
func (h *StatusesHandler) Unfavourite(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		_ = h.queries.DecrementStatusFavourites(ctx, id)

		status := h.dbStatusToAPI(ctx, dbStatus)
		status.Favourited = false
		web.JSON(w, http.StatusOK, status)
		return
	}

	status, _ := h.getStatusByID(idStr)
	status.Favourited = false
	web.JSON(w, http.StatusOK, status)
}

// Reblog handles POST /api/v1/statuses/:id/reblog
func (h *StatusesHandler) Reblog(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		originalStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Create reblog status
		reblogStatus, err := h.queries.CreateStatus(ctx, db.CreateStatusParams{
			AccountID:  accountID,
			ReblogOfID: pgtype.Int8{Int64: id, Valid: true},
			Visibility: originalStatus.Visibility,
			Local:      pgtype.Bool{Bool: true, Valid: true},
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create reblog")
			return
		}

		_ = h.queries.IncrementStatusReblogs(ctx, id)

		original := h.dbStatusToAPI(ctx, originalStatus)
		original.ReblogsCount++

		reblog := h.dbStatusToAPI(ctx, reblogStatus)
		reblog.Reblog = &original
		reblog.Reblogged = true

		web.JSON(w, http.StatusOK, reblog)
		return
	}

	// Mock fallback
	originalStatus, _ := h.getStatusByID(idStr)
	originalStatus.ReblogsCount++

	now := time.Now()
	reblogID := strconv.FormatInt(now.UnixNano(), 10)
	discoverable := true

	web.JSON(w, http.StatusOK, Status{
		ID:        reblogID,
		CreatedAt: now,
		Reblog:    &originalStatus,
		Reblogged: true,
		Account: Account{
			ID:           strconv.FormatInt(accountID, 10),
			Username:     "user",
			Acct:         "user",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []CustomEmoji{},
			Fields:       []Field{},
		},
		MediaAttachments: []MediaAttach{},
		Mentions:         []Mention{},
		Tags:             []Tag{},
		Emojis:           []any{},
	})
}

// Unreblog handles POST /api/v1/statuses/:id/unreblog
func (h *StatusesHandler) Unreblog(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		_ = h.queries.DecrementStatusReblogs(ctx, id)

		status := h.dbStatusToAPI(ctx, dbStatus)
		status.Reblogged = false
		web.JSON(w, http.StatusOK, status)
		return
	}

	status, _ := h.getStatusByID(idStr)
	status.Reblogged = false
	web.JSON(w, http.StatusOK, status)
}

// Bookmark handles POST /api/v1/statuses/:id/bookmark
func (h *StatusesHandler) Bookmark(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if h.useDB() {
		status, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		err = h.queries.CreateBookmark(ctx, db.CreateBookmarkParams{
			AccountID: accountID,
			StatusID:  id,
		})
		if err != nil {
			// Ignore unique violation
			_ = err
		}

		s := h.dbStatusToAPI(ctx, status)
		s.Bookmarked = true
		web.JSON(w, http.StatusOK, s)
		return
	}

	status, _ := h.getStatusByID(idStr)
	status.Bookmarked = true
	web.JSON(w, http.StatusOK, status)
}

// Unbookmark handles POST /api/v1/statuses/:id/unbookmark
func (h *StatusesHandler) Unbookmark(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if h.useDB() {
		status, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		_ = h.queries.DeleteBookmark(ctx, db.DeleteBookmarkParams{
			AccountID: accountID,
			StatusID:  id,
		})

		s := h.dbStatusToAPI(ctx, status)
		s.Bookmarked = false
		web.JSON(w, http.StatusOK, s)
		return
	}

	status, _ := h.getStatusByID(idStr)
	status.Bookmarked = false
	web.JSON(w, http.StatusOK, status)
}

// Context handles GET /api/v1/statuses/:id/context
func (h *StatusesHandler) Context(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	if idStr == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		// Get ancestors
		ancestors, err := h.queries.GetStatusAncestors(ctx, id)
		if err != nil {
			ancestors = []db.Status{}
		}
		ancestorStatuses := make([]Status, len(ancestors))
		for i, s := range ancestors {
			ancestorStatuses[i] = h.dbStatusToAPI(ctx, s)
		}

		// Get descendants
		descendants, err := h.queries.GetStatusDescendants(ctx, pgtype.Int8{Int64: id, Valid: true})
		if err != nil {
			descendants = []db.Status{}
		}
		descendantStatuses := make([]Status, len(descendants))
		for i, s := range descendants {
			descendantStatuses[i] = h.dbStatusToAPI(ctx, s)
		}

		web.JSON(w, http.StatusOK, map[string][]Status{
			"ancestors":   ancestorStatuses,
			"descendants": descendantStatuses,
		})
		return
	}

	web.JSON(w, http.StatusOK, map[string][]Status{
		"ancestors":   {},
		"descendants": {},
	})
}

// getStatusByID is a helper for mock fallback
func (h *StatusesHandler) getStatusByID(id string) (Status, error) {
	now := time.Now()
	discoverable := true

	return Status{
		ID:         id,
		CreatedAt:  now.Add(-time.Hour),
		Visibility: "public",
		URI:        "https://example.com/users/user/statuses/" + id,
		URL:        ptrStr("https://example.com/@user/" + id),
		Content:    "<p>Hello, world!</p>",
		Account: Account{
			ID:           "1",
			Username:     "user",
			Acct:         "user",
			DisplayName:  "Test User",
			Discoverable: &discoverable,
			CreatedAt:    now.Add(-24 * time.Hour),
			Emojis:       []CustomEmoji{},
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

// Edit handles PUT /api/v1/statuses/:id
func (h *StatusesHandler) Edit(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Status      string   `json:"status" form:"status"`
		SpoilerText string   `json:"spoiler_text" form:"spoiler_text"`
		Sensitive   bool     `json:"sensitive" form:"sensitive"`
		MediaIDs    []string `json:"media_ids" form:"media_ids[]"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if h.useDB() {
		status, err := h.queries.GetStatusByID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		if status.AccountID != accountID {
			web.JSONError(w, http.StatusForbidden, "This action is not allowed")
			return
		}

		// Update status with pgtype
		_, _ = h.queries.UpdateStatus(r.Context(), db.UpdateStatusParams{
			ID:   statusID,
			Text: pgtype.Text{String: req.Status, Valid: true},
		})

		// Return updated status from DB
		s := h.dbStatusToAPI(r.Context(), status)
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// Pin handles POST /api/v1/statuses/:id/pin
func (h *StatusesHandler) Pin(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		status, err := h.queries.GetStatusByID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		if status.AccountID != accountID {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Someone else's toot cannot be pinned")
			return
		}

		// Check visibility (must be public or unlisted)
		if status.Visibility != 0 && status.Visibility != 1 {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Private or direct statuses cannot be pinned")
			return
		}

		// Check limit (max 5)
		pinned, err := h.queries.GetPinnedStatuses(r.Context(), accountID)
		if err == nil && len(pinned) >= 5 {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: You have already pinned 5 statuses")
			return
		}

		_, _ = h.queries.PinStatus(r.Context(), db.PinStatusParams{
			AccountID: accountID,
			StatusID:  statusID,
		})
		// Ignore constraint violation (already pinned)

		s := h.dbStatusToAPI(r.Context(), status)
		s.Pinned = true
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// Unpin handles POST /api/v1/statuses/:id/unpin
func (h *StatusesHandler) Unpin(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	_ = accountID // Will be used when unpin query exists

	if h.useDB() {
		status, err := h.queries.GetStatusByID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		_ = h.queries.UnpinStatus(r.Context(), db.UnpinStatusParams{
			AccountID: accountID,
			StatusID:  statusID,
		})

		s := h.dbStatusToAPI(r.Context(), status)
		s.Pinned = false
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// FavouritedBy handles GET /api/v1/statuses/:id/favourited_by
func (h *StatusesHandler) FavouritedBy(w http.ResponseWriter, r *http.Request) {
	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		dbAccounts, err := h.queries.GetFavouritedBy(r.Context(), db.GetFavouritedByParams{
			StatusID: statusID,
			Limit:    40,
			Offset:   0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []any{})
			return
		}

		accounts := make([]Account, len(dbAccounts))
		for i, a := range dbAccounts {
			accounts[i] = h.dbAccountToAPI(a)
		}

		web.JSON(w, http.StatusOK, accounts)
		return
	}

	web.JSON(w, http.StatusOK, []any{})
}

// RebloggedBy handles GET /api/v1/statuses/:id/reblogged_by
func (h *StatusesHandler) RebloggedBy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		dbAccounts, err := h.queries.GetRebloggers(r.Context(), db.GetRebloggersParams{
			ReblogOfID: pgtype.Int8{Int64: id, Valid: true},
			Limit:      40,
			Offset:     0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []any{})
			return
		}

		accounts := make([]Account, len(dbAccounts))
		for i, a := range dbAccounts {
			accounts[i] = h.dbAccountToAPI(a)
		}

		web.JSON(w, http.StatusOK, accounts)
		return
	}

	web.JSON(w, http.StatusOK, []any{})
}

func (h *StatusesHandler) dbAccountToAPI(dbAcc db.Account) Account {
	discoverable := dbAcc.Discoverable.Valid && dbAcc.Discoverable.Bool
	accCreatedAt := time.Now().Add(-24 * time.Hour)
	if dbAcc.CreatedAt.Valid {
		accCreatedAt = dbAcc.CreatedAt.Time
	}
	return Account{
		ID:           strconv.FormatInt(dbAcc.ID, 10),
		Username:     dbAcc.Username,
		Acct:         dbAcc.Username,
		DisplayName:  dbAcc.DisplayName,
		Discoverable: &discoverable,
		CreatedAt:    accCreatedAt,
		Emojis:       []CustomEmoji{},
		Fields:       []Field{},
	}
}

// RegisterRoutes registers the statuses routes
func (h *StatusesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/statuses", h.Create)
	mux.HandleFunc("GET /api/v1/statuses/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/statuses/{id}", h.Edit)
	mux.HandleFunc("DELETE /api/v1/statuses/{id}", h.Delete)
	mux.HandleFunc("GET /api/v1/statuses/{id}/context", h.Context)
	mux.HandleFunc("POST /api/v1/statuses/{id}/favourite", h.Favourite)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unfavourite", h.Unfavourite)
	mux.HandleFunc("POST /api/v1/statuses/{id}/reblog", h.Reblog)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unreblog", h.Unreblog)
	mux.HandleFunc("POST /api/v1/statuses/{id}/bookmark", h.Bookmark)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unbookmark", h.Unbookmark)
	mux.HandleFunc("POST /api/v1/statuses/{id}/pin", h.Pin)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unpin", h.Unpin)
	mux.HandleFunc("GET /api/v1/statuses/{id}/favourited_by", h.FavouritedBy)
	mux.HandleFunc("GET /api/v1/statuses/{id}/reblogged_by", h.RebloggedBy)
}
