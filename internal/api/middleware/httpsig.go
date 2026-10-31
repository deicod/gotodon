package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// ContextKeySignedActivity is the context key for the SignedActivity of a
// verified inbox request.
const ContextKeySignedActivity ContextKey = "signed_activity"

// SignedActivity describes an inbox request whose HTTP signature
// VerifyHTTPSignature has verified.
type SignedActivity struct {
	// ActorURI is the activity's actor, which owns the signing key.
	ActorURI string
	// Body is the request body. Handlers must not modify it.
	Body []byte
	// BodyDigest is the SHA-256 of the request body.
	BodyDigest [sha256.Size]byte
	// ValidUntil is the last instant at which the signature still verifies.
	ValidUntil time.Time
}

// SignedActivityFromContext returns the SignedActivity stored by
// VerifyHTTPSignature.
func SignedActivityFromContext(ctx context.Context) (SignedActivity, bool) {
	activity, ok := ctx.Value(ContextKeySignedActivity).(SignedActivity)
	return activity, ok
}

// VerifyHTTPSignature validates ActivityPub HTTP Signatures on incoming
// requests. The signature must cover (request-target), host, date (or
// (created)) and the Digest header, the signed digest must match the body,
// and the signing key must belong to the activity's actor. Verified requests
// carry a SignedActivity in their context. The whole body is read before the
// signature is checked, so it must run after LimitRequestBody.
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
				writeBodyReadError(w, err)
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

			ctx := context.WithValue(r.Context(), ContextKeySignedActivity, SignedActivity{
				ActorURI:   actorURI,
				Body:       body,
				BodyDigest: sha256.Sum256(body),
				ValidUntil: sig.ValidUntil(),
			})
			next.ServeHTTP(w, r.WithContext(ctx))
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
