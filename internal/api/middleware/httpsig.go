package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// VerifyHTTPSignature validates ActivityPub HTTP Signatures on incoming
// requests. The signature must cover (request-target), host, date (or
// (created)) and the Digest header, the signed digest must match the body,
// and the signing key must belong to the activity's actor.
func VerifyHTTPSignature(fetcher activitypub.ActorFetcher) func(http.Handler) http.Handler {
	verifier := activitypub.NewHTTPVerifier()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fetcher == nil {
				web.JSONError(w, http.StatusInternalServerError, "Signature verification unavailable")
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))

			if len(body) == 0 {
				web.JSONError(w, http.StatusBadRequest, "Missing request body")
				return
			}

			if r.Header.Get("Signature") == "" {
				web.JSONError(w, http.StatusUnauthorized, "Missing HTTP signature")
				return
			}

			// Reject weak, stale or digest-mismatched signatures before
			// fetching any remote key.
			sig, err := verifier.ParseRequest(r, body)
			if err != nil {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid activity payload")
				return
			}

			actorURI := extractActorURI(raw["actor"])
			if actorURI == "" {
				web.JSONError(w, http.StatusBadRequest, "Missing activity actor")
				return
			}

			actor, err := fetcher.FetchActor(r.Context(), actorURI)
			if err != nil {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			if err := activitypub.VerifyKeyOwner(actor, actorURI, sig.KeyID); err != nil {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			if err := sig.Verify(actor.PublicKey.PublicKeyPem); err != nil {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractActorURI(actor any) string {
	switch value := actor.(type) {
	case string:
		return value
	case map[string]any:
		if id, ok := value["id"].(string); ok {
			return id
		}
	}
	return ""
}
