// Package activitypub provides ActivityPub federation HTTP handlers.
package activitypub

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/db"
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
func (h *FederationHandler) GetActor(c echo.Context) error {
	username := c.Param("username")

	// Check Accept header for ActivityPub
	accept := c.Request().Header.Get("Accept")
	if accept != activitypub.ActivityPubContentType &&
		accept != activitypub.ActivityPubLDContentType &&
		accept != "application/activity+json" {
		// Redirect to web profile for non-AP requests
		return c.Redirect(http.StatusFound, fmt.Sprintf("https://%s/@%s", h.domain, username))
	}

	if !h.useDB() {
		return h.mockActor(c, username)
	}

	account, err := h.queries.GetAccountByUsername(c.Request().Context(), username)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Actor not found")
	}

	return h.renderActor(c, account)
}

func (h *FederationHandler) renderActor(c echo.Context, account db.Account) error {
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

	c.Response().Header().Set("Content-Type", activitypub.ActivityPubContentType)
	return c.JSON(http.StatusOK, actor)
}

func (h *FederationHandler) mockActor(c echo.Context, username string) error {
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

	c.Response().Header().Set("Content-Type", activitypub.ActivityPubContentType)
	return c.JSON(http.StatusOK, actor)
}

// GetOutbox handles GET /users/:username/outbox
func (h *FederationHandler) GetOutbox(c echo.Context) error {
	username := c.Param("username")
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

	c.Response().Header().Set("Content-Type", activitypub.ActivityPubContentType)
	return c.JSON(http.StatusOK, collection)
}

// GetFollowers handles GET /users/:username/followers
func (h *FederationHandler) GetFollowers(c echo.Context) error {
	username := c.Param("username")
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, username)

	totalItems := 0
	if h.useDB() {
		account, err := h.queries.GetAccountByUsername(c.Request().Context(), username)
		if err == nil {
			stats, err := h.queries.GetAccountStats(c.Request().Context(), account.ID)
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

	c.Response().Header().Set("Content-Type", activitypub.ActivityPubContentType)
	return c.JSON(http.StatusOK, collection)
}

// GetFollowing handles GET /users/:username/following
func (h *FederationHandler) GetFollowing(c echo.Context) error {
	username := c.Param("username")
	actorURI := fmt.Sprintf("https://%s/users/%s", h.domain, username)

	totalItems := 0
	if h.useDB() {
		account, err := h.queries.GetAccountByUsername(c.Request().Context(), username)
		if err == nil {
			stats, err := h.queries.GetAccountStats(c.Request().Context(), account.ID)
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

	c.Response().Header().Set("Content-Type", activitypub.ActivityPubContentType)
	return c.JSON(http.StatusOK, collection)
}

// PostInbox handles POST /users/:username/inbox and POST /inbox
func (h *FederationHandler) PostInbox(c echo.Context) error {
	// For now, accept the activity and return 202 Accepted
	// Full implementation would verify signatures and process activities
	return c.NoContent(http.StatusAccepted)
}

// RegisterRoutes registers federation routes
func (h *FederationHandler) RegisterRoutes(e *echo.Echo) {
	// Actor endpoints
	e.GET("/users/:username", h.GetActor)
	e.GET("/users/:username/outbox", h.GetOutbox)
	e.GET("/users/:username/followers", h.GetFollowers)
	e.GET("/users/:username/following", h.GetFollowing)

	// Inbox endpoints
	e.POST("/inbox", h.PostInbox)
	e.POST("/users/:username/inbox", h.PostInbox)
}

// Helper to format account ID
func formatAccountID(id int64) string {
	return strconv.FormatInt(id, 10)
}
