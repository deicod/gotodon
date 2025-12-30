// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// TrendsHandler handles trends endpoints
type TrendsHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewTrendsHandler creates a new trends handler
func NewTrendsHandler(queries *db.Queries, cfg *config.Config) *TrendsHandler {
	return &TrendsHandler{queries: queries, cfg: cfg}
}

// TrendingTag represents a trending hashtag
type TrendingTag struct {
	Name      string         `json:"name"`
	URL       string         `json:"url"`
	History   []HashtagUsage `json:"history"`
	Following bool           `json:"following,omitempty"`
}

// HashtagUsage represents usage statistics for a hashtag
type HashtagUsage struct {
	Day      string `json:"day"`
	Uses     string `json:"uses"`
	Accounts string `json:"accounts"`
}

// TrendingStatus represents a trending status
type TrendingStatus struct {
	Status
}

// TrendingLink represents a trending link
type TrendingLink struct {
	PreviewCard
	History []HashtagUsage `json:"history"`
}

// Index handles GET /api/v1/trends (alias for /api/v1/trends/tags)
func (h *TrendsHandler) Index(w http.ResponseWriter, r *http.Request) {
	h.Tags(w, r)
}

// Tags handles GET /api/v1/trends/tags
func (h *TrendsHandler) Tags(w http.ResponseWriter, r *http.Request) {
	limit := parseTrendLimit(web.QueryParam(r, "limit"), 10, 20)

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []TrendingTag{})
		return
	}

	tags, err := h.queries.GetTrendingTags(r.Context(), int32(limit))
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := []TrendingTag{}
	for _, t := range tags {
		resp = append(resp, TrendingTag{
			Name: t.Name,
			URL:  h.tagURL(t.Name),
			History: []HashtagUsage{
				{
					Day:      strconv.FormatInt(time.Now().Unix(), 10),
					Uses:     strconv.FormatInt(t.UsageCount, 10),
					Accounts: strconv.FormatInt(t.AccountCount, 10),
				},
			},
		})
	}

	web.JSON(w, http.StatusOK, resp)
}

// Statuses handles GET /api/v1/trends/statuses
func (h *TrendsHandler) Statuses(w http.ResponseWriter, r *http.Request) {
	limit := parseTrendLimit(web.QueryParam(r, "limit"), 20, 40)

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []Status{})
		return
	}

	statuses, err := h.queries.GetTrendingStatuses(r.Context(), int32(limit))
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := []Status{}
	for _, s := range statuses {
		account := Account{
			Username:    s.AccountUsername,
			DisplayName: s.AccountDisplayName,
		}
		if s.AccountAvatar.Valid {
			account.Avatar = s.AccountAvatar.String
		}

		resp = append(resp, Status{
			ID:         strconv.FormatInt(s.ID, 10),
			Content:    s.Text,
			Account:    account,
			Visibility: "public",
			// Minimal fields
		})
	}

	web.JSON(w, http.StatusOK, resp)
}

// Links handles GET /api/v1/trends/links
func (h *TrendsHandler) Links(w http.ResponseWriter, r *http.Request) {
	limit := parseTrendLimit(web.QueryParam(r, "limit"), 10, 20)
	offset := parseTrendOffset(web.QueryParam(r, "offset"))

	if h.queries == nil {
		web.JSON(w, http.StatusOK, []TrendingLink{})
		return
	}

	rows, err := h.queries.GetTrendingLinks(r.Context(), db.GetTrendingLinksParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	authorAccounts := h.previewCardAuthorAccounts(r.Context(), rows)

	resp := make([]TrendingLink, 0, len(rows))
	for _, row := range rows {
		card := previewCardFromTrendRow(row)
		preview := serializePreviewCard(card, authorAccounts)
		if preview == nil {
			continue
		}
		resp = append(resp, TrendingLink{
			PreviewCard: *preview,
			History:     []HashtagUsage{trendHistoryEntry(row.UsageCount, row.AccountCount)},
		})
	}

	web.JSON(w, http.StatusOK, resp)
}

func parseTrendLimit(s string, defaultVal, maxVal int) int {
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

func parseTrendOffset(s string) int {
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func (h *TrendsHandler) tagURL(name string) string {
	domain := "localhost:3000"
	if h.cfg != nil && h.cfg.Instance.LocalDomain != "" {
		domain = h.cfg.Instance.LocalDomain
	}
	return "https://" + domain + "/tags/" + name
}

func trendHistoryEntry(uses, accounts int64) HashtagUsage {
	return HashtagUsage{
		Day:      strconv.FormatInt(time.Now().Unix(), 10),
		Uses:     strconv.FormatInt(uses, 10),
		Accounts: strconv.FormatInt(accounts, 10),
	}
}

func previewCardFromTrendRow(row db.GetTrendingLinksRow) db.PreviewCard {
	return db.PreviewCard{
		ID:                        row.ID,
		Url:                       row.Url,
		Title:                     row.Title,
		Description:               row.Description,
		ImageFileName:             row.ImageFileName,
		ImageContentType:          row.ImageContentType,
		ImageFileSize:             row.ImageFileSize,
		ImageUpdatedAt:            row.ImageUpdatedAt,
		Type:                      row.Type,
		Html:                      row.Html,
		AuthorName:                row.AuthorName,
		AuthorUrl:                 row.AuthorUrl,
		ProviderName:              row.ProviderName,
		ProviderUrl:               row.ProviderUrl,
		Width:                     row.Width,
		Height:                    row.Height,
		CreatedAt:                 row.CreatedAt,
		UpdatedAt:                 row.UpdatedAt,
		EmbedUrl:                  row.EmbedUrl,
		ImageStorageSchemaVersion: row.ImageStorageSchemaVersion,
		Blurhash:                  row.Blurhash,
		Language:                  row.Language,
		MaxScore:                  row.MaxScore,
		MaxScoreAt:                row.MaxScoreAt,
		Trendable:                 row.Trendable,
		LinkType:                  row.LinkType,
		PublishedAt:               row.PublishedAt,
		ImageDescription:          row.ImageDescription,
		AuthorAccountID:           row.AuthorAccountID,
	}
}

func (h *TrendsHandler) previewCardAuthorAccounts(ctx context.Context, rows []db.GetTrendingLinksRow) map[int64]Account {
	authorIDs := make(map[int64]struct{})
	for _, row := range rows {
		if row.AuthorAccountID.Valid {
			authorIDs[row.AuthorAccountID.Int64] = struct{}{}
		}
	}
	authorAccounts := map[int64]Account{}
	if len(authorIDs) == 0 {
		return authorAccounts
	}

	ids := make([]int64, 0, len(authorIDs))
	for id := range authorIDs {
		ids = append(ids, id)
	}
	dbAccounts, err := h.queries.GetAccountsByIDs(ctx, ids)
	if err != nil {
		return authorAccounts
	}
	for _, acc := range dbAccounts {
		authorAccounts[acc.ID] = SerializeAccount(h.cfg, acc)
	}
	return authorAccounts
}

// RegisterRoutes registers trends routes
func (h *TrendsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/trends", h.Index)
	mux.HandleFunc("GET /api/v1/trends/tags", h.Tags)
	mux.HandleFunc("GET /api/v1/trends/statuses", h.Statuses)
	mux.HandleFunc("GET /api/v1/trends/links", h.Links)
}
