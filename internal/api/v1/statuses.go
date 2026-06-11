// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/conversations"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/translation"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

var (
	mentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_]+)(?:@([a-zA-Z0-9_.-]+))?`)
	tagRegex     = regexp.MustCompile(`#([\p{L}\p{N}_]+)`)
	urlRegex     = regexp.MustCompile(`https?://[^\s<>()]+`)
)

// Visibility constants
const (
	VisibilityPublic   = 0
	VisibilityUnlisted = 1
	VisibilityPrivate  = 2
	VisibilityDirect   = 3
	VisibilityLimited  = 4
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

const defaultStatusesLimit = 20

const (
	scheduledStatusMinOffset  = 5 * time.Minute
	scheduledStatusDailyLimit = 25
	scheduledStatusTotalLimit = 300
)

const (
	quotePolicyFlagUnsupported int32 = 1 << 0
	quotePolicyFlagPublic      int32 = 1 << 1
	quotePolicyFlagFollowers   int32 = 1 << 2
	quotePolicyFlagFollowing   int32 = 1 << 3
)

// StatusesHandler handles status-related endpoints
type StatusesHandler struct {
	queries       db.Querier
	pool          *pgxpool.Pool
	publisher     streaming.Publisher
	cfg           *config.Config
	outgoing      federation.OutgoingDeliverer
	threader      federation.ThreadFetcher
	linker        LinkCrawlerEnqueuer
	pushPublisher PushNotificationPublisher
}

// PushNotificationPublisher fans out Web Push deliveries for freshly created
// notifications (implemented by push.Publisher).
type PushNotificationPublisher interface {
	PublishNotification(ctx context.Context, notification db.Notification)
}

// NewStatusesHandler creates a new statuses handler
func NewStatusesHandler(queries db.Querier, pool *pgxpool.Pool, publisher streaming.Publisher, cfg *config.Config, outgoing federation.OutgoingDeliverer) *StatusesHandler {
	return &StatusesHandler{
		queries:   queries,
		pool:      pool,
		publisher: publisher,
		cfg:       cfg,
		outgoing:  outgoing,
	}
}

// SetThreadFetcher enables remote thread resolution for context requests.
func (h *StatusesHandler) SetThreadFetcher(fetcher federation.ThreadFetcher) {
	h.threader = fetcher
}

// SetPushPublisher enables Web Push delivery for notifications created by
// this handler.
func (h *StatusesHandler) SetPushPublisher(publisher PushNotificationPublisher) {
	h.pushPublisher = publisher
}

// LinkCrawlerEnqueuer schedules link crawler jobs for preview cards.
type LinkCrawlerEnqueuer interface {
	Enqueue(ctx context.Context, statusID int64, url string) error
}

// SetLinkCrawler configures link crawling for preview cards.
func (h *StatusesHandler) SetLinkCrawler(enqueuer LinkCrawlerEnqueuer) {
	h.linker = enqueuer
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
	Pinned             *bool          `json:"pinned,omitempty"`
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
type PreviewCardAuthor struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Account *Account `json:"account"`
}

type PreviewCard struct {
	URL              string              `json:"url"`
	Title            string              `json:"title"`
	Description      string              `json:"description"`
	Language         *string             `json:"language"`
	Type             string              `json:"type"`
	AuthorName       string              `json:"author_name"`
	AuthorURL        string              `json:"author_url"`
	ProviderName     string              `json:"provider_name"`
	ProviderURL      string              `json:"provider_url"`
	HTML             string              `json:"html"`
	Width            int                 `json:"width"`
	Height           int                 `json:"height"`
	Image            *string             `json:"image"`
	ImageDescription string              `json:"image_description"`
	EmbedURL         string              `json:"embed_url"`
	Blurhash         *string             `json:"blurhash"`
	PublishedAt      *time.Time          `json:"published_at"`
	Authors          []PreviewCardAuthor `json:"authors"`
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

type statusCreateError struct {
	Status  int
	Message string
}

func (e statusCreateError) Error() string {
	return e.Message
}

func (e statusCreateError) StatusCode() int {
	return e.Status
}

type statusCreationResult struct {
	Status         db.Status
	Sender         db.Account
	Tags           []string
	NotifTasks     []notificationTask
	ActiveMentions []db.Account
}

type mentionStore interface {
	GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error)
	GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error)
	GetMutesFromAccounts(ctx context.Context, arg db.GetMutesFromAccountsParams) ([]db.Mute, error)
	GetBlocksFromAccounts(ctx context.Context, arg db.GetBlocksFromAccountsParams) ([]db.Block, error)
	CreateMentions(ctx context.Context, arg db.CreateMentionsParams) error
	CreateNotifications(ctx context.Context, arg db.CreateNotificationsParams) ([]db.Notification, error)
}

type tagStore interface {
	UpsertTags(ctx context.Context, tags []string) error
	GetTagsByNames(ctx context.Context, names []string) ([]db.Tag, error)
	AddStatusTags(ctx context.Context, arg db.AddStatusTagsParams) error
}

type statusCreateStore interface {
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	CreateStatus(ctx context.Context, arg db.CreateStatusParams) (db.Status, error)
	UpdateStatusURI(ctx context.Context, arg db.UpdateStatusURIParams) error
	CreateStatusStats(ctx context.Context, statusID int64) (db.StatusStat, error)
	IncrementStatusReplies(ctx context.Context, statusID int64) error
	AttachMediaToStatus(ctx context.Context, arg db.AttachMediaToStatusParams) (int64, error)
	CreatePoll(ctx context.Context, arg db.CreatePollParams) (db.Poll, error)
	UpdateStatusPollID(ctx context.Context, arg db.UpdateStatusPollIDParams) error
	conversations.ConversationStore
	mentionStore
	tagStore
}

// processMentions parses mentions from status text and creates notifications
func (h *StatusesHandler) processMentions(ctx context.Context, q mentionStore, statusID int64, accountID int64, text string) ([]Mention, []notificationTask, []db.Account, error) {
	// Simple mention regex: @username or @username@domain
	matches := mentionRegex.FindAllStringSubmatch(text, -1)

	var mentions []Mention
	var tasks []notificationTask
	var activeAccounts []db.Account

	if len(matches) == 0 {
		return mentions, tasks, activeAccounts, nil
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
		return mentions, tasks, activeAccounts, nil
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
		return mentions, tasks, activeAccounts, nil
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
		if !silent {
			if acc, ok := accountMap[tid]; ok {
				activeAccounts = append(activeAccounts, acc)
			}
		}
	}

	if len(mentionAccountIDs) > 0 {
		// Bulk Create Mentions
		if err := q.CreateMentions(ctx, db.CreateMentionsParams{
			StatusID: statusID,
			Column2:  mentionAccountIDs,
			Column3:  mentionSilent,
		}); err != nil {
			return mentions, tasks, activeAccounts, err
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
			return mentions, tasks, activeAccounts, err
		}

		// Map back to tasks
		// Note: The order of returned notifications matches input order for simple UNNEST usually,
		// but concurrent inserts might strictly not guarantee order if we relied on ID.
		// However, we just need to pair them up.
		// A safer way is to map by AccountID if we include it in return.
		// Our CreateNotifications returns * including AccountID.

		serializedAccounts := map[int64]Account{}
		if len(accountMap) > 0 {
			accounts := make([]db.Account, 0, len(accountMap))
			for _, acc := range accountMap {
				accounts = append(accounts, acc)
			}
			if dataQueries, ok := q.(AccountDataQuerier); ok {
				serializedAccounts = serializeAccounts(ctx, dataQueries, h.cfg, accounts, true)
			}
		}

		for _, notif := range notifs {
			acc := accountMap[notif.AccountID]
			tasks = append(tasks, notificationTask{
				Notification: notif,
				Account:      acc,
			})

			apiAcc, ok := serializedAccounts[acc.ID]
			if !ok {
				apiAcc = SerializeAccount(h.cfg, acc)
			}

			// Populate return mentions (simple version)
			mentions = append(mentions, Mention{
				ID:       apiAcc.ID,
				Username: apiAcc.Username,
				URL:      apiAcc.URI, // Using URI as URL for mention per statuses_util.go
				Acct:     apiAcc.Acct,
			})
		}
	}

	return mentions, tasks, activeAccounts, nil
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
func (h *StatusesHandler) saveTags(ctx context.Context, q tagStore, statusID int64, tags []string) error {
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
	ScheduledAt *string  `json:"scheduled_at" form:"scheduled_at"`
	Poll        *PollReq `json:"poll" form:"poll"`
}

// PollReq is the poll creation request
type PollReq struct {
	Options    []string `json:"options" form:"options[]"`
	ExpiresIn  int      `json:"expires_in" form:"expires_in"`
	Multiple   bool     `json:"multiple" form:"multiple"`
	HideTotals bool     `json:"hide_totals" form:"hide_totals"`
}

func normalizeCreateStatusRequest(req *CreateStatusRequest) error {
	if req == nil {
		return statusCreateError{Status: http.StatusBadRequest, Message: "Invalid request"}
	}
	if req.Status == "" && len(req.MediaIDs) == 0 && req.Poll == nil {
		return statusCreateError{
			Status:  http.StatusUnprocessableEntity,
			Message: "Validation failed: Text can't be blank without media or poll",
		}
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}
	if _, valid := stringToVisibility[req.Visibility]; !valid {
		return statusCreateError{
			Status:  http.StatusUnprocessableEntity,
			Message: "Validation failed: Visibility is not included in the list",
		}
	}
	return nil
}

type scheduledStatusStore interface {
	CreateScheduledStatus(ctx context.Context, arg db.CreateScheduledStatusParams) (db.ScheduledStatus, error)
	AttachMediaToScheduledStatus(ctx context.Context, arg db.AttachMediaToScheduledStatusParams) (int64, error)
	DeleteScheduledStatus(ctx context.Context, arg db.DeleteScheduledStatusParams) error
	DetachMediaFromScheduledStatus(ctx context.Context, arg db.DetachMediaFromScheduledStatusParams) (int64, error)
}

type scheduledStatusParamsPayload struct {
	Text        string   `json:"text"`
	InReplyToID *string  `json:"in_reply_to_id"`
	MediaIDs    []string `json:"media_ids"`
	Sensitive   bool     `json:"sensitive"`
	SpoilerText string   `json:"spoiler_text"`
	Visibility  string   `json:"visibility"`
	Language    *string  `json:"language"`
	Poll        *PollReq `json:"poll"`
}

func parseScheduledAt(raw *string) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, trimmed)
		if err != nil {
			return nil, err
		}
	}
	utc := parsed.UTC()
	return &utc, nil
}

func buildScheduledStatusParams(req CreateStatusRequest) ([]byte, error) {
	payload := scheduledStatusParamsPayload{
		Text:        req.Status,
		InReplyToID: req.InReplyToID,
		MediaIDs:    req.MediaIDs,
		Sensitive:   req.Sensitive,
		SpoilerText: req.SpoilerText,
		Visibility:  req.Visibility,
		Language:    req.Language,
		Poll:        req.Poll,
	}
	return json.Marshal(payload)
}

func (h *StatusesHandler) validateScheduledStatusLimits(ctx context.Context, accountID int64, scheduledAt time.Time) error {
	if !h.useDB() || h.queries == nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Database not configured"}
	}
	total, err := h.queries.CountScheduledStatusesByAccount(ctx, accountID)
	if err != nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to check scheduled status limits"}
	}
	if total >= scheduledStatusTotalLimit {
		return statusCreateError{
			Status:  http.StatusUnprocessableEntity,
			Message: fmt.Sprintf("Validation failed: You can only have %d scheduled statuses", scheduledStatusTotalLimit),
		}
	}
	daily, err := h.queries.CountScheduledStatusesByAccountAndDate(ctx, db.CountScheduledStatusesByAccountAndDateParams{
		AccountID: accountID,
		Column2:   pgtype.Date{Time: scheduledAt, Valid: true},
	})
	if err != nil {
		return statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to check scheduled status limits"}
	}
	if daily >= scheduledStatusDailyLimit {
		return statusCreateError{
			Status:  http.StatusUnprocessableEntity,
			Message: fmt.Sprintf("Validation failed: You can only have %d scheduled statuses per day", scheduledStatusDailyLimit),
		}
	}
	return nil
}

func (h *StatusesHandler) createScheduledStatusTx(ctx context.Context, q scheduledStatusStore, accountID int64, req CreateStatusRequest, scheduledAt time.Time) (db.ScheduledStatus, error) {
	params, err := buildScheduledStatusParams(req)
	if err != nil {
		return db.ScheduledStatus{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to serialize scheduled status"}
	}

	scheduled, err := q.CreateScheduledStatus(ctx, db.CreateScheduledStatusParams{
		AccountID:   accountID,
		ScheduledAt: pgtype.Timestamptz{Time: scheduledAt, Valid: true},
		Params:      params,
	})
	if err != nil {
		return db.ScheduledStatus{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to create scheduled status"}
	}

	for _, mediaIDStr := range req.MediaIDs {
		mediaID, err := strconv.ParseInt(mediaIDStr, 10, 64)
		if err != nil {
			continue
		}
		rows, err := q.AttachMediaToScheduledStatus(ctx, db.AttachMediaToScheduledStatusParams{
			ID:                mediaID,
			ScheduledStatusID: pgtype.Int8{Int64: scheduled.ID, Valid: true},
			AccountID:         pgtype.Int8{Int64: accountID, Valid: true},
		})
		if err != nil || rows != 1 {
			_, _ = q.DetachMediaFromScheduledStatus(ctx, db.DetachMediaFromScheduledStatusParams{
				ScheduledStatusID: pgtype.Int8{Int64: scheduled.ID, Valid: true},
				AccountID:         pgtype.Int8{Int64: accountID, Valid: true},
			})
			_ = q.DeleteScheduledStatus(ctx, db.DeleteScheduledStatusParams{
				ID:        scheduled.ID,
				AccountID: accountID,
			})
			return db.ScheduledStatus{}, statusCreateError{Status: http.StatusForbidden, Message: "Media not found or not owned by you"}
		}
	}

	return scheduled, nil
}

func (h *StatusesHandler) createScheduledStatus(ctx context.Context, accountID int64, req CreateStatusRequest, scheduledAt time.Time) (db.ScheduledStatus, error) {
	if h.pool == nil {
		store, ok := h.queries.(scheduledStatusStore)
		if !ok {
			return db.ScheduledStatus{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Database not configured"}
		}
		return h.createScheduledStatusTx(ctx, store, accountID, req, scheduledAt)
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return db.ScheduledStatus{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to start transaction"}
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := db.New(tx)
	scheduled, err := h.createScheduledStatusTx(ctx, qtx, accountID, req, scheduledAt)
	if err != nil {
		return db.ScheduledStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ScheduledStatus{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to commit transaction"}
	}
	return scheduled, nil
}

func (h *StatusesHandler) createStatus(ctx context.Context, accountID int64, req CreateStatusRequest) (statusCreationResult, error) {
	if h.pool == nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to start transaction"}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to start transaction"}
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := h.createStatusTx(ctx, db.New(tx), accountID, req)
	if err != nil {
		return statusCreationResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to commit transaction"}
	}

	return result, nil
}

func (h *StatusesHandler) createStatusTx(ctx context.Context, qtx statusCreateStore, accountID int64, req CreateStatusRequest) (statusCreationResult, error) {
	if err := normalizeCreateStatusRequest(&req); err != nil {
		return statusCreationResult{}, err
	}
	visibility := stringToVisibility[req.Visibility]

	if h.cfg == nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Local domain not configured"}
	}

	// Fetch sender account for URI generation and later usage
	sender, err := qtx.GetAccountByID(ctx, accountID)
	if err != nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to fetch account"}
	}

	var inReplyToID, inReplyToAccountID pgtype.Int8
	var parentStatus *db.Status
	if req.InReplyToID != nil {
		id, err := strconv.ParseInt(*req.InReplyToID, 10, 64)
		if err == nil {
			inReplyToID = pgtype.Int8{Int64: id, Valid: true}
			// Get parent status to set reply account
			parent, err := qtx.GetStatusByID(ctx, id)
			if err == nil {
				inReplyToAccountID = pgtype.Int8{Int64: parent.AccountID, Valid: true}
				parentStatus = &parent
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
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to create status"}
	}

	// Generate and Update URI with Snowflake ID
	// Format: https://<domain>/users/<username>/statuses/<id>
	statusIDStr := strconv.FormatInt(dbStatus.ID, 10)
	uri := fmt.Sprintf("https://%s/users/%s/statuses/%s", h.cfg.Instance.LocalDomain, sender.Username, statusIDStr)

	if err := qtx.UpdateStatusURI(ctx, db.UpdateStatusURIParams{
		Uri: pgtype.Text{String: uri, Valid: true},
		ID:  dbStatus.ID,
	}); err != nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to update status URI"}
	}

	// Update struct for response
	dbStatus.Uri = pgtype.Text{String: uri, Valid: true}

	if req.Poll != nil {
		tallies := make([]int64, len(req.Poll.Options))
		expiresAt := time.Now().Add(time.Duration(req.Poll.ExpiresIn) * time.Second)
		dbPoll, err := qtx.CreatePoll(ctx, db.CreatePollParams{
			AccountID:     pgtype.Int8{Int64: accountID, Valid: true},
			StatusID:      pgtype.Int8{Int64: dbStatus.ID, Valid: true},
			ExpiresAt:     pgtype.Timestamptz{Time: expiresAt, Valid: true},
			Options:       req.Poll.Options,
			CachedTallies: tallies,
			Multiple:      req.Poll.Multiple,
			HideTotals:    req.Poll.HideTotals,
		})
		if err != nil {
			return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to create poll"}
		}
		if err := qtx.UpdateStatusPollID(ctx, db.UpdateStatusPollIDParams{
			ID:     dbStatus.ID,
			PollID: pgtype.Int8{Int64: dbPoll.ID, Valid: true},
		}); err != nil {
			return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to attach poll"}
		}
		dbStatus.PollID = pgtype.Int8{Int64: dbPoll.ID, Valid: true}
	}

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

	_, notifTasks, activeMentions, err := h.processMentions(ctx, qtx, dbStatus.ID, accountID, req.Status)
	if err != nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to process mentions"}
	}

	if visibility == VisibilityDirect {
		convParent := parentStatus
		if convParent != nil && convParent.Visibility != VisibilityDirect && !convParent.ConversationID.Valid {
			convParent = nil
		}
		updatedStatus, err := conversations.EnsureConversationForStatus(ctx, qtx, dbStatus, convParent)
		if err != nil {
			return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to update conversation"}
		}
		dbStatus = updatedStatus
		if err := conversations.AddStatusToAccountConversations(ctx, qtx, dbStatus, sender, activeMentions); err != nil {
			return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to update conversations"}
		}
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
			return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to attach media"}
		}
		if rows != 1 {
			return statusCreationResult{}, statusCreateError{Status: http.StatusForbidden, Message: "Media not found or not owned by you"}
		}
	}

	// Process Hashtags
	tags, _ := h.processTags(req.Status)
	if err := h.saveTags(ctx, qtx, dbStatus.ID, tags); err != nil {
		return statusCreationResult{}, statusCreateError{Status: http.StatusInternalServerError, Message: "Failed to save tags"}
	}

	return statusCreationResult{
		Status:         dbStatus,
		Sender:         sender,
		Tags:           tags,
		NotifTasks:     notifTasks,
		ActiveMentions: activeMentions,
	}, nil
}

func (h *StatusesHandler) publishCreatedStatus(ctx context.Context, accountID int64, result statusCreationResult) Status {
	apiStatus := SerializeStatus(ctx, h.queries, h.cfg, result.Status)

	// Publish streaming events
	if h.publisher != nil {
		streams := statusStreamsForPublish(accountID, result.Status.Visibility, result.Tags)
		_ = h.publisher.PublishStatusToRedis(context.Background(), apiStatus, streams)

		// Publish notifications
		senderAPI := h.dbAccountToAPI(result.Sender)
		for _, task := range result.NotifTasks {
			apiNotif := Notification{
				ID:        strconv.FormatInt(task.Notification.ID, 10),
				Type:      "mention",
				CreatedAt: task.Notification.CreatedAt.Time,
				Account:   senderAPI,
				Status:    &apiStatus,
			}
			_ = h.publisher.PublishNotificationToRedis(ctx, task.Account.ID, apiNotif)
		}

		if result.Status.Visibility == VisibilityDirect {
			h.publishConversationEvents(ctx, result.Status, result.Sender, result.ActiveMentions)
		}
	}

	if h.pushPublisher != nil {
		for _, task := range result.NotifTasks {
			h.pushPublisher.PublishNotification(ctx, task.Notification)
		}
	}

	if h.outgoing != nil {
		if err := h.outgoing.DeliverStatusCreate(ctx, result.Status.ID); err != nil {
			fmt.Printf("Failed to deliver Create activity: %v\n", err)
		}
	}

	if h.linker != nil {
		if link := extractFirstURL(result.Status.Text); link != "" {
			_ = h.linker.Enqueue(ctx, result.Status.ID, link)
		}
	}

	return apiStatus
}

func (h *StatusesHandler) publishStatusUpdate(ctx context.Context, status Status, streams []string) {
	if h == nil || h.publisher == nil || len(streams) == 0 {
		return
	}
	publisher, ok := h.publisher.(streaming.EventPublisher)
	if !ok {
		return
	}
	_ = publisher.PublishEventToRedis(ctx, streaming.EventStatusUpdate, status, streams)
}

func (h *StatusesHandler) publishConversationEvents(ctx context.Context, status db.Status, author db.Account, mentions []db.Account) {
	if h == nil || h.publisher == nil || h.queries == nil {
		return
	}
	if !status.ConversationID.Valid || status.ID == 0 {
		return
	}
	publisher, ok := h.publisher.(streaming.EventPublisher)
	if !ok {
		return
	}

	recipients := localRecipientsForStreaming(author, mentions)
	if len(recipients) == 0 {
		return
	}

	participants := participantAccountIDs(author, mentions)
	convHandler := &ConversationsHandler{queries: h.queries, cfg: h.cfg}

	for _, recipient := range recipients {
		participantIDs := removeAccountID(participants, recipient.ID)
		conv, err := h.queries.GetAccountConversationByParticipants(ctx, db.GetAccountConversationByParticipantsParams{
			AccountID:             recipient.ID,
			ConversationID:        status.ConversationID.Int64,
			ParticipantAccountIds: participantIDs,
		})
		if err != nil {
			continue
		}
		apiConv, err := convHandler.buildConversation(ctx, recipient.ID, conv)
		if err != nil {
			continue
		}
		_ = publisher.PublishEventToRedis(ctx, streaming.EventConversation, apiConv, []string{streaming.DirectStreamKey(recipient.ID)})
	}
}

func statusStreamsForPublish(accountID int64, visibility int32, tags []string) []string {
	streams := make([]string, 0, 2+len(tags))
	if accountID > 0 {
		streams = append(streams, streaming.UserStreamKey(accountID))
	}
	if visibility == VisibilityPublic {
		streams = append(streams, streaming.StreamPublic, streaming.StreamPublicLocal)
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		streams = append(streams, fmt.Sprintf("hashtag:%s", tag))
	}
	return streams
}

func (h *StatusesHandler) loadStatusTagNames(ctx context.Context, q db.Querier, statusID int64) []string {
	if h == nil || q == nil || statusID <= 0 {
		return nil
	}
	rows, err := q.GetTagsByStatusIDs(ctx, []int64{statusID})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		tag := strings.TrimSpace(row.Name)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func mergeTags(tags ...[]string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, list := range tags {
		for _, tag := range list {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			out = append(out, tag)
		}
	}
	return out
}

func participantAccountIDs(author db.Account, mentions []db.Account) []int64 {
	ids := make([]int64, 0, len(mentions)+1)
	if author.ID != 0 {
		ids = append(ids, author.ID)
	}
	for _, acc := range mentions {
		if acc.ID != 0 {
			ids = append(ids, acc.ID)
		}
	}
	return uniqueSortedIDs(ids)
}

func localRecipientsForStreaming(author db.Account, mentions []db.Account) []db.Account {
	recipients := make(map[int64]db.Account)
	if isLocalAccount(author) {
		recipients[author.ID] = author
	}
	for _, acc := range mentions {
		if isLocalAccount(acc) {
			recipients[acc.ID] = acc
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	out := make([]db.Account, 0, len(recipients))
	for _, acc := range recipients {
		out = append(out, acc)
	}
	slices.SortFunc(out, func(a, b db.Account) int {
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func isLocalAccount(acc db.Account) bool {
	return !acc.Domain.Valid || strings.TrimSpace(acc.Domain.String) == ""
}

func uniqueSortedIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	out := ids[:0]
	var prev int64
	for i, id := range ids {
		if i == 0 || id != prev {
			out = append(out, id)
			prev = id
		}
	}
	return out
}

func removeAccountID(ids []int64, target int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != target {
			out = append(out, id)
		}
	}
	return out
}

func extractFirstURL(text string) string {
	match := urlRegex.FindString(text)
	if match == "" {
		return ""
	}
	return trimTrailingPunctuation(match)
}

func trimTrailingPunctuation(value string) string {
	trimSet := ".,!?:;)]}\"'»”’"
	for len(value) > 0 {
		last := value[len(value)-1]
		if strings.ContainsRune(trimSet, rune(last)) {
			value = value[:len(value)-1]
			continue
		}
		break
	}
	return value
}

// Index handles GET /api/v1/statuses
func (h *StatusesHandler) Index(w http.ResponseWriter, r *http.Request) {
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read:statuses") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	ids := parseStatusIDs(r)
	if len(ids) > defaultStatusesLimit {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed")
		return
	}
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
		return
	}
	if len(ids) == 0 {
		web.JSON(w, http.StatusOK, []Status{})
		return
	}

	ctx := r.Context()
	dbStatuses, err := h.queries.GetStatusesByIDs(ctx, ids)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to fetch statuses")
		return
	}
	if len(dbStatuses) == 0 {
		web.JSON(w, http.StatusOK, []Status{})
		return
	}

	statusIDs := make([]int64, len(dbStatuses))
	authorIDs := make([]int64, 0, len(dbStatuses))
	authorIDSeen := make(map[int64]bool)
	for i, s := range dbStatuses {
		statusIDs[i] = s.ID
		if !authorIDSeen[s.AccountID] {
			authorIDs = append(authorIDs, s.AccountID)
			authorIDSeen[s.AccountID] = true
		}
	}

	dbAccounts, err := h.queries.GetAccountsByIDs(ctx, authorIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to fetch accounts")
		return
	}
	accountsByID := make(map[int64]db.Account, len(dbAccounts))
	for _, acc := range dbAccounts {
		accountsByID[acc.ID] = acc
	}

	viewerID, viewerOK := middleware.GetAccountID(ctx)
	viewerDomain := ""
	if viewerOK {
		viewer, err := h.queries.GetAccountByID(ctx, viewerID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch viewer account")
			return
		}
		if viewer.Domain.Valid {
			viewerDomain = viewer.Domain.String
		}
	}

	mentionedByStatus := make(map[int64]bool)
	if viewerOK {
		mentions, err := h.queries.GetMentionsByStatusIDs(ctx, statusIDs)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch mentions")
			return
		}
		for _, m := range mentions {
			if m.AccountID == viewerID {
				mentionedByStatus[m.StatusID] = true
			}
		}
	}

	followingAuthors := make(map[int64]bool)
	mutedAuthors := make(map[int64]bool)
	blockedAuthors := make(map[int64]bool)
	authorsBlockingViewer := make(map[int64]bool)
	blockedDomains := make(map[string]bool)
	authorsBlockingViewerDomain := make(map[int64]bool)

	if viewerOK && len(authorIDs) > 0 {
		follows, err := h.queries.GetFollowsByAccountIDs(ctx, db.GetFollowsByAccountIDsParams{
			AccountID:        viewerID,
			TargetAccountIds: authorIDs,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch follows")
			return
		}
		for _, targetID := range follows {
			followingAuthors[targetID] = true
		}

		mutes, err := h.queries.GetMutesByAccountIDs(ctx, db.GetMutesByAccountIDsParams{
			AccountID:        viewerID,
			TargetAccountIds: authorIDs,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch mutes")
			return
		}
		for _, m := range mutes {
			mutedAuthors[m.TargetAccountID] = true
		}

		blocks, err := h.queries.GetBlocksByAccountIDs(ctx, db.GetBlocksByAccountIDsParams{
			AccountID:        viewerID,
			TargetAccountIds: authorIDs,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch blocks")
			return
		}
		for _, b := range blocks {
			blockedAuthors[b.TargetAccountID] = true
		}

		blockedBy, err := h.queries.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
			TargetAccountID: viewerID,
			AccountIds:      authorIDs,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch blocking relationships")
			return
		}
		for _, b := range blockedBy {
			authorsBlockingViewer[b.AccountID] = true
		}
	}

	if viewerOK {
		domains, err := h.queries.ListDomainBlockDomains(ctx, viewerID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch domain blocks")
			return
		}
		for _, domain := range domains {
			if domain != "" {
				blockedDomains[domain] = true
			}
		}
	}

	if viewerOK && viewerDomain != "" && len(authorIDs) > 0 {
		blockedByDomain, err := h.queries.GetDomainBlocksByAccountsAndDomain(ctx, db.GetDomainBlocksByAccountsAndDomainParams{
			AccountIds: authorIDs,
			Domain:     viewerDomain,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch domain blocking relationships")
			return
		}
		for _, accountID := range blockedByDomain {
			authorsBlockingViewerDomain[accountID] = true
		}
	}

	isPermitted := func(status db.Status, author db.Account) bool {
		if viewerOK && viewerID == author.ID {
			return true
		}
		if author.SuspendedAt.Valid {
			return false
		}

		requiresMention := status.Visibility == VisibilityDirect || status.Visibility == VisibilityLimited
		isPrivate := status.Visibility == VisibilityPrivate

		if requiresMention {
			return viewerOK && mentionedByStatus[status.ID]
		}

		if isPrivate {
			if !viewerOK {
				return false
			}
			return mentionedByStatus[status.ID] || followingAuthors[author.ID]
		}

		if !viewerOK {
			return true
		}

		if authorsBlockingViewer[author.ID] {
			return false
		}
		if viewerDomain != "" && authorsBlockingViewerDomain[author.ID] {
			return false
		}
		return true
	}

	isFiltered := func(status db.Status, author db.Account) bool {
		if !viewerOK || viewerID == author.ID {
			return false
		}

		if blockedAuthors[author.ID] || mutedAuthors[author.ID] {
			return true
		}

		if author.Domain.Valid && blockedDomains[author.Domain.String] {
			return true
		}

		if author.SilencedAt.Valid && !followingAuthors[author.ID] {
			return true
		}

		return false
	}

	filtered := make([]db.Status, 0, len(dbStatuses))
	for _, s := range dbStatuses {
		author, ok := accountsByID[s.AccountID]
		if !ok {
			continue
		}
		if !isPermitted(s, author) || isFiltered(s, author) {
			continue
		}
		filtered = append(filtered, s)
	}

	web.JSON(w, http.StatusOK, SerializeStatuses(ctx, h.queries, h.cfg, filtered))
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

	ctx := r.Context()
	parsedScheduledAt, err := parseScheduledAt(req.ScheduledAt)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Scheduled at is invalid")
		return
	}
	now := time.Now().UTC()
	if parsedScheduledAt != nil && !parsedScheduledAt.After(now) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Scheduled status must be in the future")
		return
	}

	if h.useDB() {
		if parsedScheduledAt != nil {
			if err := normalizeCreateStatusRequest(&req); err != nil {
				var createErr statusCreateError
				if errors.As(err, &createErr) {
					web.JSONError(w, createErr.Status, createErr.Message)
					return
				}
				web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
				return
			}
			if !parsedScheduledAt.After(now.Add(scheduledStatusMinOffset)) {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Scheduled status must be at least 5 minutes in the future")
				return
			}
			if err := h.validateScheduledStatusLimits(ctx, accountID, *parsedScheduledAt); err != nil {
				var createErr statusCreateError
				if errors.As(err, &createErr) {
					web.JSONError(w, createErr.Status, createErr.Message)
					return
				}
				web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
				return
			}
			scheduled, err := h.createScheduledStatus(ctx, accountID, req, *parsedScheduledAt)
			if err != nil {
				var createErr statusCreateError
				if errors.As(err, &createErr) {
					web.JSONError(w, createErr.Status, createErr.Message)
					return
				}
				web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
				return
			}
			mediaMap := scheduledStatusMediaAttachments(ctx, h.queries, []int64{scheduled.ID})
			web.JSON(w, http.StatusOK, serializeScheduledStatus(scheduled, mediaMap[scheduled.ID]))
			return
		}

		result, err := h.createStatus(ctx, accountID, req)
		if err != nil {
			var createErr statusCreateError
			if errors.As(err, &createErr) {
				web.JSONError(w, createErr.Status, createErr.Message)
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
			return
		}

		apiStatus := h.publishCreatedStatus(ctx, accountID, result)
		web.JSON(w, http.StatusOK, apiStatus)
		return
	}

	if err := normalizeCreateStatusRequest(&req); err != nil {
		var createErr statusCreateError
		if errors.As(err, &createErr) {
			web.JSONError(w, createErr.Status, createErr.Message)
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to create status")
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
		return
	}

	if h.useDB() {
		dbStatus, err := h.queries.GetStatusByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, SerializeStatus(ctx, h.queries, h.cfg, dbStatus))
		return
	}
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

		var mentionAccounts []db.Account
		var activeMentionAccounts []db.Account
		mentionsResolved := false
		if h.outgoing != nil || dbStatus.Visibility == VisibilityDirect {
			mentions, err := qtx.GetMentionsByStatusID(ctx, id)
			if err != nil {
				fmt.Printf("Failed to load mentions for delete: %v\n", err)
			} else if len(mentions) == 0 {
				mentionsResolved = true
			} else {
				seen := make(map[int64]bool)
				ids := make([]int64, 0, len(mentions))
				activeIDs := make([]int64, 0, len(mentions))
				for _, mention := range mentions {
					if mention.AccountID == 0 || seen[mention.AccountID] {
						continue
					}
					seen[mention.AccountID] = true
					ids = append(ids, mention.AccountID)
					if !mention.Silent {
						activeIDs = append(activeIDs, mention.AccountID)
					}
				}
				if len(ids) > 0 {
					accounts, err := qtx.GetAccountsByIDs(ctx, ids)
					if err != nil {
						fmt.Printf("Failed to load mention accounts for delete: %v\n", err)
					} else {
						mentionsResolved = true
						mentionAccounts = accounts
						if len(activeIDs) > 0 {
							accountMap := make(map[int64]db.Account, len(accounts))
							for _, acc := range accounts {
								accountMap[acc.ID] = acc
							}
							for _, id := range activeIDs {
								if acc, ok := accountMap[id]; ok {
									activeMentionAccounts = append(activeMentionAccounts, acc)
								}
							}
						}
					}
				} else {
					mentionsResolved = true
				}
			}
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

		if dbStatus.Visibility == VisibilityDirect && mentionsResolved {
			author, err := qtx.GetAccountByID(ctx, dbStatus.AccountID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to load author")
				return
			}
			if err := conversations.RemoveStatusFromAccountConversations(ctx, qtx, dbStatus, author, activeMentionAccounts); err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to update conversations")
				return
			}
		}

		if err := tx.Commit(ctx); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to commit transaction")
			return
		}

		if h.outgoing != nil {
			if err := h.outgoing.DeliverStatusDelete(ctx, dbStatus, mentionAccounts); err != nil {
				fmt.Printf("Failed to deliver Delete activity: %v\n", err)
			}
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
		favourite, err := h.queries.CreateFavourite(ctx, db.CreateFavouriteParams{
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

		if h.outgoing != nil {
			if err := h.outgoing.DeliverLike(ctx, favourite); err != nil {
				fmt.Printf("Failed to deliver Like activity: %v\n", err)
			}
		}

		status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
		status.Favourited = true
		status.FavouritesCount++
		web.JSON(w, http.StatusOK, status)
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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

		var favourite db.Favourite
		var hasFavourite bool
		if h.outgoing != nil {
			favourite, err = h.queries.GetFavourite(ctx, db.GetFavouriteParams{
				AccountID: accountID,
				StatusID:  id,
			})
			if err == nil {
				hasFavourite = true
			}
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

		if hasFavourite && h.outgoing != nil {
			if err := h.outgoing.DeliverUndoLike(ctx, favourite); err != nil {
				fmt.Printf("Failed to deliver Undo Like activity: %v\n", err)
			}
		}
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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

		actor, err := h.queries.GetAccountByID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
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

		reblogURI := fmt.Sprintf("https://%s/users/%s/statuses/%d", h.cfg.Instance.LocalDomain, actor.Username, reblogStatus.ID)
		if err := h.queries.UpdateStatusURI(ctx, db.UpdateStatusURIParams{
			ID:  reblogStatus.ID,
			Uri: pgtype.Text{String: reblogURI, Valid: true},
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update reblog URI")
			return
		}
		reblogStatus.Uri = pgtype.Text{String: reblogURI, Valid: true}

		_ = h.queries.IncrementStatusReblogs(ctx, id)

		original := SerializeStatus(ctx, h.queries, h.cfg, originalStatus)
		original.ReblogsCount++

		reblog := SerializeStatus(ctx, h.queries, h.cfg, reblogStatus)
		reblog.Reblog = &original
		reblog.Reblogged = true

		if h.outgoing != nil {
			if err := h.outgoing.DeliverAnnounce(ctx, reblogStatus.ID); err != nil {
				fmt.Printf("Failed to deliver Announce activity: %v\n", err)
			}
		}

		web.JSON(w, http.StatusOK, reblog)
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
}

// Unreblog handles POST /api/v1/statuses/:id/unreblog
func (h *StatusesHandler) Unreblog(w http.ResponseWriter, r *http.Request) {
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

		var reblogStatus db.Status
		var hasReblog bool
		if h.outgoing != nil {
			reblogStatus, err = h.queries.GetReblogByAccountAndOriginal(ctx, db.GetReblogByAccountAndOriginalParams{
				AccountID:  accountID,
				ReblogOfID: pgtype.Int8{Int64: id, Valid: true},
			})
			if err == nil {
				hasReblog = true
			}
		}

		if err := h.queries.DecrementStatusReblogs(ctx, id); err != nil {
			fmt.Printf("Failed to decrement reblogs count: %v\n", err)
		}

		status := SerializeStatus(ctx, h.queries, h.cfg, dbStatus)
		status.Reblogged = false
		web.JSON(w, http.StatusOK, status)

		if hasReblog && h.outgoing != nil {
			if err := h.outgoing.DeliverUndoAnnounce(ctx, reblogStatus); err != nil {
				fmt.Printf("Failed to deliver Undo Announce activity: %v\n", err)
			}
		}
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
}

// Mute handles POST /api/v1/statuses/:id/mute
func (h *StatusesHandler) Mute(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if !oauth.HasScope(scopes, "write:mutes") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
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
		if !status.ConversationID.Valid {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Conversation is missing")
			return
		}

		if err := h.queries.CreateConversationMute(ctx, db.CreateConversationMuteParams{
			AccountID:      accountID,
			ConversationID: status.ConversationID.Int64,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to mute conversation")
			return
		}

		s := SerializeStatus(ctx, h.queries, h.cfg, status)
		s.Muted = true
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
}

// Unmute handles POST /api/v1/statuses/:id/unmute
func (h *StatusesHandler) Unmute(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if !oauth.HasScope(scopes, "write:mutes") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
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
		if !status.ConversationID.Valid {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Conversation is missing")
			return
		}

		if err := h.queries.DeleteConversationMute(ctx, db.DeleteConversationMuteParams{
			AccountID:      accountID,
			ConversationID: status.ConversationID.Int64,
		}); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to unmute conversation")
			return
		}

		s := SerializeStatus(ctx, h.queries, h.cfg, status)
		s.Muted = false
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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
		if h.threader != nil {
			if status, err := h.queries.GetStatusByID(ctx, id); err == nil {
				_ = h.threader.FetchThread(ctx, status)
			}
		}
		// Get ancestors
		ancestors, err := h.queries.GetStatusAncestors(ctx, id)
		if err != nil {
			ancestors = []db.Status{}
		}
		// Batch serialize ancestors to avoid N+1 queries
		ancestorStatuses := SerializeStatuses(ctx, h.queries, h.cfg, ancestors)

		// Get descendants
		descendants, err := h.queries.GetStatusDescendants(ctx, pgtype.Int8{Int64: id, Valid: true})
		if err != nil {
			descendants = []db.Status{}
		}
		// Batch serialize descendants to avoid N+1 queries
		descendantStatuses := SerializeStatuses(ctx, h.queries, h.cfg, descendants)

		web.JSON(w, http.StatusOK, map[string][]Status{
			"ancestors":   ancestorStatuses,
			"descendants": descendantStatuses,
		})
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
}

func parseStatusIDs(r *http.Request) []int64 {
	query := r.URL.Query()
	rawIDs := append([]string{}, query["id"]...)
	rawIDs = append(rawIDs, query["id[]"]...)
	rawIDs = uniqueStrings(rawIDs)

	ids := make([]int64, 0, len(rawIDs))
	for _, value := range rawIDs {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			id = 0
		}
		ids = append(ids, id)
	}
	return ids
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

type interactionPolicyRequest struct {
	QuoteApprovalPolicy string `json:"quote_approval_policy" form:"quote_approval_policy"`
}

func decodeInteractionPolicyRequest(r *http.Request) (interactionPolicyRequest, error) {
	var req interactionPolicyRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		return req, err
	}
	return req, nil
}

func quoteApprovalPolicyValue(name string) (int32, bool) {
	switch name {
	case "public":
		return quotePolicyFlagPublic << 16, true
	case "followers":
		return quotePolicyFlagFollowers << 16, true
	case "nobody":
		return 0, true
	default:
		return 0, false
	}
}

func defaultQuotePolicy(ctx context.Context, q db.Querier, accountID int64) string {
	if q == nil {
		return "public"
	}

	user, err := q.GetUserByAccountID(ctx, accountID)
	if err != nil || !user.Settings.Valid {
		return "public"
	}

	var settings map[string]any
	if err := json.Unmarshal([]byte(user.Settings.String), &settings); err != nil {
		return "public"
	}

	if value, ok := settings["default_quote_policy"].(string); ok && value != "" {
		return value
	}

	return "public"
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
		var newNotifTasks []notificationTask
		if len(newMentions) > 0 {
			// Check mute/block for new mentions only
			// Reuse parts of processMentions logic
			_, tasks, err := h.createMentionsAndNotifications(r.Context(), qtx, statusID, accountID, newMentions)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to add new mentions")
				return
			}
			newNotifTasks = tasks
		}

		// Handle Hashtags Sync (Replace all)
		oldTags := h.loadStatusTagNames(r.Context(), qtx, statusID)
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
		streamTags := mergeTags(oldTags, newTags)

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
		if h.outgoing != nil {
			if err := h.outgoing.DeliverStatusUpdate(r.Context(), statusID); err != nil {
				fmt.Printf("Failed to deliver Update activity: %v\n", err)
			}
		}
		if h.publisher != nil {
			h.publishStatusUpdate(r.Context(), s, statusStreamsForPublish(updatedStatus.AccountID, updatedStatus.Visibility, streamTags))
		}
		if h.pushPublisher != nil {
			for _, task := range newNotifTasks {
				h.pushPublisher.PublishNotification(r.Context(), task.Notification)
			}
		}
		web.JSON(w, http.StatusOK, s)
		return
	}

	web.JSONError(w, http.StatusNotFound, "Record not found")
}

// InteractionPolicy handles PUT /api/v1/statuses/:id/interaction_policy
func (h *StatusesHandler) InteractionPolicy(w http.ResponseWriter, r *http.Request) {
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

	statusID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	req, err := decodeInteractionPolicyRequest(r)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	policyName := strings.ToLower(strings.TrimSpace(req.QuoteApprovalPolicy))
	if policyName == "" {
		policyName = defaultQuotePolicy(r.Context(), h.queries, accountID)
	}

	policyValue, ok := quoteApprovalPolicyValue(policyName)
	if !ok {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Quote approval policy is invalid")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	status, err := h.queries.GetStatusByID(ctx, statusID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if status.AccountID != accountID {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if status.Local.Valid && status.Local.Bool && status.Visibility != VisibilityPublic && status.Visibility != VisibilityUnlisted {
		policyValue = 0
	}

	updatedStatus, err := h.queries.UpdateStatusQuoteApprovalPolicy(ctx, db.UpdateStatusQuoteApprovalPolicyParams{
		ID:                  statusID,
		QuoteApprovalPolicy: policyValue,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to update status")
		return
	}

	if h.outgoing != nil {
		if err := h.outgoing.DeliverStatusUpdate(ctx, statusID); err != nil {
			fmt.Printf("Failed to deliver Update activity: %v\n", err)
		}
	}

	web.JSON(w, http.StatusOK, SerializeStatus(ctx, h.queries, h.cfg, updatedStatus))
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

		// Return the created notifications so the caller can fan out push
		// deliveries after the surrounding transaction commits.
		for _, notif := range notifs {
			tasks = append(tasks, notificationTask{Notification: notif})
		}

		// Unlike processMentions, tasks carry only the Notification (for push
		// fan-out); mentions stay empty since Edit returns the Status, whose
		// "Mentions" list is rebuilt separately.
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
		pinnedValue := true
		s.Pinned = &pinnedValue
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
		pinnedValue := false
		s.Pinned = &pinnedValue
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

	expired := expiresAt != nil && time.Now().After(*expiresAt)

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

		// Check visibility - private and direct statuses require authorization
		viewerAccountID, isAuthenticated := middleware.GetAccountID(ctx)

		// For private (followers-only) statuses, viewer must be author or follower
		// For direct statuses, viewer must be author or mentioned
		if dbStatus.Visibility == VisibilityPrivate || dbStatus.Visibility == VisibilityDirect {
			if !isAuthenticated {
				web.JSONError(w, http.StatusNotFound, "Record not found")
				return
			}

			// Author can always see their own status
			if viewerAccountID != dbStatus.AccountID {
				// For direct messages, check if viewer is mentioned
				if dbStatus.Visibility == VisibilityDirect {
					mentions, err := h.queries.GetMentionsByStatusID(ctx, id)
					if err != nil {
						web.JSONError(w, http.StatusNotFound, "Record not found")
						return
					}
					isMentioned := false
					for _, m := range mentions {
						if m.AccountID == viewerAccountID {
							isMentioned = true
							break
						}
					}
					if !isMentioned {
						web.JSONError(w, http.StatusNotFound, "Record not found")
						return
					}
				} else {
					// For private (followers-only), check if viewer follows the author
					_, err := h.queries.GetFollow(ctx, db.GetFollowParams{
						AccountID:       viewerAccountID,
						TargetAccountID: dbStatus.AccountID,
					})
					if err != nil {
						web.JSONError(w, http.StatusNotFound, "Record not found")
						return
					}
				}
			}
		}

		// Get the status author
		dbAccount, err := h.queries.GetAccountByID(ctx, dbStatus.AccountID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
			return
		}

		account := h.dbAccountToAPI(dbAccount)
		local := !dbStatus.Local.Valid || dbStatus.Local.Bool

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
				Content:          formatStatusText(h.cfg, local, edit.Text, statusContentOptions{author: &account}),
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
			Content:          formatStatusText(h.cfg, local, dbStatus.Text, statusContentOptions{author: &account}),
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

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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
			Content:                formatStatusText(h.cfg, true, result.Content, statusContentOptions{}),
			SpoilerText:            spoilerText,
			DetectedSourceLanguage: result.DetectedSourceLanguage,
			Language:               result.Language,
			Provider:               result.Provider,
		})
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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

	web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
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
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Status data unavailable")
		return
	}

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
	mux.HandleFunc("GET /api/v1/statuses", h.Index)
	mux.HandleFunc("POST /api/v1/statuses", h.Create)
	mux.HandleFunc("GET /api/v1/statuses/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/statuses/{id}", h.Edit)
	mux.HandleFunc("PATCH /api/v1/statuses/{id}", h.Edit)
	mux.HandleFunc("DELETE /api/v1/statuses/{id}", h.Delete)
	mux.HandleFunc("GET /api/v1/statuses/{id}/context", h.Context)
	mux.HandleFunc("GET /api/v1/statuses/{id}/history", h.History)
	mux.HandleFunc("GET /api/v1/statuses/{id}/source", h.Source)
	mux.HandleFunc("PUT /api/v1/statuses/{id}/interaction_policy", h.InteractionPolicy)
	mux.HandleFunc("PATCH /api/v1/statuses/{id}/interaction_policy", h.InteractionPolicy)
	mux.HandleFunc("POST /api/v1/statuses/{id}/translate", h.Translate)
	mux.HandleFunc("GET /api/v1/statuses/{id}/quotes", h.Quotes)
	mux.HandleFunc("POST /api/v1/statuses/{id}/quotes/{quote_id}/revoke", h.RevokeQuote)
	mux.HandleFunc("POST /api/v1/statuses/{id}/favourite", h.Favourite)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unfavourite", h.Unfavourite)
	mux.HandleFunc("POST /api/v1/statuses/{id}/reblog", h.Reblog)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unreblog", h.Unreblog)
	mux.HandleFunc("POST /api/v1/statuses/{id}/bookmark", h.Bookmark)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unbookmark", h.Unbookmark)
	mux.HandleFunc("POST /api/v1/statuses/{id}/mute", h.Mute)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unmute", h.Unmute)
	mux.HandleFunc("POST /api/v1/statuses/{id}/pin", h.Pin)
	mux.HandleFunc("POST /api/v1/statuses/{id}/unpin", h.Unpin)
	mux.HandleFunc("GET /api/v1/statuses/{id}/favourited_by", h.FavouritedBy)
	mux.HandleFunc("GET /api/v1/statuses/{id}/reblogged_by", h.RebloggedBy)
}
