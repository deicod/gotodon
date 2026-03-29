// Package activitypub provides ActivityPub federation HTTP handlers.
package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// FederationHandler handles ActivityPub federation endpoints
type FederationHandler struct {
	store         Store
	domain        string
	actorFetcher  activitypub.ActorFetcher
	inboxHandlers map[string]InboxHandler
}

// InboxHandler processes a specific ActivityPub activity type.
type InboxHandler func(ctx context.Context, activity *activitypub.Activity) error

// InboxError allows handlers to return specific HTTP status codes.
type InboxError struct {
	Status  int
	Message string
}

func (e *InboxError) Error() string {
	return e.Message
}

// NewFederationHandler creates a new federation handler
func NewFederationHandler(store Store, domain string) *FederationHandler {
	return &FederationHandler{
		store:         store,
		domain:        domain,
		actorFetcher:  newActorFetcher(store, domain),
		inboxHandlers: make(map[string]InboxHandler),
	}
}

func (h *FederationHandler) useDB() bool {
	return h.store != nil
}

// ActorFetcher exposes the resolver used for signature verification and inbox handlers.
func (h *FederationHandler) ActorFetcher() activitypub.ActorFetcher {
	return h.actorFetcher
}

// RegisterInboxHandler registers a handler for a specific activity type.
func (h *FederationHandler) RegisterInboxHandler(activityType string, handler InboxHandler) {
	if h.inboxHandlers == nil {
		h.inboxHandlers = make(map[string]InboxHandler)
	}
	h.inboxHandlers[activityType] = handler
}

// GetActor handles GET /users/:username - returns the actor profile
func (h *FederationHandler) GetActor(w http.ResponseWriter, r *http.Request) {
	username := accountParam(r)

	if !isActivityPubRequest(r) {
		if h.useDB() {
			if account, err := h.loadAccountFromRequest(r.Context(), r); err == nil && account.Username != "" {
				http.Redirect(w, r, fmt.Sprintf("https://%s/@%s", h.domain, account.Username), http.StatusFound)
				return
			}
		}
		if username != "" {
			http.Redirect(w, r, fmt.Sprintf("https://%s/@%s", h.domain, username), http.StatusFound)
			return
		}
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Actor unavailable")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}

	h.renderActor(w, account)
}

func (h *FederationHandler) renderActor(w http.ResponseWriter, account db.Account) {
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, account.Username)
	discoverable := account.Discoverable.Valid && account.Discoverable.Bool
	indexable := account.Indexable

	actor := activitypub.Actor{
		Object: activitypub.Object{
			Context: activitypub.MastodonContextFull(),
			ID:      actorURI,
			Type:    activitypub.TypePerson,
			Name:    account.DisplayName,
			Content: account.Note,
			Summary: account.Note,
			URL:     fmt.Sprintf("https://%s/@%s", h.domain, account.Username),
		},
		PreferredUsername:         account.Username,
		Inbox:                     fmt.Sprintf("%s/inbox", actorURI),
		Outbox:                    fmt.Sprintf("%s/outbox", actorURI),
		Followers:                 fmt.Sprintf("%s/followers", actorURI),
		Following:                 fmt.Sprintf("%s/following", actorURI),
		Featured:                  fmt.Sprintf("%s/collections/featured", actorURI),
		FeaturedTags:              fmt.Sprintf("%s/collections/tags", actorURI),
		ManuallyApprovesFollowers: account.Locked,
		Discoverable:              &discoverable,
		Indexable:                 &indexable,
		Endpoints: &activitypub.Endpoints{
			SharedInbox: fmt.Sprintf("https://%s/inbox", h.domain),
		},
	}

	if account.PublicKey != "" {
		actor.PublicKey = &activitypub.PublicKey{
			ID:           fmt.Sprintf("%s#main-key", actorURI),
			Owner:        actorURI,
			PublicKeyPem: account.PublicKey,
		}
	}

	if account.CreatedAt.Valid {
		actor.Published = account.CreatedAt.Time.Format("2006-01-02T15:04:05Z")
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, actor)
}

// GetOutbox handles GET /users/:username/outbox
func (h *FederationHandler) GetOutbox(w http.ResponseWriter, r *http.Request) {
	h.writeOutbox(w, r)
}

// GetFollowers handles GET /users/:username/followers
func (h *FederationHandler) GetFollowers(w http.ResponseWriter, r *http.Request) {
	h.writeFollowers(w, r)
}

// GetFollowing handles GET /users/:username/following
func (h *FederationHandler) GetFollowing(w http.ResponseWriter, r *http.Request) {
	h.writeFollowing(w, r)
}

// GetInstanceActor handles GET /actor
func (h *FederationHandler) GetInstanceActor(w http.ResponseWriter, r *http.Request) {
	if !isActivityPubRequest(r) {
		redirectURL := h.instanceActorURL()
		if redirectURL == "" {
			redirectURL = "/"
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Actor unavailable")
		return
	}

	account, err := h.instanceActorAccount(r.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Actor not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to load actor")
		return
	}

	h.renderInstanceActor(w, account)
}

// GetInstanceOutbox handles GET /actor/outbox
func (h *FederationHandler) GetInstanceOutbox(w http.ResponseWriter, r *http.Request) {
	actorURI := h.instanceActorURI()
	if actorURI == "" {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}

	pageRequested, limit, maxID, minID, sinceID := parseOutboxRequest(r)
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Outbox unavailable")
		return
	}

	account, err := h.instanceActorAccount(r.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Actor not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to load outbox")
		return
	}

	h.writeOutboxForAccount(w, r, account, actorURI, pageRequested, limit, maxID, minID, sinceID)
}

// PostInbox handles POST /users/:username/inbox and POST /inbox
func (h *FederationHandler) PostInbox(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	_ = r.Body.Close()

	if len(body) == 0 {
		web.JSONError(w, http.StatusBadRequest, "Missing activity payload")
		return
	}

	activity, err := parseInboxActivity(body)
	if err != nil {
		h.writeInboxError(w, err)
		return
	}

	handler, exists := h.inboxHandlers[activity.Type]
	if !exists {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if err := handler(r.Context(), activity); err != nil {
		h.writeInboxError(w, err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// RegisterRoutes registers federation routes
func (h *FederationHandler) RegisterRoutes(mux *http.ServeMux) {
	// Actor endpoints
	mux.HandleFunc("GET /users/{username}", h.GetActor)
	mux.HandleFunc("GET /users/{username}/outbox", h.GetOutbox)
	mux.HandleFunc("GET /users/{username}/statuses/{statusID}", h.GetStatus)
	mux.HandleFunc("GET /users/{username}/statuses/{statusID}/activity", h.GetStatusActivity)
	mux.HandleFunc("GET /users/{username}/statuses/{statusID}/replies", h.GetStatusReplies)
	mux.HandleFunc("GET /users/{username}/statuses/{statusID}/likes", h.GetStatusLikes)
	mux.HandleFunc("GET /users/{username}/statuses/{statusID}/shares", h.GetStatusShares)
	mux.HandleFunc("GET /users/{username}/followers", h.GetFollowers)
	mux.HandleFunc("GET /users/{username}/following", h.GetFollowing)
	mux.HandleFunc("GET /users/{username}/followers_synchronization", h.GetFollowersSynchronization)
	mux.HandleFunc("GET /users/{username}/quote_authorizations/{id}", h.GetQuoteAuthorization)
	mux.HandleFunc("GET /users/{username}/collections/featured", h.GetFeatured)
	mux.HandleFunc("GET /users/{username}/collections/tags", h.GetFeaturedTags)
	mux.HandleFunc("GET /actor", h.GetInstanceActor)
	mux.HandleFunc("GET /actor/outbox", h.GetInstanceOutbox)
	mux.HandleFunc("GET /contexts/{id}", h.GetContext)
	mux.HandleFunc("GET /contexts/{id}/items", h.GetContextItems)

	// Inbox endpoints
	inboxHandler := middleware.VerifyHTTPSignature(h.actorFetcher)(http.HandlerFunc(h.PostInbox))
	mux.Handle("POST /inbox", inboxHandler)
	mux.Handle("POST /users/{username}/inbox", inboxHandler)
	mux.Handle("POST /actor/inbox", inboxHandler)

	// ActivityPub alias routes
	mux.HandleFunc("GET /ap/users/{id}", h.GetActor)
	mux.HandleFunc("GET /ap/users/{id}/outbox", h.GetOutbox)
	mux.HandleFunc("GET /ap/users/{id}/statuses/{statusID}", h.GetStatus)
	mux.HandleFunc("GET /ap/users/{id}/statuses/{statusID}/activity", h.GetStatusActivity)
	mux.HandleFunc("GET /ap/users/{id}/statuses/{statusID}/replies", h.GetStatusReplies)
	mux.HandleFunc("GET /ap/users/{id}/statuses/{statusID}/likes", h.GetStatusLikes)
	mux.HandleFunc("GET /ap/users/{id}/statuses/{statusID}/shares", h.GetStatusShares)
	mux.HandleFunc("GET /ap/users/{id}/followers", h.GetFollowers)
	mux.HandleFunc("GET /ap/users/{id}/following", h.GetFollowing)
	mux.HandleFunc("GET /ap/users/{id}/followers_synchronization", h.GetFollowersSynchronization)
	mux.HandleFunc("GET /ap/users/{id}/quote_authorizations/{quoteID}", h.GetQuoteAuthorization)
	mux.HandleFunc("GET /ap/users/{id}/collections/featured", h.GetFeatured)
	mux.HandleFunc("GET /ap/users/{id}/collections/tags", h.GetFeaturedTags)
	mux.Handle("POST /ap/inbox", inboxHandler)
	mux.Handle("POST /ap/users/{username}/inbox", inboxHandler)
}

func (h *FederationHandler) loadAccountFromRequest(ctx context.Context, r *http.Request) (db.Account, error) {
	if h.store == nil {
		return db.Account{}, pgx.ErrNoRows
	}
	if username := web.Param(r, "username"); username != "" {
		return h.store.GetAccountByUsername(ctx, username)
	}
	if idParam := web.Param(r, "id"); idParam != "" {
		id, err := strconv.ParseInt(idParam, 10, 64)
		if err == nil && id > 0 {
			return h.store.GetAccountByID(ctx, id)
		}
		return h.store.GetAccountByUsername(ctx, idParam)
	}
	return db.Account{}, pgx.ErrNoRows
}

func accountParam(r *http.Request) string {
	if username := web.Param(r, "username"); username != "" {
		return username
	}
	return web.Param(r, "id")
}

func (h *FederationHandler) writeInboxError(w http.ResponseWriter, err error) {
	var inboxErr *InboxError
	if errors.As(err, &inboxErr) && inboxErr != nil {
		web.JSONError(w, inboxErr.Status, inboxErr.Message)
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Failed to process activity")
}

func parseInboxActivity(body []byte) (*activitypub.Activity, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, &InboxError{Status: http.StatusBadRequest, Message: "Invalid activity payload"}
	}

	activityType, err := extractActivityType(raw)
	if err != nil {
		return nil, err
	}

	raw["type"] = activityType
	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, &InboxError{Status: http.StatusBadRequest, Message: "Invalid activity payload"}
	}

	var activity activitypub.Activity
	if err := json.Unmarshal(normalized, &activity); err != nil {
		return nil, &InboxError{Status: http.StatusBadRequest, Message: "Invalid activity payload"}
	}
	activity.Type = activityType

	if extractActorURI(&activity) == "" {
		return nil, &InboxError{Status: http.StatusBadRequest, Message: "Missing activity actor"}
	}

	return &activity, nil
}

func isActivityPubRequest(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == activitypub.ActivityPubContentType ||
		accept == activitypub.ActivityPubLDContentType ||
		accept == "application/activity+json"
}

func (h *FederationHandler) instanceActorAccount(ctx context.Context) (db.Account, error) {
	if h == nil || h.store == nil {
		return db.Account{}, pgx.ErrNoRows
	}
	return EnsureInstanceActor(ctx, asInstanceActorStore(h.store), h.domain)
}

func (h *FederationHandler) renderInstanceActor(w http.ResponseWriter, account db.Account) {
	actorURI := h.instanceActorURI()
	if actorURI == "" {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}

	preferredUsername := account.Username
	if preferredUsername == "" {
		preferredUsername = h.domain
	}

	actorType := activitypub.TypeApplication
	if account.ActorType.Valid && strings.TrimSpace(account.ActorType.String) != "" {
		actorType = account.ActorType.String
	}

	actor := activitypub.Actor{
		Object: activitypub.Object{
			Context: activitypub.MastodonContextFull(),
			ID:      actorURI,
			Type:    actorType,
			URL:     h.instanceActorURL(),
		},
		PreferredUsername:         preferredUsername,
		Inbox:                     actorURI + "/inbox",
		Outbox:                    actorURI + "/outbox",
		ManuallyApprovesFollowers: true,
		Endpoints: &activitypub.Endpoints{
			SharedInbox: fmt.Sprintf("https://%s/inbox", h.domain),
		},
	}

	if account.PublicKey != "" {
		actor.PublicKey = &activitypub.PublicKey{
			ID:           fmt.Sprintf("%s#main-key", actorURI),
			Owner:        actorURI,
			PublicKeyPem: account.PublicKey,
		}
	}
	if account.CreatedAt.Valid {
		actor.Published = account.CreatedAt.Time.Format("2006-01-02T15:04:05Z")
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, actor)
}

func (h *FederationHandler) instanceActorURI() string {
	return instanceActorURIForDomain(h.domain)
}

func (h *FederationHandler) instanceActorURL() string {
	return instanceActorURLForDomain(h.domain)
}

func asInstanceActorStore(store Store) InstanceActorStore {
	if store == nil {
		return nil
	}
	typed, ok := any(store).(InstanceActorStore)
	if !ok {
		return nil
	}
	return typed
}

func extractActivityType(raw map[string]any) (string, error) {
	val, ok := raw["type"]
	if !ok {
		return "", &InboxError{Status: http.StatusBadRequest, Message: "Missing activity type"}
	}

	switch t := val.(type) {
	case string:
		if t == "" {
			return "", &InboxError{Status: http.StatusBadRequest, Message: "Missing activity type"}
		}
		return t, nil
	case []any:
		var fallback string
		for _, item := range t {
			s, ok := item.(string)
			if !ok || s == "" {
				continue
			}
			if fallback == "" {
				fallback = s
			}
			if !strings.EqualFold(s, "Activity") {
				return s, nil
			}
		}
		if fallback != "" {
			return fallback, nil
		}
	case []string:
		var fallback string
		for _, s := range t {
			if s == "" {
				continue
			}
			if fallback == "" {
				fallback = s
			}
			if !strings.EqualFold(s, "Activity") {
				return s, nil
			}
		}
		if fallback != "" {
			return fallback, nil
		}
	}

	return "", &InboxError{Status: http.StatusBadRequest, Message: "Invalid activity type"}
}

func extractActorURI(activity *activitypub.Activity) string {
	switch actor := activity.Actor.(type) {
	case string:
		return actor
	case map[string]any:
		if id, ok := actor["id"].(string); ok {
			return id
		}
	}
	return ""
}

// Helper to format account ID
