// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/translation"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

var (
	mentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_]+)(?:@([a-zA-Z0-9_.-]+))?`)
	tagRegex     = regexp.MustCompile(`#([\p{L}\p{N}_]+)`)
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
	queries   db.Querier
	pool      *pgxpool.Pool
	publisher streaming.Publisher
	cfg       *config.Config
}

// NewStatusesHandler creates a new statuses handler
func NewStatusesHandler(queries db.Querier, pool *pgxpool.Pool, publisher streaming.Publisher, cfg *config.Config) *StatusesHandler {
	return &StatusesHandler{
		queries:   queries,
		pool:      pool,
		publisher: publisher,
		cfg:       cfg,
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
	Quote              *Status        `json:"quote,omitempty"`
	QuoteID            *string        `json:"quote_id,omitempty"`
	Application        *StatusApp     `json:"application,omitempty"`
	Account            Account        `json:"account"`
	MediaAttachments   []MediaAttach  `json:"media_attachments"`
	Mentions           []Mention      `json:"mentions"`
	Tags               []Tag          `json:"tags"`
	Emojis             []CustomEmoji  `json:"emojis"`
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
	ID          string        `json:"id"`
	ExpiresAt   *time.Time    `json:"expires_at"`
	Expired     bool          `json:"expired"`
	Multiple    bool          `json:"multiple"`
	VotesCount  int           `json:"votes_count"`
	VotersCount *int          `json:"voters_count"`
	Voted       bool          `json:"voted"`
	OwnVotes    []int         `json:"own_votes"`
	Options     []PollOption  `json:"options"`
	Emojis      []CustomEmoji `json:"emojis"`
}

// notificationTask captures data needed to publish a notification
type notificationTask struct {
	Notification db.Notification
	Account      db.Account
}

// processMentions parses mentions from status text and creates notifications
func (h *StatusesHandler) processMentions(ctx context.Context, q db.Querier, statusID int64, accountID int64, text string) ([]Mention, []notificationTask, error) {
	// Simple mention regex: @username or @username@domain
	matches := mentionRegex.FindAllStringSubmatch(text, -1)

	var mentions []Mention
	var tasks []notificationTask

	if len(matches) == 0 {
		return mentions, tasks, nil
	}

	// 1. Collect names to fetch
	localUsernames := make([]string, 0)
	remoteUsernames := make([]string, 0)
	remoteDomains := make([]string, 0)

	seenLocals := make(map[string]bool)
	seenRemotes := make(map[string]bool)

	for _, match := range matches {
		username := match[1]
		if len(match) > 2 && match[2] != "" {
			domain := match[2]
			key := strings.ToLower(username) + "|" + strings.ToLower(domain)
			if !seenRemotes[key] {
				seenRemotes[key] = true
				remoteUsernames = append(remoteUsernames, username)
				remoteDomains = append(remoteDomains, domain)
			}
		} else {
			u := strings.ToLower(username)
			if !seenLocals[u] {
				seenLocals[u] = true
				localUsernames = append(localUsernames, u)
			}
		}
	}

	// 2. Bulk Fetch
	var accounts []db.Account
	if len(localUsernames) > 0 {
		accs, err := q.GetAccountsByUsernames(ctx, localUsernames)
		if err == nil {
			accounts = append(accounts, accs...)
		}
	}

	if len(remoteUsernames) > 0 {
		accs, err := q.GetAccountsByUsernamesAndDomains(ctx, db.GetAccountsByUsernamesAndDomainsParams{
			Usernames: remoteUsernames,
			Domains:   remoteDomains,
		})
		if err == nil {
			accounts = append(accounts, accs...)
		}
	}

	if len(accounts) == 0 {
		return mentions, tasks, nil
	}

	// 3. Filter valid accounts and prep for Mute/Block check
	accountMap := make(map[int64]db.Account)
	targetIDs := make([]int64, 0, len(accounts))

	for _, a := range accounts {
		if a.ID == accountID {
			continue // Don't mention yourself
		}
		if _, exists := accountMap[a.ID]; exists {
			continue
		}
		accountMap[a.ID] = a
		targetIDs = append(targetIDs, a.ID)
	}

	if len(targetIDs) == 0 {
		return mentions, tasks, nil
	}

	// 4. Bulk check Mutes/Blocks (from mentioned users targeting sender)
	mutedBy := make(map[int64]bool)
	blockedBy := make(map[int64]bool)

	mutes, err := q.GetMutesFromAccounts(ctx, db.GetMutesFromAccountsParams{
		TargetAccountID: accountID,
		AccountIds:      targetIDs,
	})
	if err == nil {
		for _, m := range mutes {
			mutedBy[m.AccountID] = true
		}
	}

	blocks, err := q.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
		TargetAccountID: accountID,
		AccountIds:      targetIDs,
	})
	if err == nil {
		for _, b := range blocks {
			blockedBy[b.AccountID] = true
		}
	}

	// 5. Process matches to create mentions
	var mentionAccountIDs []int64
	var mentionSilent []bool

	for _, tid := range targetIDs {
		silent := mutedBy[tid] || blockedBy[tid]
		mentionAccountIDs = append(mentionAccountIDs, tid)
		mentionSilent = append(mentionSilent, silent)
	}

	if len(mentionAccountIDs) > 0 {
		// Bulk Create Mentions
		if err := q.CreateMentions(ctx, db.CreateMentionsParams{
			StatusID: statusID,
			Column2:  mentionAccountIDs,
			Column3:  mentionSilent,
		}); err != nil {
			return mentions, tasks, err
		}

		// Prepare Notifications
		var notifAccountIDs []int64
		notifAccountIDs = append(notifAccountIDs, targetIDs...)

		// Bulk Create Notifications
		notifs, err := q.CreateNotifications(ctx, db.CreateNotificationsParams{
			Column1:       notifAccountIDs,
			FromAccountID: accountID,
			ActivityType:  "Status",
			ActivityID:    statusID,
			Type:          pgtype.Text{String: "mention", Valid: true},
		})
		if err != nil {
			return mentions, tasks, err
		}

		// Map back to tasks
		// Note: The order of returned notifications matches input order for simple UNNEST usually,
		// but concurrent inserts might strictly not guarantee order if we relied on ID.
		// However, we just need to pair them up.
		// A safer way is to map by AccountID if we include it in return.
		// Our CreateNotifications returns * including AccountID.

		for _, notif := range notifs {
			acc := accountMap[notif.AccountID]
			tasks = append(tasks, notificationTask{
				Notification: notif,
				Account:      acc,
			})

			apiAcc := SerializeAccount(h.cfg, acc)

			// Populate return mentions (simple version)
			mentions = append(mentions, Mention{
				ID:       apiAcc.ID,
				Username: apiAcc.Username,
				URL:      apiAcc.URI, // Using URI as URL for mention per statuses_util.go
				Acct:     apiAcc.Acct,
			})
		}
	}

	return mentions, tasks, nil
}

// processTags parses hashtags from text
func (h *StatusesHandler) processTags(text string) ([]string, error) {
	matches := tagRegex.FindAllStringSubmatch(text, -1)

	tagsMap := make(map[string]bool)
	var tags []string

	for _, match := range matches {
		tag := strings.ToLower(match[1])
		if tagsMap[tag] {
			continue
		}
		tagsMap[tag] = true
		tags = append(tags, tag)
	}

	return tags, nil
}

// saveTags persists tags for a status
func (h *StatusesHandler) saveTags(ctx context.Context, q db.Querier, statusID int64, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	// 1. Bulk Upsert all tags
	if err := q.UpsertTags(ctx, tags); err != nil {
		return err
	}

	// 2. Fetch all IDs
	dbTags, err := q.GetTagsByNames(ctx, tags)
	if err != nil {
		return err
	}

	// 3. Bulk Link tags to status
	tagIDs := make([]int64, len(dbTags))
	for i, t := range dbTags {
		tagIDs[i] = t.ID
	}
	if err := q.AddStatusTags(ctx, db.AddStatusTagsParams{
		StatusID: statusID,
		Column2:  tagIDs,
	}); err != nil {
		return err
	}

	return nil
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
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to start transaction")
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()

		qtx := db.New(tx)

		// Fetch sender account for URI generation and later usage
		sender, err := qtx.GetAccountByID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
			return
		}

		var inReplyToID, inReplyToAccountID pgtype.Int8
		if req.InReplyToID != nil {
			id, err := strconv.ParseInt(*req.InReplyToID, 10, 64)
			if err == nil {
				inReplyToID = pgtype.Int8{Int64: id, Valid: true}
				// Get parent status to set reply account
				parent, err := qtx.GetStatusByID(ctx, id)
				if err == nil {
					inReplyToAccountID = pgtype.Int8{Int64: parent.AccountID, Valid: true}
				}
			}
		}

		var lang pgtype.Text
		if req.Language != nil {
			lang = pgtype.Text{String: *req.Language, Valid: true}
		}

		dbStatus, err := qtx.CreateStatus(ctx, db.CreateStatusParams{
			Uri:                pgtype.Text{Valid: false}, // Set later with ID
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

		// Generate and Update URI with Snowflake ID
		// Format: https://<domain>/users/<username>/statuses/<id>
		statusIDStr := strconv.FormatInt(dbStatus.ID, 10)
		uri := fmt.Sprintf("https://%s/users/%s/statuses/%s", h.cfg.Instance.LocalDomain, sender.Username, statusIDStr)

		if err := qtx.UpdateStatusURI(ctx, db.UpdateStatusURIParams{
			Uri: pgtype.Text{String: uri, Valid: true},
			ID:  dbStatus.ID,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update status URI")
			return
		}

		// Update struct for response
		dbStatus.Uri = pgtype.Text{String: uri, Valid: true}

		// Create stats
		if _, err := qtx.CreateStatusStats(ctx, dbStatus.ID); err != nil {
			fmt.Printf("Failed to create status stats: %v\n", err)
		}

		// Increment reply count on parent
		if inReplyToID.Valid {
			if err := qtx.IncrementStatusReplies(ctx, inReplyToID.Int64); err != nil {
				fmt.Printf("Failed to increment reply count: %v\n", err)
			}
		}

		_, notifTasks, err := h.processMentions(ctx, qtx, dbStatus.ID, accountID, req.Status)
		if err != nil {
			// Log error but continue status creation?
			// Better to fail if critical, or log and continue.
			// Review says: "Silent failure patterns... This could lead to statuses being posted without mentions"
			// So we should handle it. However, failing the whole status after creation might be extreme.
			// But since we are in a transaction, we can return error and rollback.
			web.JSONError(w, http.StatusInternalServerError, "Failed to process mentions")
			return
		}

		// Process Media Attachments
		for _, mediaIDStr := range req.MediaIDs {
			mediaID, err := strconv.ParseInt(mediaIDStr, 10, 64)
			if err != nil {
				continue
			}
			rows, err := qtx.AttachMediaToStatus(ctx, db.AttachMediaToStatusParams{
				ID:        mediaID,
				StatusID:  pgtype.Int8{Int64: dbStatus.ID, Valid: true},
				AccountID: pgtype.Int8{Int64: accountID, Valid: true},
			})
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to attach media")
				return
			}
			if rows != 1 {
				web.JSONError(w, http.StatusForbidden, "Media not found or not owned by you")
				return
			}
		}

		// Process Hashtags
		tags, _ := h.processTags(req.Status)
		if err := h.saveTags(ctx, qtx, dbStatus.ID, tags); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to save tags")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to commit transaction")
			return
		}

		apiStatus := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)

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

			for _, t := range tags {
				streams = append(streams, fmt.Sprintf("hashtag:%s", t))
			}

			_ = h.publisher.PublishStatusToRedis(context.Background(), apiStatus, streams)

			// Publish notifications
			// Sender already fetched
			senderAPI := h.dbAccountToAPI(sender)
			for _, task := range notifTasks {
				apiNotif := Notification{
					ID:        strconv.FormatInt(task.Notification.ID, 10),
					Type:      "mention",
					CreatedAt: task.Notification.CreatedAt.Time,
					Account:   senderAPI,
					Status:    &apiStatus,
				}
				_ = h.publisher.PublishNotificationToRedis(ctx, task.Account.ID, apiNotif)
			}
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
		Emojis:           []CustomEmoji{},
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
		web.JSON(w, http.StatusOK, SerializeStatus(ctx, h.queries, h.cfg, dbStatus))
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
		Emojis:           []CustomEmoji{},
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
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to start transaction")
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		qtx := db.New(tx)

		dbStatus, err := qtx.GetStatusByID(ctx, id)
		if err != nil || dbStatus.AccountID != accountID {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		if err := qtx.DeleteStatus(ctx, id); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to delete status")
			return
		}

		// Decrement parent reply count
		if dbStatus.InReplyToID.Valid {
			if err := qtx.DecrementStatusReplies(ctx, dbStatus.InReplyToID.Int64); err != nil {
				fmt.Printf("Failed to decrement reply count: %v\n", err)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to commit transaction")
			return
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
			// Hashtag streams for delete not fully supported yet (needs query)

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
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Create favourite
		_, err = h.queries.CreateFavourite(ctx, db.CreateFavouriteParams{
			AccountID: accountID,
			StatusID:  id,
		})
		if err != nil {
			// Check for unique violation (already favourited)
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				// Already favourited, just return status as is
				status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
				status.Favourited = true
				web.JSON(w, http.StatusOK, status)
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Failed to favourite status")
			return
		}

		if err := h.queries.IncrementStatusFavourites(ctx, id); err != nil {
			// Try to rollback favourite creation? Or just log.
			fmt.Printf("Failed to increment favourites count: %v\n", err)
		}

		status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
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
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Delete favourite
		err = h.queries.DeleteFavourite(ctx, db.DeleteFavouriteParams{
			AccountID: accountID,
			StatusID:  id,
		})
		if err != nil {
			// If not found, just return success
			_ = err
		} else {
			// Only decrement if we actually deleted something
			if err := h.queries.DecrementStatusFavourites(ctx, id); err != nil {
				fmt.Printf("Failed to decrement favourites count: %v\n", err)
			}
		}

		status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
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

		original := SerializeStatus(ctx, h.queries, h.cfg, originalStatus)
		original.ReblogsCount++

		reblog := SerializeStatus(ctx, h.queries, h.cfg, reblogStatus)
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
		Emojis:           []CustomEmoji{},
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

		if err := h.queries.DecrementStatusReblogs(ctx, id); err != nil {
			fmt.Printf("Failed to decrement reblogs count: %v\n", err)
		}

		status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
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
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				// Already bookmarked, ignore
			} else {
				web.JSONError(w, http.StatusInternalServerError, "Failed to bookmark status")
				return
			}
		}

		s := SerializeStatus(ctx, h.queries, h.cfg, status)
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

		err = h.queries.DeleteBookmark(ctx, db.DeleteBookmarkParams{
			AccountID: accountID,
			StatusID:  id,
		})
		if err != nil {
			// If not found, ignore
			_ = err
		}

		s := SerializeStatus(ctx, h.queries, h.cfg, status)
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
			ancestorStatuses[i] = SerializeStatus(ctx, h.queries, h.cfg, s)
		}

		// Get descendants
		descendants, err := h.queries.GetStatusDescendants(ctx, pgtype.Int8{Int64: id, Valid: true})
		if err != nil {
			descendants = []db.Status{}
		}
		descendantStatuses := make([]Status, len(descendants))
		for i, s := range descendants {
			descendantStatuses[i] = SerializeStatus(ctx, h.queries, h.cfg, s)
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
		Emojis:           []CustomEmoji{},
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
		Poll        *PollReq `json:"poll" form:"poll"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	if req.Status == "" && len(req.MediaIDs) == 0 && req.Poll == nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Text can't be blank without media or poll")
		return
	}

	if h.useDB() {
		tx, err := h.pool.Begin(r.Context())
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to start transaction")
			return
		}
		defer func() { _ = tx.Rollback(r.Context()) }()
		qtx := db.New(tx)

		status, err := qtx.GetStatusByID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		if status.AccountID != accountID {
			web.JSONError(w, http.StatusForbidden, "This action is not allowed")
			return
		}

		// Handle Media Updates
		currentMedia, err := qtx.ListMediaAttachmentsByStatus(r.Context(), pgtype.Int8{Int64: statusID, Valid: true})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch current media")
			return
		}

		currentMediaMap := make(map[int64]bool)
		for _, m := range currentMedia {
			currentMediaMap[m.ID] = true
		}

		newMediaMap := make(map[int64]bool)
		for _, idStr := range req.MediaIDs {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil {
				newMediaMap[id] = true
			}
		}

		for _, m := range currentMedia {
			if !newMediaMap[m.ID] {
				rows, err := qtx.DetachMediaFromStatus(r.Context(), db.DetachMediaFromStatusParams{
					ID:        m.ID,
					AccountID: pgtype.Int8{Int64: accountID, Valid: true},
				})
				if err != nil {
					web.JSONError(w, http.StatusInternalServerError, "Failed to detach media")
					return
				}
				if rows != 1 {
					// Should strictly not happen if we just fetched it, but good for safety
					web.JSONError(w, http.StatusInternalServerError, "Failed to detach media (ownership?)")
					return
				}
			}
		}

		for _, idStr := range req.MediaIDs {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				continue
			}
			if !currentMediaMap[id] {
				rows, err := qtx.AttachMediaToStatus(r.Context(), db.AttachMediaToStatusParams{
					ID:        id,
					StatusID:  pgtype.Int8{Int64: statusID, Valid: true},
					AccountID: pgtype.Int8{Int64: accountID, Valid: true},
				})
				if err != nil {
					web.JSONError(w, http.StatusInternalServerError, "Failed to attach new media")
					return
				}
				if rows != 1 {
					web.JSONError(w, http.StatusForbidden, "Media not found or not owned by you")
					return
				}
			}
		}

		// Update status fields
		_, err = qtx.UpdateStatus(r.Context(), db.UpdateStatusParams{
			ID:          statusID,
			Text:        pgtype.Text{String: req.Status, Valid: true},
			Sensitive:   pgtype.Bool{Bool: req.Sensitive, Valid: true},
			SpoilerText: pgtype.Text{String: req.SpoilerText, Valid: true},
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update status")
			return
		}

		// Handle Mentions Diff
		// 1. Fetch current mentions
		currentMentions, err := qtx.GetMentionsByStatusID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch current mentions")
			return
		}
		currentMentionAccountIDs := make(map[int64]bool)
		for _, m := range currentMentions {
			currentMentionAccountIDs[m.AccountID] = true
		}

		// 2. Identify new potential mentions
		// We use processMentions BUT we modify it or handle logic here.
		// Since processMentions does "bulk" ops, we can't easily inject "only valid ones".
		// We should probably parse them first to get IDs, then diff, then insert.
		// However, processMentions does all of that: parsing, fetching accounts, checking blocks, inserting.
		// To repurpose it for diffing, we'd need to change `processMentions` or duplicate logic.
		// Duplicating logic is safer to avoid breaking `Create`.
		// Actually, `CreateMentions` has `ON CONFLICT DO NOTHING`.
		// So inserting duplicates is safe-ish, BUT `CreateNotifications` does NOT.
		// So logic:
		// a. Parse and resolve accounts (copying logic from processMentions or using helper).
		// b. Filter out AccountIDs that are already in `currentMentionAccountIDs`.
		// c. Identify AccountIDs in `currentMentionAccountIDs` that are NOT in parsed list (to be removed).

		// Helper to resolve accounts from text
		resolvedAccounts, err := h.resolveAccountsFromText(r.Context(), qtx, req.Status, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to resolve mentions")
			return
		}

		resolvedAccountIDMap := make(map[int64]bool)
		var newMentions []int64
		var removedMentions []int64

		for _, acc := range resolvedAccounts {
			resolvedAccountIDMap[acc.ID] = true
			if !currentMentionAccountIDs[acc.ID] {
				newMentions = append(newMentions, acc.ID)
			}
		}

		for mID := range currentMentionAccountIDs {
			if !resolvedAccountIDMap[mID] {
				removedMentions = append(removedMentions, mID)
			}
		}

		// 3. Remove old mentions
		if len(removedMentions) > 0 {
			if err := qtx.RemoveMentions(r.Context(), db.RemoveMentionsParams{
				StatusID: statusID,
				Column2:  removedMentions,
			}); err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to remove old mentions")
				return
			}
		}

		// 4. Add new mentions and notifications
		if len(newMentions) > 0 {
			// Check mute/block for new mentions only
			// Reuse parts of processMentions logic
			_, _, err := h.createMentionsAndNotifications(r.Context(), qtx, statusID, accountID, newMentions)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to add new mentions")
				return
			}
		}

		// Handle Hashtags Sync (Replace all)
		if err := qtx.ClearStatusTags(r.Context(), statusID); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to clear tags")
			return
		}

		// Parse new tags
		newTags, _ := h.processTags(req.Status)
		if err := h.saveTags(r.Context(), qtx, statusID, newTags); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to save tags")
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to commit transaction")
			return
		}

		updatedStatus, err := h.queries.GetStatusByID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch updated status")
			return
		}

		s := SerializeStatus(r.Context(), h.queries, h.cfg, updatedStatus)
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

func (h *StatusesHandler) resolveAccountsFromText(ctx context.Context, q db.Querier, text string, userAccountID int64) ([]db.Account, error) {
	// Re-implement or factor out logic from processMentions
	// 1. Parse
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	localUsernames := make([]string, 0)
	remoteUsernames := make([]string, 0)
	remoteDomains := make([]string, 0)
	seen := make(map[string]bool)

	for _, match := range matches {
		username := match[1]
		if len(match) > 2 && match[2] != "" {
			domain := match[2]
			key := strings.ToLower(username) + "|" + strings.ToLower(domain)
			if !seen[key] {
				seen[key] = true
				remoteUsernames = append(remoteUsernames, username)
				remoteDomains = append(remoteDomains, domain)
			}
		} else {
			u := strings.ToLower(username)
			if !seen[u] {
				seen[u] = true
				localUsernames = append(localUsernames, u)
			}
		}
	}

	var accounts []db.Account
	if len(localUsernames) > 0 {
		accs, err := q.GetAccountsByUsernames(ctx, localUsernames)
		if err == nil {
			accounts = append(accounts, accs...)
		}
	}
	if len(remoteUsernames) > 0 {
		accs, err := q.GetAccountsByUsernamesAndDomains(ctx, db.GetAccountsByUsernamesAndDomainsParams{
			Usernames: remoteUsernames,
			Domains:   remoteDomains,
		})
		if err == nil {
			accounts = append(accounts, accs...)
		}
	}

	// Filter self
	var result []db.Account
	for _, a := range accounts {
		if a.ID != userAccountID {
			result = append(result, a)
		}
	}
	return result, nil
}

func (h *StatusesHandler) createMentionsAndNotifications(ctx context.Context, q db.Querier, statusID int64, accountID int64, targetIDs []int64) ([]Mention, []notificationTask, error) {
	// 4. Bulk check Mutes/Blocks (from mentioned users targeting sender)
	mutedBy := make(map[int64]bool)
	blockedBy := make(map[int64]bool)

	mutes, err := q.GetMutesFromAccounts(ctx, db.GetMutesFromAccountsParams{
		TargetAccountID: accountID,
		AccountIds:      targetIDs,
	})
	if err == nil {
		for _, m := range mutes {
			mutedBy[m.AccountID] = true
		}
	}

	blocks, err := q.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
		TargetAccountID: accountID,
		AccountIds:      targetIDs,
	})
	if err == nil {
		for _, b := range blocks {
			blockedBy[b.AccountID] = true
		}
	}

	// 5. Process matches to create mentions
	var mentionAccountIDs []int64
	var mentionSilent []bool

	for _, tid := range targetIDs {
		silent := mutedBy[tid] || blockedBy[tid]
		mentionAccountIDs = append(mentionAccountIDs, tid)
		mentionSilent = append(mentionSilent, silent)
	}

	var mentions []Mention
	var tasks []notificationTask

	if len(mentionAccountIDs) > 0 {
		// Bulk Create Mentions
		if err := q.CreateMentions(ctx, db.CreateMentionsParams{
			StatusID: statusID,
			Column2:  mentionAccountIDs,
			Column3:  mentionSilent,
		}); err != nil {
			return mentions, tasks, err
		}

		// Prepare Notifications
		var notifAccountIDs []int64
		notifAccountIDs = append(notifAccountIDs, targetIDs...)

		// Bulk Create Notifications
		_, err := q.CreateNotifications(ctx, db.CreateNotificationsParams{
			Column1:       notifAccountIDs,
			FromAccountID: accountID,
			ActivityType:  "Status",
			ActivityID:    statusID,
			Type:          pgtype.Text{String: "mention", Valid: true},
		})
		if err != nil {
			return mentions, tasks, err
		}

		// Note: We don't construct the full return objects here to save complexity as Edit doesn't necessarily return them in the same way,
		// but the original processMentions did.
		// For now, we return empty mentions/tasks as we just wanted side effects.
		// If we need them for return value (e.g. streaming), we should rebuild them.
		// But for Edit, we usually just return the Status which has "Mentions" list.
		// Notifications are async.
	}

	return mentions, tasks, nil
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

		_, err = h.queries.PinStatus(r.Context(), db.PinStatusParams{
			AccountID: accountID,
			StatusID:  statusID,
		})
		if err != nil {
			// Check for unique violation (already pinned)
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				// Already pinned, ignore
			} else {
				web.JSONError(w, http.StatusInternalServerError, "Failed to pin status")
				return
			}
		}

		s := SerializeStatus(r.Context(), h.queries, h.cfg, status)
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

	// _ = accountID // Removed redundant assignment

	if h.useDB() {
		status, err := h.queries.GetStatusByID(r.Context(), statusID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		if err := h.queries.UnpinStatus(r.Context(), db.UnpinStatusParams{
			AccountID: accountID,
			StatusID:  statusID,
		}); err != nil {
			// If not found, ignore? UnpinStatus is delete.
			// But if error is real DB error, we should probably report it or log it.
			// Deletes usually return nil if nothing deleted unless Exec is checked for rows affected.
			// sqlc Exec returns error on DB failure.
			// Favour safety: Report invalid errors, ignore 'not found'-like semantics if implicit.
			// But for Unpin, if it fails, client might get confused.
			web.JSONError(w, http.StatusInternalServerError, "Failed to unpin status")
			return
		}

		s := SerializeStatus(r.Context(), h.queries, h.cfg, status)
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

// getMediaAttachmentsForStatus fetches and serializes media attachments for a status
func (h *StatusesHandler) getMediaAttachmentsForStatus(ctx context.Context, statusID int64) []MediaAttach {
	if h.queries == nil {
		return []MediaAttach{}
	}

	dbMedias, err := h.queries.GetMediaAttachmentsByStatusIDs(ctx, []int64{statusID})
	if err != nil || len(dbMedias) == 0 {
		return []MediaAttach{}
	}

	result := make([]MediaAttach, 0, len(dbMedias))
	for _, m := range dbMedias {
		if !m.StatusID.Valid || m.StatusID.Int64 != statusID {
			continue
		}
		result = append(result, h.serializeMedia(m))
	}
	return result
}

// getMediaAttachmentsByIDs fetches and serializes media by their IDs
func (h *StatusesHandler) getMediaAttachmentsByIDs(ctx context.Context, ids []int64) []MediaAttach {
	if h.queries == nil || len(ids) == 0 {
		return []MediaAttach{}
	}

	dbMedias, err := h.queries.GetMediaAttachmentsByIDs(ctx, ids)
	if err != nil || len(dbMedias) == 0 {
		return []MediaAttach{}
	}

	// Preserve order based on input IDs
	idToMedia := make(map[int64]db.MediaAttachment)
	for _, m := range dbMedias {
		idToMedia[m.ID] = m
	}

	result := make([]MediaAttach, 0, len(ids))
	for _, id := range ids {
		if m, ok := idToMedia[id]; ok {
			result = append(result, h.serializeMedia(m))
		}
	}
	return result
}

// serializeMedia converts a db.MediaAttachment to API MediaAttach
func (h *StatusesHandler) serializeMedia(m db.MediaAttachment) MediaAttach {
	mediaType := "image"
	if m.Type == MediaTypeVideo {
		mediaType = "video"
	}

	var remoteURL *string
	if m.RemoteUrl != "" {
		s := m.RemoteUrl
		remoteURL = &s
	}

	var description *string
	if m.Description.Valid {
		s := m.Description.String
		description = &s
	}

	var blurhash *string
	if m.Blurhash.Valid {
		s := m.Blurhash.String
		blurhash = &s
	}

	return MediaAttach{
		ID:          strconv.FormatInt(m.ID, 10),
		Type:        mediaType,
		URL:         m.RemoteUrl,
		PreviewURL:  m.ThumbnailRemoteUrl.String,
		RemoteURL:   remoteURL,
		Description: description,
		Blurhash:    blurhash,
	}
}

// getPollForStatus fetches and serializes a poll for display
func (h *StatusesHandler) getPollForStatus(ctx context.Context, pollID int64, accountID int64) *Poll {
	if h.queries == nil {
		return nil
	}

	dbPoll, err := h.queries.GetPoll(ctx, pollID)
	if err != nil {
		return nil
	}

	// Get poll options
	dbOptions, err := h.queries.GetPollOptions(ctx, pollID)
	if err != nil {
		return nil
	}

	options := make([]PollOption, len(dbOptions))
	totalVotes := 0
	for i, opt := range dbOptions {
		// Type assertions for interface{} fields from sqlc
		title := ""
		if t, ok := opt.Title.(string); ok {
			title = t
		}
		votesCount := 0
		if vc, ok := opt.VotesCount.(int64); ok {
			votesCount = int(vc)
		}
		vc := votesCount // Create local copy for pointer
		options[i] = PollOption{
			Title:      title,
			VotesCount: &vc,
		}
		totalVotes += votesCount
	}

	var expiresAt *time.Time
	if dbPoll.ExpiresAt.Valid {
		expiresAt = &dbPoll.ExpiresAt.Time
	}

	expired := false
	if expiresAt != nil && time.Now().After(*expiresAt) {
		expired = true
	}

	// Check if user voted (if authenticated)
	voted := false
	var ownVotes []int
	if accountID > 0 {
		votes, err := h.queries.GetPollVotesByAccountID(ctx, db.GetPollVotesByAccountIDParams{
			PollID:    pollID,
			AccountID: pgtype.Int8{Int64: accountID, Valid: true},
		})
		if err == nil && len(votes) > 0 {
			voted = true
			ownVotes = make([]int, len(votes))
			for i, v := range votes {
				ownVotes[i] = int(v.Choice)
			}
		}
	}
	if ownVotes == nil {
		ownVotes = []int{}
	}

	votersCount := 0
	if dbPoll.VotersCount.Valid {
		votersCount = int(dbPoll.VotersCount.Int64)
	}

	return &Poll{
		ID:          strconv.FormatInt(pollID, 10),
		ExpiresAt:   expiresAt,
		Expired:     expired,
		Multiple:    dbPoll.Multiple,
		VotesCount:  totalVotes,
		VotersCount: &votersCount,
		Voted:       voted,
		OwnVotes:    ownVotes,
		Options:     options,
		Emojis:      []CustomEmoji{},
	}
}

type StatusEdit struct {
	Content          string        `json:"content"`
	SpoilerText      string        `json:"spoiler_text"`
	Sensitive        bool          `json:"sensitive"`
	CreatedAt        time.Time     `json:"created_at"`
	Account          Account       `json:"account"`
	Poll             *Poll         `json:"poll"`
	MediaAttachments []MediaAttach `json:"media_attachments"`
	Emojis           []CustomEmoji `json:"emojis"`
}

// StatusSource represents the raw source of a status
type StatusSource struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	SpoilerText string `json:"spoiler_text"`
}

// History handles GET /api/v1/statuses/:id/history
func (h *StatusesHandler) History(w http.ResponseWriter, r *http.Request) {
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
		// Get the current status
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Get the status author
		dbAccount, err := h.queries.GetAccountByID(ctx, dbStatus.AccountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
			return
		}

		account := h.dbAccountToAPI(dbAccount)

		// Get all edits for this status
		edits, err := h.queries.GetStatusEditsByStatusID(ctx, id)
		if err != nil {
			// No edits is not an error - return original as only version
			edits = nil
		}

		// Get media attachments for current status
		currentMedia := h.getMediaAttachmentsForStatus(ctx, id)

		// Get poll for current status if exists
		var currentPoll *Poll
		if dbStatus.PollID.Valid {
			currentPoll = h.getPollForStatus(ctx, dbStatus.PollID.Int64, 0)
		}

		var history []StatusEdit

		// Add all historical edits
		for _, edit := range edits {
			// Reconstruct poll from edit's poll_options if available
			var editPoll *Poll
			if len(edit.PollOptions) > 0 {
				editPoll = &Poll{
					Options: make([]PollOption, len(edit.PollOptions)),
					Emojis:  []CustomEmoji{},
				}
				for i, opt := range edit.PollOptions {
					editPoll.Options[i] = PollOption{Title: opt}
				}
			}

			// Reconstruct media from edit's ordered_media_attachment_ids
			var editMedia []MediaAttach
			if len(edit.OrderedMediaAttachmentIds) > 0 {
				editMedia = h.getMediaAttachmentsByIDs(ctx, edit.OrderedMediaAttachmentIds)
				// Apply descriptions from edit if available
				for i := range editMedia {
					if i < len(edit.MediaDescriptions) {
						editMedia[i].Description = &edit.MediaDescriptions[i]
					}
				}
			} else {
				editMedia = []MediaAttach{}
			}

			history = append(history, StatusEdit{
				Content:          formatContent(edit.Text),
				SpoilerText:      edit.SpoilerText,
				Sensitive:        edit.Sensitive.Valid && edit.Sensitive.Bool,
				CreatedAt:        edit.CreatedAt.Time,
				Account:          account,
				Poll:             editPoll,
				MediaAttachments: editMedia,
				Emojis:           []CustomEmoji{},
			})
		}

		// Add current version as the latest entry
		history = append(history, StatusEdit{
			Content:          formatContent(dbStatus.Text),
			SpoilerText:      dbStatus.SpoilerText,
			Sensitive:        dbStatus.Sensitive,
			CreatedAt:        dbStatus.CreatedAt.Time,
			Account:          account,
			Poll:             currentPoll,
			MediaAttachments: currentMedia,
			Emojis:           []CustomEmoji{},
		})

		web.JSON(w, http.StatusOK, history)
		return
	}

	// Mock fallback - return empty array
	web.JSON(w, http.StatusOK, []StatusEdit{})
}

// Source handles GET /api/v1/statuses/:id/source
func (h *StatusesHandler) Source(w http.ResponseWriter, r *http.Request) {
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

	// Require authentication - only author can see source
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Only the author can view the source
		if dbStatus.AccountID != accountID {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		web.JSON(w, http.StatusOK, StatusSource{
			ID:          idStr,
			Text:        dbStatus.Text,
			SpoilerText: dbStatus.SpoilerText,
		})
		return
	}

	// Mock fallback
	web.JSON(w, http.StatusOK, StatusSource{
		ID:          idStr,
		Text:        "Hello, world!",
		SpoilerText: "",
	})
}

// formatContent converts plain text to HTML content
func formatContent(text string) string {
	if text == "" {
		return ""
	}
	// Simple HTML wrapping - real implementation would linkify mentions/hashtags/URLs
	return "<p>" + text + "</p>"
}

// Translation represents a translated status
type Translation struct {
	Content                string `json:"content"`
	SpoilerText            string `json:"spoiler_text,omitempty"`
	DetectedSourceLanguage string `json:"detected_source_language"`
	Language               string `json:"language"`
	Provider               string `json:"provider"`
}

// Translate handles POST /api/v1/statuses/:id/translate
func (h *StatusesHandler) Translate(w http.ResponseWriter, r *http.Request) {
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

	// Parse target language (default to "en")
	targetLang := web.QueryParam(r, "lang")
	if targetLang == "" {
		targetLang = "en"
	}

	ctx := r.Context()

	// Check if translation is enabled
	if h.cfg == nil || !h.cfg.Translation.Enabled() {
		web.JSONError(w, http.StatusUnprocessableEntity, "Translation is not enabled on this server")
		return
	}

	if h.useDB() {
		// Fetch the status
		status, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Only allow translation of public/unlisted statuses
		if status.Visibility != VisibilityPublic && status.Visibility != VisibilityUnlisted {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Create translation service
		svc := translation.NewService(translation.Config{
			Backend:  h.cfg.Translation.Backend,
			APIKey:   h.cfg.Translation.APIKey,
			Endpoint: h.cfg.Translation.Endpoint,
			Plan:     h.cfg.Translation.Plan,
		})

		// Translate the content
		result, err := svc.Translate(ctx, status.Text, targetLang)
		if err != nil {
			if errors.Is(err, translation.ErrTranslationNotConfigured) {
				web.JSONError(w, http.StatusUnprocessableEntity, "Translation is not enabled on this server")
			} else {
				web.JSONError(w, http.StatusServiceUnavailable, "Translation service unavailable")
			}
			return
		}

		// Also translate spoiler text if present
		spoilerText := ""
		if status.SpoilerText != "" {
			spoilerResult, err := svc.Translate(ctx, status.SpoilerText, targetLang)
			if err == nil {
				spoilerText = spoilerResult.Content
			}
		}

		web.JSON(w, http.StatusOK, Translation{
			Content:                formatContent(result.Content),
			SpoilerText:            spoilerText,
			DetectedSourceLanguage: result.DetectedSourceLanguage,
			Language:               result.Language,
			Provider:               result.Provider,
		})
		return
	}

	// Mock fallback - translation not available without DB
	web.JSONError(w, http.StatusUnprocessableEntity, "Translation is not enabled on this server")
}

// Quotes handles GET /api/v1/statuses/:id/quotes
// Returns statuses that have quoted this status
func (h *StatusesHandler) Quotes(w http.ResponseWriter, r *http.Request) {
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

	limit := parseLimit(web.QueryParam(r, "limit"), 40, 40)
	ctx := r.Context()

	if h.useDB() {
		// Verify status exists
		_, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Get quotes for this status
		quotes, err := h.queries.GetQuotesByQuotedStatusID(ctx, db.GetQuotesByQuotedStatusIDParams{
			QuotedStatusID: pgtype.Int8{Int64: id, Valid: true},
			Limit:          int32(limit),
			Offset:         0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Status{})
			return
		}

		// Get statuses that quote this one
		statuses := make([]Status, 0, len(quotes))
		for _, q := range quotes {
			dbStatus, err := h.queries.GetStatusByID(ctx, q.StatusID)
			if err == nil {
				statuses = append(statuses, SerializeStatus(ctx, h.queries, h.cfg, dbStatus))
			}
		}

		web.JSON(w, http.StatusOK, statuses)
		return
	}

	// Mock fallback
	web.JSON(w, http.StatusOK, []Status{})
}

// RevokeQuote handles POST /api/v1/statuses/:id/quotes/:quote_id/revoke
// Allows the quoted status author to reject/revoke a quote
func (h *StatusesHandler) RevokeQuote(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	statusIDStr := web.Param(r, "id")
	quoteIDStr := web.Param(r, "quote_id")

	statusID, err := strconv.ParseInt(statusIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	quoteID, err := strconv.ParseInt(quoteIDStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		// Verify the status exists and belongs to the user
		status, err := h.queries.GetStatusByID(ctx, statusID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		if status.AccountID != accountID {
			web.JSONError(w, http.StatusForbidden, "You do not have permission to revoke quotes on this status")
			return
		}

		// Get the quote
		quote, err := h.queries.GetQuote(ctx, quoteID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Verify quote is for this status
		if !quote.QuotedStatusID.Valid || quote.QuotedStatusID.Int64 != statusID {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Set quote state to rejected (state 2)
		_, err = h.queries.UpdateQuoteState(ctx, db.UpdateQuoteStateParams{
			ID:    quoteID,
			State: 2, // rejected
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to revoke quote")
			return
		}
	}

	web.JSON(w, http.StatusOK, map[string]interface{}{})
}

// RegisterRoutes registers the statuses routes
func (h *StatusesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/statuses", h.Create)
	mux.HandleFunc("GET /api/v1/statuses/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/statuses/{id}", h.Edit)
	mux.HandleFunc("DELETE /api/v1/statuses/{id}", h.Delete)
	mux.HandleFunc("GET /api/v1/statuses/{id}/context", h.Context)
	mux.HandleFunc("GET /api/v1/statuses/{id}/history", h.History)
	mux.HandleFunc("GET /api/v1/statuses/{id}/source", h.Source)
	mux.HandleFunc("POST /api/v1/statuses/{id}/translate", h.Translate)
	mux.HandleFunc("GET /api/v1/statuses/{id}/quotes", h.Quotes)
	mux.HandleFunc("POST /api/v1/statuses/{id}/quotes/{quote_id}/revoke", h.RevokeQuote)
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
