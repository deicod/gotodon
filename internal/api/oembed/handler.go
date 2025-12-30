// Package oembed provides the /api/oembed endpoint.
package oembed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/jackc/pgx/v5"
)

const (
	oembedType         = "rich"
	oembedVersion      = "1.0"
	oembedCacheAge     = 86400
	oembedDefaultWidth = 400

	visibilityPublic   = 0
	visibilityUnlisted = 1
)

const (
	oembedBlockquoteStyle = "background: #FCF8FF; border-radius: 8px; border: 1px solid #C9C4DA; margin: 0; max-width: 540px; min-width: 270px; overflow: hidden; padding: 0;"
	oembedStatusLinkStyle = "align-items: center; color: #1C1A25; display: flex; flex-direction: column; font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Oxygen, Ubuntu, Cantarell, 'Fira Sans', 'Droid Sans', 'Helvetica Neue', Roboto, sans-serif; font-size: 14px; justify-content: center; letter-spacing: 0.25px; line-height: 20px; padding: 24px; text-decoration: none;"
	oembedAccountStyle    = "color: #787588; margin-top: 16px;"
	oembedViewStyle       = "font-weight: 500;"
)

type store interface {
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
}

// Handler serves the /api/oembed endpoint.
type Handler struct {
	store store
	cfg   *config.Config
}

// NewHandler creates a new oEmbed handler.
func NewHandler(store store, cfg *config.Config) *Handler {
	return &Handler{store: store, cfg: cfg}
}

// RegisterRoutes registers oEmbed routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/oembed", h.Show)
}

// Show handles GET /api/oembed.
func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(web.QueryParam(r, "url"))
	if rawURL == "" || h.store == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	status, err := h.findStatus(ctx, rawURL)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if status.Visibility != visibilityPublic && status.Visibility != visibilityUnlisted {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	account, err := h.store.GetAccountByID(ctx, status.AccountID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	width := oembedDefaultWidth
	if v := parseDimension(web.QueryParam(r, "maxwidth")); v != nil {
		width = *v
	}
	height := parseDimension(web.QueryParam(r, "maxheight"))

	providerName := h.localDomain()
	rootURL := h.rootURL()
	rootURLWithSlash := rootURL + "/"
	authorName := strings.TrimSpace(account.DisplayName)
	if authorName == "" {
		authorName = account.Username
	}

	statusID := strconv.FormatInt(status.ID, 10)
	accountSegment := h.accountPathSegment(account)
	statusURL := h.shortStatusURL(accountSegment, statusID)
	embedURL := h.embedStatusURL(accountSegment, statusID)

	authorAcct := accountSegment

	authorLine := authorAcct
	if !strings.Contains(authorAcct, "@") {
		authorLine = authorAcct + "@" + providerName
	}

	resp := OEmbedResponse{
		Type:         oembedType,
		Version:      oembedVersion,
		AuthorName:   authorName,
		AuthorURL:    h.accountURL(account),
		ProviderName: providerName,
		ProviderURL:  rootURLWithSlash,
		CacheAge:     oembedCacheAge,
		HTML:         h.buildHTML(embedURL, statusURL, authorLine),
		Width:        width,
		Height:       height,
	}

	w.Header().Set("Cache-Control", "private, no-store")
	web.JSON(w, http.StatusOK, resp)
}

// OEmbedResponse is the JSON response for /api/oembed.
type OEmbedResponse struct {
	Type         string `json:"type"`
	Version      string `json:"version"`
	AuthorName   string `json:"author_name"`
	AuthorURL    string `json:"author_url"`
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	CacheAge     int    `json:"cache_age"`
	HTML         string `json:"html"`
	Width        int    `json:"width"`
	Height       *int   `json:"height"`
}

func (h *Handler) findStatus(ctx context.Context, rawURL string) (db.Status, error) {
	id, err := h.parseStatusID(rawURL)
	if err != nil {
		return db.Status{}, err
	}
	status, err := h.store.GetStatusByID(ctx, id)
	if err != nil {
		return db.Status{}, err
	}
	return status, nil
}

func (h *Handler) parseStatusID(rawURL string) (int64, error) {
	if rawURL == "" {
		return 0, pgx.ErrNoRows
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, pgx.ErrNoRows
	}

	if parsed.Host != "" && !h.isAllowedHost(parsed.Host) {
		return 0, pgx.ErrNoRows
	}
	if parsed.Host == "" && !strings.HasPrefix(rawURL, "/") {
		return 0, pgx.ErrNoRows
	}

	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		return 0, pgx.ErrNoRows
	}

	parts := strings.Split(path, "/")
	if parts[len(parts)-1] == "embed" {
		parts = parts[:len(parts)-1]
		if len(parts) == 0 {
			return 0, pgx.ErrNoRows
		}
	}

	var idStr string
	switch {
	case len(parts) == 2 && strings.HasPrefix(parts[0], "@"):
		idStr = parts[1]
	case len(parts) == 4 && parts[0] == "users" && parts[2] == "statuses":
		idStr = parts[3]
	default:
		return 0, pgx.ErrNoRows
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id < 1 {
		return 0, pgx.ErrNoRows
	}
	return id, nil
}

func parseDimension(val string) *int {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	parsed, err := strconv.Atoi(val)
	if err != nil || parsed < 1 {
		return nil
	}
	return &parsed
}

func (h *Handler) localDomain() string {
	if h.cfg != nil && h.cfg.Instance.LocalDomain != "" {
		return h.cfg.Instance.LocalDomain
	}
	return "localhost:3000"
}

func (h *Handler) webDomain() string {
	if h.cfg != nil && h.cfg.Instance.WebDomain != "" {
		return h.cfg.Instance.WebDomain
	}
	return h.localDomain()
}

func (h *Handler) rootURL() string {
	return "https://" + h.webDomain()
}

func (h *Handler) isAllowedHost(host string) bool {
	return h.matchesDomain(host, h.localDomain()) || h.matchesDomain(host, h.webDomain())
}

func (h *Handler) matchesDomain(host, domain string) bool {
	if strings.EqualFold(host, domain) {
		return true
	}
	hostParts := strings.Split(host, ":")
	domainParts := strings.Split(domain, ":")
	return len(hostParts) > 0 && len(domainParts) > 0 && strings.EqualFold(hostParts[0], domainParts[0])
}

func (h *Handler) accountPathSegment(account db.Account) string {
	if account.Domain.Valid && strings.TrimSpace(account.Domain.String) != "" {
		return account.Username + "@" + account.Domain.String
	}
	return account.Username
}

func (h *Handler) shortAccountURL(acct string) string {
	return fmt.Sprintf("%s/@%s", h.rootURL(), acct)
}

func (h *Handler) shortStatusURL(acct, statusID string) string {
	return fmt.Sprintf("%s/%s", h.shortAccountURL(acct), statusID)
}

func (h *Handler) embedStatusURL(acct, statusID string) string {
	return fmt.Sprintf("%s/embed", h.shortStatusURL(acct, statusID))
}

func (h *Handler) accountURL(account db.Account) string {
	if !account.Domain.Valid || strings.TrimSpace(account.Domain.String) == "" {
		return h.shortAccountURL(h.accountPathSegment(account))
	}
	if account.Url.Valid && strings.TrimSpace(account.Url.String) != "" {
		return account.Url.String
	}
	if strings.TrimSpace(account.Uri) != "" {
		return account.Uri
	}
	return h.shortAccountURL(account.Username)
}

func (h *Handler) buildHTML(embedURL, statusURL, authorLine string) string {
	allowedPrefix := h.rootURL() + "/"
	scriptURL := h.rootURL() + "/embed.js"
	return fmt.Sprintf(
		`<blockquote class="mastodon-embed" data-embed-url="%s" style="%s"><a href="%s" target="_blank" style="%s"><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="32" height="32" viewBox="0 0 79 75"><path d="M63 45.3v-20c0-4.1-1-7.3-3.2-9.7-2.1-2.4-5-3.7-8.5-3.7-4.1 0-7.2 1.6-9.3 4.7l-2 3.3-2-3.3c-2-3.1-5.1-4.7-9.2-4.7-3.5 0-6.4 1.3-8.6 3.7-2.1 2.4-3.1 5.6-3.1 9.7v20h8V25.9c0-4.1 1.7-6.2 5.2-6.2 3.8 0 5.8 2.5 5.8 7.4V37.7H44V27.1c0-4.9 1.9-7.4 5.8-7.4 3.5 0 5.2 2.1 5.2 6.2V45.3h8ZM74.7 16.6c.6 6 .1 15.7.1 17.3 0 .5-.1 4.8-.1 5.3-.7 11.5-8 16-15.6 17.5-.1 0-.2 0-.3 0-4.9 1-10 1.2-14.9 1.4-1.2 0-2.4 0-3.6 0-4.8 0-9.7-.6-14.4-1.7-.1 0-.1 0-.1 0s-.1 0-.1 0 0 .1 0 .1 0 0 0 0c.1 1.6.4 3.1 1 4.5.6 1.7 2.9 5.7 11.4 5.7 5 0 9.9-.6 14.8-1.7 0 0 0 0 0 0 .1 0 .1 0 .1 0 0 .1 0 .1 0 .1.1 0 .1 0 .1.1v5.6s0 .1-.1.1c0 0 0 0 0 .1-1.6 1.1-3.7 1.7-5.6 2.3-.8.3-1.6.5-2.4.7-7.5 1.7-15.4 1.3-22.7-1.2-6.8-2.4-13.8-8.2-15.5-15.2-.9-3.8-1.6-7.6-1.9-11.5-.6-5.8-.6-11.7-.8-17.5C3.9 24.5 4 20 4.9 16 6.7 7.9 14.1 2.2 22.3 1c1.4-.2 4.1-1 16.5-1h.1C51.4 0 56.7.8 58.1 1c8.4 1.2 15.5 7.5 16.6 15.6Z" fill="currentColor"/></svg><div style="%s">Post by @%s</div><div style="%s">View on Mastodon</div></a></blockquote><script data-allowed-prefixes="%s" async src="%s"></script>`,
		embedURL,
		oembedBlockquoteStyle,
		statusURL,
		oembedStatusLinkStyle,
		oembedAccountStyle,
		authorLine,
		oembedViewStyle,
		allowedPrefix,
		scriptURL,
	)
}
