package compat

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

const (
	visibilityPublic   = 0
	visibilityUnlisted = 1
)

type store interface {
	GetLocalAccountByUsernameAllowSuspended(ctx context.Context, username string) (db.Account, error)
	HasAccountDeletionRequest(ctx context.Context, accountID int64) (bool, error)
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
}

type activityPubRenderer interface {
	GetStatus(w http.ResponseWriter, r *http.Request)
	GetActor(w http.ResponseWriter, r *http.Request)
}

// Handler renders compatibility short account/status pages:
//   - GET /@username
//   - GET /@username/{statusID}
type Handler struct {
	store store
	cfg   *config.Config
	ap    activityPubRenderer
}

func NewHandler(store store, cfg *config.Config, ap activityPubRenderer) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{
		store: store,
		cfg:   cfg,
		ap:    ap,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{segment}", h.ShowAccount)
	mux.HandleFunc("GET /{segment}/{status}", h.ShowStatus)
}

func (h *Handler) ShowAccount(w http.ResponseWriter, r *http.Request) {
	segment := r.PathValue("segment")
	if !strings.HasPrefix(segment, "@") {
		http.NotFound(w, r)
		return
	}

	username := strings.TrimPrefix(segment, "@")
	jsonRequested := strings.HasSuffix(username, ".json")
	if jsonRequested {
		username = strings.TrimSuffix(username, ".json")
	}
	acceptNegotiated := !jsonRequested
	if acceptNegotiated {
		setVaryAccept(w.Header())
	}
	if strings.TrimSpace(username) == "" {
		http.NotFound(w, r)
		return
	}

	if jsonRequested || wantsActivityPub(r) {
		if h.ap == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cloned := r.Clone(r.Context())
		cloned.SetPathValue("username", username)
		cloned.Header.Set("Accept", "application/activity+json")
		h.ap.GetActor(w, cloned)
		return
	}

	account, err := h.loadLocalAccount(r.Context(), username)
	if err != nil {
		switch {
		case statusErrorCode(err) != 0:
			w.WriteHeader(statusErrorCode(err))
		case errors.Is(err, pgx.ErrNoRows):
			http.NotFound(w, r)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	pageURL := h.shortAccountURL(account.Username)
	ogImage := h.accountImageURL(account)
	title := h.accountTitle(account)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(
		`<!doctype html><html lang="en"><head>` +
			`<meta charset="utf-8">` +
			`<meta name="viewport" content="width=device-width, initial-scale=1">` +
			`<title>` + html.EscapeString(title) + `</title>` +
			h.commonHeadLinks(pageURL) +
			`<meta property="og:title" content="` + html.EscapeString(title) + `">` +
			`<meta property="og:type" content="profile">` +
			`<meta property="og:image" content="` + html.EscapeString(ogImage) + `">` +
			`<meta property="og:url" content="` + html.EscapeString(pageURL) + `">` +
			`</head><body><main>` +
			`<h1>` + html.EscapeString(displayName(account)) + `</h1>` +
			`<p>@` + html.EscapeString(account.Username) + `</p>` +
			`</main></body></html>`,
	))
}

func (h *Handler) ShowStatus(w http.ResponseWriter, r *http.Request) {
	segment := r.PathValue("segment")
	if !strings.HasPrefix(segment, "@") {
		http.NotFound(w, r)
		return
	}
	username := strings.TrimPrefix(segment, "@")
	if strings.TrimSpace(username) == "" {
		http.NotFound(w, r)
		return
	}

	statusPart := strings.TrimSpace(r.PathValue("status"))
	if statusPart == "" {
		http.NotFound(w, r)
		return
	}
	jsonRequested := strings.HasSuffix(statusPart, ".json")
	if jsonRequested {
		statusPart = strings.TrimSuffix(statusPart, ".json")
	}
	acceptNegotiated := !jsonRequested
	if acceptNegotiated {
		setVaryAccept(w.Header())
	}

	statusID, err := strconv.ParseInt(statusPart, 10, 64)
	if err != nil || statusID <= 0 {
		http.NotFound(w, r)
		return
	}

	if jsonRequested || wantsActivityPub(r) {
		if h.ap == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cloned := r.Clone(r.Context())
		cloned.SetPathValue("username", username)
		cloned.SetPathValue("statusID", strconv.FormatInt(statusID, 10))
		cloned.Header.Set("Accept", "application/activity+json")
		h.ap.GetStatus(w, cloned)
		return
	}

	account, err := h.loadLocalAccount(r.Context(), username)
	if err != nil {
		switch {
		case statusErrorCode(err) != 0:
			w.WriteHeader(statusErrorCode(err))
		case errors.Is(err, pgx.ErrNoRows):
			http.NotFound(w, r)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	status, err := h.store.GetStatusByID(r.Context(), statusID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if status.AccountID != account.ID {
		http.NotFound(w, r)
		return
	}

	if status.Visibility != visibilityPublic && status.Visibility != visibilityUnlisted {
		http.NotFound(w, r)
		return
	}

	if status.ReblogOfID.Valid {
		target, ok := h.reblogTargetURL(r.Context(), account, status)
		if !ok || strings.TrimSpace(target) == "" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	statusText := strings.TrimSpace(status.Text)
	if spoiler := strings.TrimSpace(status.SpoilerText); spoiler != "" {
		statusText = spoiler
	}
	pageURL := h.shortStatusURL(account.Username, status.ID)
	if statusText == "" {
		statusText = fmt.Sprintf("Post by @%s", account.Username)
	}
	title := fmt.Sprintf("%s: %s", displayName(account), trimmed(statusText, 80))
	description := trimmed(statusText, 200)

	published := ""
	if status.CreatedAt.Valid {
		published = status.CreatedAt.Time.UTC().Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	body := `<!doctype html><html lang="en"><head>` +
		`<meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title>` +
		h.commonHeadLinks(pageURL) +
		`<meta property="og:title" content="` + html.EscapeString(title) + `">` +
		`<meta property="og:type" content="article">` +
		`<meta property="og:url" content="` + html.EscapeString(pageURL) + `">`
	if published != "" {
		body += `<meta property="og:published_time" content="` + html.EscapeString(published) + `">`
	}
	if description != "" {
		body += `<meta property="og:description" content="` + html.EscapeString(description) + `">`
	}
	body += `</head><body><main><h1>` + html.EscapeString(displayName(account)) + `</h1>` +
		`<article><p>` + html.EscapeString(statusText) + `</p></article>` +
		`</main></body></html>`
	_, _ = w.Write([]byte(body))
}

func (h *Handler) loadLocalAccount(ctx context.Context, username string) (db.Account, error) {
	account, err := h.store.GetLocalAccountByUsernameAllowSuspended(ctx, username)
	if err != nil {
		return db.Account{}, err
	}
	if account.SuspendedAt.Valid {
		hasDeletionRequest, err := h.store.HasAccountDeletionRequest(ctx, account.ID)
		if err != nil {
			return db.Account{}, err
		}
		if hasDeletionRequest {
			return db.Account{}, &accountVisibilityError{status: http.StatusForbidden}
		}
		return db.Account{}, &accountVisibilityError{status: http.StatusGone}
	}
	return account, nil
}

func wantsActivityPub(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "application/activity+json") ||
		strings.Contains(accept, "application/ld+json") ||
		strings.Contains(accept, "application/activity")
}

func (h *Handler) reblogTargetURL(ctx context.Context, account db.Account, status db.Status) (string, bool) {
	if !status.ReblogOfID.Valid {
		return "", false
	}
	original, err := h.store.GetStatusByID(ctx, status.ReblogOfID.Int64)
	if err != nil {
		return "", false
	}
	if original.Url.Valid && strings.TrimSpace(original.Url.String) != "" {
		return original.Url.String, true
	}
	if original.Uri.Valid && strings.TrimSpace(original.Uri.String) != "" {
		return original.Uri.String, true
	}
	author, err := h.store.GetAccountByID(ctx, original.AccountID)
	if err != nil {
		return "", false
	}
	if !author.Domain.Valid || strings.TrimSpace(author.Domain.String) == "" {
		target := h.shortStatusURL(author.Username, original.ID)
		if target == h.shortStatusURL(account.Username, status.ID) {
			return "", false
		}
		return target, true
	}
	return "", false
}

func (h *Handler) commonHeadLinks(canonicalURL string) string {
	icon := h.rootURL() + "/icons/android-chrome-192x192.png"
	return `<link rel="icon" href="` + html.EscapeString(icon) + `">` +
		`<link rel="apple-touch-icon" href="` + html.EscapeString(icon) + `">` +
		`<link rel="canonical" href="` + html.EscapeString(canonicalURL) + `">`
}

func (h *Handler) accountTitle(account db.Account) string {
	return fmt.Sprintf("%s (@%s)", displayName(account), account.Username)
}

func (h *Handler) accountImageURL(account db.Account) string {
	if account.AvatarRemoteUrl.Valid && strings.TrimSpace(account.AvatarRemoteUrl.String) != "" {
		return h.imageURL(account.AvatarRemoteUrl.String)
	}
	if account.AvatarFileName.Valid && strings.TrimSpace(account.AvatarFileName.String) != "" {
		return h.imageURL(account.AvatarFileName.String)
	}
	return h.rootURL() + "/icons/android-chrome-192x192.png"
}

func (h *Handler) imageURL(value string) string {
	trimmedVal := strings.TrimSpace(value)
	if trimmedVal == "" {
		return ""
	}
	if strings.HasPrefix(trimmedVal, "http://") || strings.HasPrefix(trimmedVal, "https://") {
		return trimmedVal
	}
	if !strings.HasPrefix(trimmedVal, "/") {
		trimmedVal = "/files/" + trimmedVal
	}
	return h.rootURL() + trimmedVal
}

func (h *Handler) rootURL() string {
	return "https://" + h.publicDomain()
}

func (h *Handler) shortAccountURL(username string) string {
	return fmt.Sprintf("%s/@%s", h.rootURL(), username)
}

func (h *Handler) shortStatusURL(username string, statusID int64) string {
	return fmt.Sprintf("%s/%d", h.shortAccountURL(username), statusID)
}

func (h *Handler) publicDomain() string {
	if h.cfg == nil {
		return "example.com"
	}
	if d := strings.TrimSpace(h.cfg.Instance.WebDomain); d != "" {
		return d
	}
	if d := strings.TrimSpace(h.cfg.Instance.LocalDomain); d != "" {
		return d
	}
	return "example.com"
}

func (h *Handler) federationDomain() string {
	if h.cfg == nil {
		return "example.com"
	}
	if d := strings.TrimSpace(h.cfg.Instance.LocalDomain); d != "" {
		return d
	}
	if d := strings.TrimSpace(h.cfg.Instance.WebDomain); d != "" {
		return d
	}
	return "example.com"
}

func displayName(account db.Account) string {
	if d := strings.TrimSpace(account.DisplayName); d != "" {
		return d
	}
	return account.Username
}

func trimmed(input string, max int) string {
	if max <= 0 {
		return ""
	}
	in := strings.TrimSpace(input)
	if len(in) <= max {
		return in
	}
	return strings.TrimSpace(in[:max]) + "…"
}

type accountVisibilityError struct {
	status int
}

func (e *accountVisibilityError) Error() string {
	return http.StatusText(e.status)
}

func (e *accountVisibilityError) Status() int {
	return e.status
}

func statusErrorCode(err error) int {
	type statusErr interface {
		Status() int
	}
	var serr statusErr
	if errors.As(err, &serr) {
		return serr.Status()
	}
	return 0
}

func setVaryAccept(h http.Header) {
	if h == nil {
		return
	}
	values := h.Values("Vary")
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Accept") {
				return
			}
		}
	}
	h.Add("Vary", "Accept")
}
