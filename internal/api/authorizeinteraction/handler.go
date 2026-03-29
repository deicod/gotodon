package authorizeinteraction

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

const (
	visibilityPublic   = 0
	visibilityUnlisted = 1
)

type store interface {
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	GetStatusByURI(ctx context.Context, uri pgtype.Text) (db.Status, error)
	GetStatusByURL(ctx context.Context, url pgtype.Text) (db.Status, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error)
}

type accountResolver interface {
	ResolveAccount(ctx context.Context, acctOrActorURI string) (db.Account, error)
}

type statusFetcher interface {
	FetchStatus(ctx context.Context, uri string) (db.Status, error)
}

// Handler handles GET /authorize_interaction compatibility redirects.
type Handler struct {
	store    store
	cfg      *config.Config
	resolver accountResolver
	fetcher  statusFetcher
}

func NewHandler(store store, cfg *config.Config, resolver accountResolver, fetcher statusFetcher) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{store: store, cfg: cfg, resolver: resolver, fetcher: fetcher}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /authorize_interaction", h.Show)
}

func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	resource := strings.TrimSpace(r.URL.Query().Get("uri"))
	if resource == "" {
		resource = stripAcctPrefix(strings.TrimSpace(r.URL.Query().Get("acct")))
	}
	resource = h.normalizeAcctResource(resource)
	if resource == "" || h == nil || h.store == nil {
		http.NotFound(w, r)
		return
	}

	target, ok := h.resolveTarget(r.Context(), resource)
	if !ok || strings.TrimSpace(target) == "" {
		http.NotFound(w, r)
		return
	}

	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Handler) resolveTarget(ctx context.Context, resource string) (string, bool) {
	if target, ok := h.resolveLocalShortURL(ctx, resource); ok {
		return target, true
	}

	if target, ok := h.resolveStatusByURI(ctx, resource, true); ok {
		return target, true
	}

	if target, ok := h.resolveAccountByResource(ctx, resource); ok {
		return target, true
	}

	return "", false
}

func (h *Handler) resolveLocalShortURL(ctx context.Context, raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", false
	}
	if u.Host != "" && !h.isAllowedHost(u.Host) {
		return "", false
	}

	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", false
	}
	parts := strings.Split(path, "/")

	if len(parts) == 1 && strings.HasPrefix(parts[0], "@") {
		username := strings.TrimPrefix(parts[0], "@")
		if username == "" {
			return "", false
		}
		account, err := h.store.GetAccountByUsername(ctx, username)
		if err != nil {
			return "", false
		}
		return h.accountTarget(account)
	}

	if len(parts) == 2 && strings.HasPrefix(parts[0], "@") {
		username := strings.TrimPrefix(parts[0], "@")
		statusID, err := strconv.ParseInt(parts[1], 10, 64)
		if username == "" || err != nil || statusID <= 0 {
			return "", false
		}
		status, err := h.store.GetStatusByID(ctx, statusID)
		if err != nil {
			return "", false
		}
		author, err := h.store.GetAccountByID(ctx, status.AccountID)
		if err != nil || !strings.EqualFold(author.Username, username) || !isLocalAccount(author) {
			return "", false
		}
		return h.statusTarget(ctx, status)
	}

	return "", false
}

func (h *Handler) resolveStatusByURI(ctx context.Context, uri string, allowRemoteFetch bool) (string, bool) {
	status, err := h.store.GetStatusByURI(ctx, pgtype.Text{String: uri, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		status, err = h.store.GetStatusByURL(ctx, pgtype.Text{String: uri, Valid: true})
	}
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", false
		}
		if !allowRemoteFetch || h.fetcher == nil || h.isAllowedHostFromURL(uri) || !h.canResolveRemote(ctx) {
			return "", false
		}
		status, err = h.fetcher.FetchStatus(ctx, uri)
		if err != nil {
			return "", false
		}
	}
	return h.statusTarget(ctx, status)
}

func (h *Handler) resolveAccountByResource(ctx context.Context, resource string) (string, bool) {
	if account, err := h.store.GetAccountByURI(ctx, resource); err == nil {
		return h.accountTarget(account)
	}
	if looksLikeAcct(resource) {
		if username, domain, ok := splitAcct(resource); ok {
			account, err := h.getCachedAccountByAcct(ctx, username, domain)
			if err == nil {
				return h.accountTarget(account)
			}
		}
	}
	if h.resolver != nil && h.canResolveRemote(ctx) {
		account, err := h.resolver.ResolveAccount(ctx, resource)
		if err == nil {
			return h.accountTarget(account)
		}
	}

	acct, ok := acctFromProfileURL(resource)
	if !ok {
		return "", false
	}
	if username, domain, ok := splitAcct(acct); ok {
		account, err := h.getCachedAccountByAcct(ctx, username, domain)
		if err == nil {
			return h.accountTarget(account)
		}
	}
	if h.resolver == nil || !h.canResolveRemote(ctx) {
		return "", false
	}
	account, err := h.resolver.ResolveAccount(ctx, acct)
	if err == nil {
		return h.accountTarget(account)
	}
	return "", false
}

func (h *Handler) getCachedAccountByAcct(ctx context.Context, username, domain string) (db.Account, error) {
	if matchesDomain(domain, h.localDomain()) || matchesDomain(domain, h.webDomain()) {
		return h.store.GetAccountByUsername(ctx, username)
	}
	return h.store.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
		Username: username,
		Domain:   domain,
	})
}

func (h *Handler) canResolveRemote(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if _, ok := middleware.GetAccountID(ctx); ok {
		return true
	}
	_, ok := middleware.GetUserID(ctx)
	return ok
}

func (h *Handler) accountTarget(account db.Account) (string, bool) {
	if isLocalAccount(account) {
		if strings.TrimSpace(account.Username) == "" {
			return "", false
		}
		return h.shortAccountURL(account.Username), true
	}
	if account.Url.Valid && strings.TrimSpace(account.Url.String) != "" {
		return strings.TrimSpace(account.Url.String), true
	}
	if strings.TrimSpace(account.Uri) != "" {
		return strings.TrimSpace(account.Uri), true
	}
	return "", false
}

func (h *Handler) statusTarget(ctx context.Context, status db.Status) (string, bool) {
	if status.Visibility != visibilityPublic && status.Visibility != visibilityUnlisted {
		return "", false
	}
	if status.Local.Valid && status.Local.Bool {
		author, err := h.store.GetAccountByID(ctx, status.AccountID)
		if err != nil || strings.TrimSpace(author.Username) == "" {
			return "", false
		}
		return h.shortStatusURL(author.Username, status.ID), true
	}
	if status.Url.Valid && strings.TrimSpace(status.Url.String) != "" {
		return strings.TrimSpace(status.Url.String), true
	}
	if status.Uri.Valid && strings.TrimSpace(status.Uri.String) != "" {
		return strings.TrimSpace(status.Uri.String), true
	}
	return "", false
}

func stripAcctPrefix(input string) string {
	if strings.HasPrefix(strings.ToLower(input), "acct:") {
		return strings.TrimSpace(input[5:])
	}
	return input
}

func (h *Handler) normalizeAcctResource(resource string) string {
	resource = strings.TrimSpace(resource)
	if !looksLikeAcct(resource) {
		return resource
	}

	username, domain, ok := splitAcct(resource)
	if !ok {
		return resource
	}
	if !matchesDomain(domain, h.webDomain()) || matchesDomain(domain, h.localDomain()) {
		return resource
	}

	localDomain := h.localDomain()
	if localDomain == "" {
		return resource
	}

	return username + "@" + localDomain
}

func looksLikeAcct(input string) bool {
	input = stripAcctPrefix(strings.TrimSpace(input))
	if input == "" {
		return false
	}
	if strings.Contains(input, "://") || strings.Contains(input, "/") {
		return false
	}
	return strings.Contains(input, "@")
}

func splitAcct(input string) (username string, domain string, ok bool) {
	parts := strings.Split(strings.TrimSpace(input), "@")
	if len(parts) != 2 {
		return "", "", false
	}
	username = strings.TrimSpace(parts[0])
	domain = strings.TrimSpace(parts[1])
	if username == "" || domain == "" {
		return "", "", false
	}
	return username, domain, true
}

func acctFromProfileURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return "", false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", false
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 1 || !strings.HasPrefix(parts[0], "@") {
		return "", false
	}
	username := strings.TrimPrefix(parts[0], "@")
	if username == "" {
		return "", false
	}
	return username + "@" + host, true
}

func (h *Handler) isAllowedHostFromURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	return h.isAllowedHost(u.Host)
}

func (h *Handler) isAllowedHost(host string) bool {
	return matchesDomain(host, h.localDomain()) || matchesDomain(host, h.webDomain())
}

func (h *Handler) localDomain() string {
	if h.cfg == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.Instance.LocalDomain)
}

func (h *Handler) webDomain() string {
	if h.cfg == nil {
		return ""
	}
	return strings.TrimSpace(h.cfg.Instance.WebDomain)
}

func (h *Handler) publicDomain() string {
	if d := h.webDomain(); d != "" {
		return d
	}
	if d := h.localDomain(); d != "" {
		return d
	}
	return "example.com"
}

func (h *Handler) rootURL() string {
	return "https://" + h.publicDomain()
}

func (h *Handler) shortAccountURL(username string) string {
	return fmt.Sprintf("%s/@%s", h.rootURL(), username)
}

func (h *Handler) shortStatusURL(username string, statusID int64) string {
	return fmt.Sprintf("%s/@%s/%d", h.rootURL(), username, statusID)
}

func isLocalAccount(account db.Account) bool {
	return !account.Domain.Valid || strings.TrimSpace(account.Domain.String) == ""
}

func matchesDomain(host, domain string) bool {
	if domain == "" {
		return false
	}
	if strings.EqualFold(host, domain) {
		return true
	}
	hostParts := strings.Split(host, ":")
	domainParts := strings.Split(domain, ":")
	return len(hostParts) > 0 && len(domainParts) > 0 && strings.EqualFold(hostParts[0], domainParts[0])
}
