// Package activitypub provides ActivityPub federation HTTP handlers.
package activitypub

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// FederationHandler handles ActivityPub federation endpoints
type FederationHandler struct {
	queries *db.Queries
	domain  string
}

// NewFederationHandler creates a new federation handler
func NewFederationHandler(queries *db.Queries, domain string) *FederationHandler {
	return &FederationHandler{
		queries: queries,
		domain:  domain,
	}
}

func (h *FederationHandler) useDB() bool {
	return h.queries != nil
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
		actor.Object.Published = account.CreatedAt.Time.Format("2006-01-02T15:04:05Z")
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
	// For now, accept the activity and return 202 Accepted
	// Full implementation would verify signatures and process activities
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
	mux.HandleFunc("POST /inbox", h.PostInbox)
	mux.HandleFunc("POST /users/{username}/inbox", h.PostInbox)
}

// Helper to format account ID
func formatAccountID(id int64) string {
	return strconv.FormatInt(id, 10)
}
