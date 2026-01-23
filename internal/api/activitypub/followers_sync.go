package activitypub

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// GetFollowersSynchronization handles GET /users/:username/followers_synchronization
func (h *FederationHandler) GetFollowersSynchronization(w http.ResponseWriter, r *http.Request) {
	if !h.useDB() {
		web.JSONError(w, http.StatusNotFound, "Actor not found")
		return
	}

	actorURI, ok := h.verifySignedRequest(w, r)
	if !ok {
		return
	}
	uriPrefix := synchronizationPrefix(actorURI)
	if uriPrefix == "" {
		web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
		return
	}

	account, err := h.loadAccountFromRequest(r.Context(), r)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.JSONError(w, http.StatusNotFound, "Actor not found")
			return
		}
		web.JSONError(w, http.StatusInternalServerError, "Failed to load followers")
		return
	}

	items, err := h.store.ListFollowerURIsByPrefix(r.Context(), db.ListFollowerURIsByPrefixParams{
		TargetAccountID: account.ID,
		Prefix:          pgtype.Text{String: uriPrefix, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load followers")
		return
	}

	ordered := make([]any, 0, len(items))
	for _, uri := range items {
		trimmed := strings.TrimSpace(uri)
		if trimmed == "" {
			continue
		}
		ordered = append(ordered, trimmed)
	}

	collection := activitypub.OrderedCollection{
		Collection: activitypub.Collection{
			Object: activitypub.Object{
				Context: activitypub.DefaultContext(),
				ID:      h.absoluteRequestURL(r),
				Type:    activitypub.TypeOrderedCollection,
			},
		},
		OrderedItems: ordered,
	}

	w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
	web.JSON(w, http.StatusOK, collection)
}

func (h *FederationHandler) verifySignedRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.actorFetcher == nil {
		web.JSONError(w, http.StatusUnauthorized, "Signature verification unavailable")
		return "", false
	}

	keyID := signatureKeyID(r.Header.Get("Signature"))
	if keyID == "" {
		web.JSONError(w, http.StatusUnauthorized, "Missing HTTP signature")
		return "", false
	}

	actorURI := actorURIFromKeyID(keyID)
	if actorURI == "" {
		web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
		return "", false
	}

	actor, err := h.actorFetcher.FetchActor(r.Context(), actorURI)
	if err != nil || actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
		return "", false
	}

	verifier := activitypub.NewHTTPVerifier()
	if err := verifier.VerifyRequest(r, actor.PublicKey.PublicKeyPem); err != nil {
		web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
		return "", false
	}

	if actor.ID != "" {
		actorURI = actor.ID
	}
	return actorURI, true
}

func signatureKeyID(header string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.TrimSpace(kv[0]) == "keyId" {
			return strings.Trim(strings.TrimSpace(kv[1]), `"`)
		}
	}
	return ""
}

func actorURIFromKeyID(keyID string) string {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return ""
	}
	if idx := strings.Index(keyID, "#"); idx > 0 {
		return keyID[:idx]
	}
	return keyID
}

func synchronizationPrefix(actorURI string) string {
	parsed, err := url.Parse(actorURI)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s/", parsed.Scheme, parsed.Host)
}

func (h *FederationHandler) absoluteRequestURL(r *http.Request) string {
	if h.domain == "" {
		return r.URL.Path
	}
	return fmt.Sprintf("https://%s%s", h.domain, r.URL.Path)
}
