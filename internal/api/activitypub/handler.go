// Package activitypub provides ActivityPub federation HTTP handlers.
package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// FederationHandler handles ActivityPub federation endpoints
type FederationHandler struct {
	queries       *db.Queries
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
func NewFederationHandler(queries *db.Queries, domain string) *FederationHandler {
	return &FederationHandler{
		queries:       queries,
		domain:        domain,
		actorFetcher:  newActorFetcher(queries, domain),
		inboxHandlers: make(map[string]InboxHandler),
	}
}

func (h *FederationHandler) useDB() bool {
	return h.queries != nil
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
	username := web.Param(r, "username")

	// Check Accept header for ActivityPub
	accept := r.Header.Get("Accept")
	if accept != activitypub.ActivityPubContentType &&
		accept != activitypub.ActivityPubLDContentType &&
		accept != "application/activity+json" {
		// Redirect to web profile for non-AP requests
		http.Redirect(w, r, fmt.Sprintf("https://%s/@%s", h.domain, username), http.StatusFound)
		return
	}

	if !h.useDB() {
		h.mockActor(w, username)
		return
	}

	account, err := h.queries.GetAccountByUsername(r.Context(), username)
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

func (h *FederationHandler) mockActor(w http.ResponseWriter, username string) {
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, username)
	discoverable := true

	actor := activitypub.Actor{
		Object: activitypub.Object{
			Context: activitypub.MastodonContextFull(),
			ID:      actorURI,
			Type:    activitypub.TypePerson,
			Name:    username,
			URL:     fmt.Sprintf("https://%s/@%s", h.domain, username),
		},
		PreferredUsername: username,
		Inbox:             fmt.Sprintf("%s/inbox", actorURI),
		Outbox:            fmt.Sprintf("%s/outbox", actorURI),
		Followers:         fmt.Sprintf("%s/followers", actorURI),
		Following:         fmt.Sprintf("%s/following", actorURI),
		Discoverable:      &discoverable,
		Endpoints: &activitypub.Endpoints{
			SharedInbox: fmt.Sprintf("https://%s/inbox", h.domain),
		},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, actor)
}

// GetOutbox handles GET /users/:username/outbox
func (h *FederationHandler) GetOutbox(w http.ResponseWriter, r *http.Request) {
	username := web.Param(r, "username")
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, username)

	// Return an empty ordered collection for now
	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      fmt.Sprintf("%s/outbox", actorURI),
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: 0,
			First:      fmt.Sprintf("%s/outbox?page=true", actorURI),
		},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

// GetFollowers handles GET /users/:username/followers
func (h *FederationHandler) GetFollowers(w http.ResponseWriter, r *http.Request) {
	username := web.Param(r, "username")
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, username)

	totalItems := 0
	if h.useDB() {
		account, err := h.queries.GetAccountByUsername(r.Context(), username)
		if err == nil {
			stats, err := h.queries.GetAccountStats(r.Context(), account.ID)
			if err == nil {
				totalItems = int(stats.FollowersCount)
			}
		}
	}

	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      fmt.Sprintf("%s/followers", actorURI),
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: totalItems,
			First:      fmt.Sprintf("%s/followers?page=true", actorURI),
		},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

// GetFollowing handles GET /users/:username/following
func (h *FederationHandler) GetFollowing(w http.ResponseWriter, r *http.Request) {
	username := web.Param(r, "username")
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, username)

	totalItems := 0
	if h.useDB() {
		account, err := h.queries.GetAccountByUsername(r.Context(), username)
		if err == nil {
			stats, err := h.queries.GetAccountStats(r.Context(), account.ID)
			if err == nil {
				totalItems = int(stats.FollowingCount)
			}
		}
	}

	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      fmt.Sprintf("%s/following", actorURI),
				Type:    activitypub.TypeOrderedCollection,
			},
			TotalItems: totalItems,
			First:      fmt.Sprintf("%s/following?page=true", actorURI),
		},
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
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
	mux.HandleFunc("GET /users/{username}/followers", h.GetFollowers)
	mux.HandleFunc("GET /users/{username}/following", h.GetFollowing)

	// Inbox endpoints
	inboxHandler := middleware.VerifyHTTPSignature(h.actorFetcher)(http.HandlerFunc(h.PostInbox))
	mux.Handle("POST /inbox", inboxHandler)
	mux.Handle("POST /users/{username}/inbox", inboxHandler)
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
