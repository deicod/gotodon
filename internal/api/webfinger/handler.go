package webfinger

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	activitypubapi "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

const (
	webFingerContentType         = "application/jrd+json"
	activityPubContentType       = "application/activity+json"
	webFingerProfilePageRel      = "http://webfinger.net/rel/profile-page"
	webFingerSubscribeRel        = "http://ostatus.org/schema/1.0/subscribe"
	webFingerAvatarRel           = "http://webfinger.net/rel/avatar"
	webFingerSuccessCacheControl = "public, max-age=259200" // 3 days
	webFingerErrorCacheControl   = "public, max-age=180"    // 3 minutes
)

var (
	errBadRequest = errors.New("webfinger: bad request")
	errNotFound   = errors.New("webfinger: not found")
)

type store interface {
	GetLocalAccountByUsernameAllowSuspended(ctx context.Context, username string) (db.Account, error)
	HasAccountDeletionRequest(ctx context.Context, accountID int64) (bool, error)
	activitypubapi.InstanceActorStore
}

type Handler struct {
	store store
	cfg   *config.Config
}

func NewHandler(store store, cfg *config.Config) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{store: store, cfg: cfg}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/webfinger", h.Show)
}

func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	addVaryOrigin(w)

	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	if resource == "" {
		h.writeError(w, http.StatusBadRequest)
		return
	}
	if h == nil || h.store == nil {
		h.writeError(w, http.StatusNotFound)
		return
	}

	resolved, err := h.parseResource(resource)
	if err != nil {
		switch {
		case errors.Is(err, errBadRequest):
			h.writeError(w, http.StatusBadRequest)
		case errors.Is(err, errNotFound):
			h.writeError(w, http.StatusNotFound)
		default:
			h.writeError(w, http.StatusBadRequest)
		}
		return
	}

	account, err := h.lookupAccount(r.Context(), resolved)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.writeError(w, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if account.SuspendedAt.Valid {
		hasDeletionRequest, err := h.store.HasAccountDeletionRequest(r.Context(), account.ID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !hasDeletionRequest {
			h.writeError(w, http.StatusGone)
			return
		}
	}

	resp := h.serialize(account, resolved.InstanceActor)
	w.Header().Set("Cache-Control", webFingerSuccessCacheControl)
	w.Header().Set("Content-Type", webFingerContentType)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) lookupAccount(ctx context.Context, resolved resolvedResource) (db.Account, error) {
	if resolved.InstanceActor {
		return activitypubapi.EnsureInstanceActor(ctx, h.store, h.federationDomain())
	}
	return h.store.GetLocalAccountByUsernameAllowSuspended(ctx, resolved.Username)
}

type resolvedResource struct {
	Username      string
	InstanceActor bool
}

func (h *Handler) parseResource(resource string) (resolvedResource, error) {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return resolvedResource{}, errBadRequest
	}

	lower := strings.ToLower(resource)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return h.parseURLResource(resource)
	}

	if strings.Contains(resource, "://") {
		return resolvedResource{}, errBadRequest
	}

	username, domain, ok := parseAcctLike(resource)
	if ok {
		if !h.isAcceptedDomain(domain) {
			return resolvedResource{}, errNotFound
		}
		return h.resolveUsername(username), nil
	}

	if h.isAcceptedDomain(resource) {
		return resolvedResource{Username: h.instanceActorUsername(), InstanceActor: true}, nil
	}

	return resolvedResource{}, errBadRequest
}

func (h *Handler) parseURLResource(resource string) (resolvedResource, error) {
	u, err := url.Parse(resource)
	if err != nil {
		return resolvedResource{}, errBadRequest
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return resolvedResource{}, errBadRequest
	}
	if !h.isAcceptedDomain(host) {
		return resolvedResource{}, errNotFound
	}

	path := strings.TrimSpace(u.EscapedPath())
	if path == "" || path == "/" || path == "/actor" || path == "/actor/" {
		return resolvedResource{Username: h.instanceActorUsername(), InstanceActor: true}, nil
	}

	unescapedPath, err := url.PathUnescape(path)
	if err != nil {
		return resolvedResource{}, errBadRequest
	}

	if strings.HasPrefix(unescapedPath, "/users/") {
		trimmed := strings.TrimPrefix(unescapedPath, "/users/")
		if strings.Contains(trimmed, "/") || strings.TrimSpace(trimmed) == "" {
			return resolvedResource{}, errNotFound
		}
		return h.resolveUsername(trimmed), nil
	}

	if strings.HasPrefix(unescapedPath, "/@") {
		trimmed := strings.TrimPrefix(unescapedPath, "/@")
		trimmed = strings.TrimSuffix(trimmed, "/")
		if strings.Contains(trimmed, "/") || strings.TrimSpace(trimmed) == "" {
			return resolvedResource{}, errNotFound
		}
		return h.resolveUsername(trimmed), nil
	}

	return resolvedResource{}, errNotFound
}

func parseAcctLike(resource string) (username, domain string, ok bool) {
	clean := strings.TrimSpace(resource)
	if idx := strings.Index(clean, ":"); idx > 0 && strings.EqualFold(clean[:idx], "acct") {
		clean = clean[idx+1:]
	}
	clean = strings.TrimPrefix(clean, "@")
	parts := strings.SplitN(clean, "@", 2)
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

func (h *Handler) resolveUsername(username string) resolvedResource {
	username = strings.TrimSpace(username)
	if strings.EqualFold(username, h.federationDomain()) || strings.EqualFold(username, h.publicDomain()) {
		return resolvedResource{Username: h.instanceActorUsername(), InstanceActor: true}
	}
	return resolvedResource{Username: username}
}

type response struct {
	Subject string   `json:"subject"`
	Aliases []string `json:"aliases,omitempty"`
	Links   []link   `json:"links,omitempty"`
}

type link struct {
	Rel      string `json:"rel"`
	Type     string `json:"type,omitempty"`
	Href     string `json:"href,omitempty"`
	Template string `json:"template,omitempty"`
}

func (h *Handler) serialize(account db.Account, instanceActor bool) response {
	federationDomain := h.federationDomain()
	publicDomain := h.publicDomain()
	subject := "acct:" + account.Username + "@" + federationDomain

	profileURL := "https://" + publicDomain + "/@" + account.Username
	selfURL := strings.TrimSpace(account.Uri)
	if selfURL == "" {
		selfURL = "https://" + federationDomain + "/users/" + account.Username
	}

	aliases := []string{profileURL, selfURL}
	if instanceActor {
		profileURL = "https://" + publicDomain + "/about/more?instance_actor=true"
		selfURL = "https://" + federationDomain + "/actor"
		aliases = []string{selfURL}
	}

	links := []link{
		{
			Rel:  webFingerProfilePageRel,
			Type: "text/html",
			Href: profileURL,
		},
		{
			Rel:  "self",
			Type: activityPubContentType,
			Href: selfURL,
		},
		{
			Rel:      webFingerSubscribeRel,
			Template: "https://" + publicDomain + "/authorize_interaction?uri={uri}",
		},
	}

	if avatar := h.avatarLink(account); avatar != nil {
		links = append(links, *avatar)
	}

	return response{
		Subject: subject,
		Aliases: aliases,
		Links:   links,
	}
}

func (h *Handler) avatarLink(account db.Account) *link {
	if !account.AvatarContentType.Valid {
		return nil
	}
	contentType := strings.TrimSpace(account.AvatarContentType.String)
	if contentType == "" {
		return nil
	}

	avatarURL := imageURL(h.publicDomain(), strings.TrimSpace(account.AvatarRemoteUrl.String), "")
	if avatarURL == "" {
		avatarURL = imageURL(h.publicDomain(), strings.TrimSpace(account.AvatarFileName.String), "")
	}
	if avatarURL == "" {
		return nil
	}

	return &link{
		Rel:  webFingerAvatarRel,
		Type: contentType,
		Href: avatarURL,
	}
}

func imageURL(domain, value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return value
		}
		if !strings.HasPrefix(value, "/") {
			value = "/files/" + value
		}
		if domain == "" {
			return value
		}
		return "https://" + domain + value
	}

	if fallback == "" {
		return ""
	}
	if domain == "" {
		return fallback
	}
	return "https://" + domain + fallback
}

func (h *Handler) isAcceptedDomain(domain string) bool {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false
	}
	return strings.EqualFold(domain, h.federationDomain()) || strings.EqualFold(domain, h.publicDomain())
}

func (h *Handler) publicDomain() string {
	if h == nil || h.cfg == nil {
		return "example.com"
	}
	if domain := strings.TrimSpace(h.cfg.Instance.WebDomain); domain != "" {
		return domain
	}
	if domain := strings.TrimSpace(h.cfg.Instance.LocalDomain); domain != "" {
		return domain
	}
	return "example.com"
}

func (h *Handler) federationDomain() string {
	if h == nil || h.cfg == nil {
		return "example.com"
	}
	if domain := strings.TrimSpace(h.cfg.Instance.LocalDomain); domain != "" {
		return domain
	}
	if domain := strings.TrimSpace(h.cfg.Instance.WebDomain); domain != "" {
		return domain
	}
	return "example.com"
}

func (h *Handler) instanceActorUsername() string {
	return h.federationDomain()
}

func (h *Handler) writeError(w http.ResponseWriter, status int) {
	w.Header().Set("Cache-Control", webFingerErrorCacheControl)
	w.WriteHeader(status)
}

func addVaryOrigin(w http.ResponseWriter) {
	headers := w.Header().Values("Vary")
	for _, value := range headers {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Origin") {
				return
			}
		}
	}
	w.Header().Add("Vary", "Origin")
}
