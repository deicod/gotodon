// Package v2 provides the Mastodon-compatible REST API v2 handlers.
package v2

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// SearchHandler handles search endpoints
type SearchHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewSearchHandler creates a new search handler
func NewSearchHandler(queries *db.Queries, cfg *config.Config) *SearchHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &SearchHandler{queries: queries, cfg: cfg}
}

func (h *SearchHandler) useDB() bool {
	return h.queries != nil
}

// SearchResult is the response for /api/v2/search
type SearchResult struct {
	Accounts []v1.Account `json:"accounts"`
	Statuses []v1.Status  `json:"statuses"`
	Hashtags []Tag        `json:"hashtags"`
}

// Tag represents a hashtag in search results
type Tag struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	URL       string         `json:"url"`
	History   []HashtagUsage `json:"history"`
	Following *bool          `json:"following,omitempty"`
	Featuring *bool          `json:"featuring,omitempty"`
}

// HashtagUsage represents usage statistics for a hashtag
type HashtagUsage struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

// Search handles GET /api/v2/search
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(web.QueryParam(r, "q"))
	if q == "" {
		web.JSONError(w, http.StatusBadRequest, "Search query is required")
		return
	}

	// Parse parameters
	searchType := web.QueryParam(r, "type") // accounts, hashtags, statuses
	limit := 20
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 40 {
			limit = parsed
		}
	}
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get auth context (optional for search)
	accountID, _ := middleware.GetAccountID(r.Context())
	_ = accountID // Used for "following" logic in future

	result := SearchResult{
		Accounts: []v1.Account{},
		Statuses: []v1.Status{},
		Hashtags: []Tag{},
	}

	ctx := r.Context()

	if !h.useDB() {
		web.JSON(w, http.StatusOK, result)
		return
	}

	// Perform search based on type
	if searchType == "accounts" || searchType == "" {
		accounts, err := h.queries.SearchAccountsV2(ctx, db.SearchAccountsV2Params{
			Query:  q,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err == nil {
			serialized := v1.SerializeAccounts(ctx, h.queries, h.cfg, accounts, true)
			for _, account := range accounts {
				if apiAcc, ok := serialized[account.ID]; ok {
					result.Accounts = append(result.Accounts, apiAcc)
					continue
				}
				result.Accounts = append(result.Accounts, v1.SerializeAccount(h.cfg, account))
			}
		}
	}

	if searchType == "hashtags" || searchType == "" {
		tagQuery := strings.TrimPrefix(q, "#")
		hashtags, err := h.queries.SearchHashtags(ctx, db.SearchHashtagsParams{
			Query:  pgtype.Text{String: tagQuery, Valid: true},
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err == nil {
			tagIDs := make([]int64, 0, len(hashtags))
			for _, tag := range hashtags {
				tagIDs = append(tagIDs, tag.ID)
			}

			historyByID := map[int64][]HashtagUsage{}
			if len(tagIDs) > 0 {
				if rows, err := h.queries.ListTagHistories(ctx, tagIDs); err == nil {
					historyByID = buildHashtagHistories(rows)
				}
			}

			followed := map[int64]bool{}
			featured := map[int64]bool{}
			if accountID != 0 && len(tagIDs) > 0 {
				if ids, err := h.queries.ListFollowedTagIDs(ctx, db.ListFollowedTagIDsParams{
					AccountID: accountID,
					TagIds:    tagIDs,
				}); err == nil {
					for _, id := range ids {
						followed[id] = true
					}
				}
				if ids, err := h.queries.ListFeaturedTagIDs(ctx, db.ListFeaturedTagIDsParams{
					AccountID: accountID,
					TagIds:    tagIDs,
				}); err == nil {
					for _, id := range ids {
						if id.Valid {
							featured[id.Int64] = true
						}
					}
				}
			}

			for _, tag := range hashtags {
				history := historyByID[tag.ID]
				if history == nil {
					history = []HashtagUsage{}
				}
				entry := Tag{
					ID:      strconv.FormatInt(tag.ID, 10),
					Name:    tagDisplayName(tag),
					URL:     tagURL(tag.Name, h.cfg),
					History: history,
				}
				if accountID != 0 {
					isFollowing := followed[tag.ID]
					isFeaturing := featured[tag.ID]
					entry.Following = &isFollowing
					entry.Featuring = &isFeaturing
				}
				result.Hashtags = append(result.Hashtags, entry)
			}
		}
	}

	if searchType == "statuses" || searchType == "" {
		statuses, err := h.queries.SearchStatuses(ctx, db.SearchStatusesParams{
			Query:  q,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err == nil {
			dbStatuses := make([]db.Status, 0, len(statuses))
			for _, s := range statuses {
				dbStatuses = append(dbStatuses, statusFromSearchRow(s))
			}
			result.Statuses = v1.SerializeStatuses(ctx, h.queries, h.cfg, dbStatuses)
		}
	}

	web.JSON(w, http.StatusOK, result)
}

// RegisterRoutes registers search routes
func (h *SearchHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v2/search", h.Search)
}

func tagDisplayName(tag db.Tag) string {
	if tag.DisplayName.Valid && strings.TrimSpace(tag.DisplayName.String) != "" {
		return tag.DisplayName.String
	}
	return tag.Name
}

func tagURL(name string, cfg *config.Config) string {
	domain := ""
	if cfg != nil {
		domain = strings.TrimSpace(cfg.Instance.WebDomain)
		if domain == "" {
			domain = cfg.Instance.LocalDomain
		}
	}
	if domain == "" {
		return "/tags/" + name
	}
	return "https://" + domain + "/tags/" + name
}

func buildHashtagHistories(rows []db.ListTagHistoriesRow) map[int64][]HashtagUsage {
	histories := make(map[int64][]HashtagUsage)
	for _, row := range rows {
		if !row.Day.Valid {
			continue
		}
		histories[row.TagID] = append(histories[row.TagID], HashtagUsage{
			Day:      strconv.FormatInt(row.Day.Time.Unix(), 10),
			Uses:     strconv.FormatInt(row.Uses, 10),
			Accounts: strconv.FormatInt(row.Accounts, 10),
		})
	}
	return histories
}

func statusFromSearchRow(row db.SearchStatusesRow) db.Status {
	return db.Status{
		ID:                        row.ID,
		Uri:                       row.Uri,
		Text:                      row.Text,
		CreatedAt:                 row.CreatedAt,
		UpdatedAt:                 row.UpdatedAt,
		InReplyToID:               row.InReplyToID,
		ReblogOfID:                row.ReblogOfID,
		Url:                       row.Url,
		Sensitive:                 row.Sensitive,
		Visibility:                row.Visibility,
		SpoilerText:               row.SpoilerText,
		Reply:                     row.Reply,
		Language:                  row.Language,
		ConversationID:            row.ConversationID,
		Local:                     row.Local,
		AccountID:                 row.AccountID,
		ApplicationID:             row.ApplicationID,
		InReplyToAccountID:        row.InReplyToAccountID,
		PollID:                    row.PollID,
		DeletedAt:                 row.DeletedAt,
		EditedAt:                  row.EditedAt,
		Trendable:                 row.Trendable,
		OrderedMediaAttachmentIds: row.OrderedMediaAttachmentIds,
		FetchedRepliesAt:          row.FetchedRepliesAt,
		QuoteApprovalPolicy:       row.QuoteApprovalPolicy,
	}
}
