package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// VerifyHTTPSignature validates ActivityPub HTTP Signatures on incoming requests.
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

			var activity activitypub.Activity
			if err := json.Unmarshal(body, &activity); err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid activity payload")
				return
			}

			actorURI := extractActorURI(&activity)
			if actorURI == "" {
				web.JSONError(w, http.StatusBadRequest, "Missing activity actor")
				return
			}

			actor, err := fetcher.FetchActor(r.Context(), actorURI)
			if err != nil || actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			if err := verifyDigest(r.Header.Get("Digest"), body); err != nil {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			if err := verifier.VerifyRequest(r, actor.PublicKey.PublicKeyPem); err != nil {
				web.JSONError(w, http.StatusUnauthorized, "Signature verification failed")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
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

func verifyDigest(header string, body []byte) error {
	if header == "" {
		return fmt.Errorf("missing digest header")
	}

	expected := ""
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), "sha-256") {
			expected = strings.Trim(strings.TrimSpace(kv[1]), "\"")
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("missing sha-256 digest")
	}

	sum := sha256.Sum256(body)
	actual := base64.StdEncoding.EncodeToString(sum[:])
	if expected != actual {
		return fmt.Errorf("digest mismatch")
	}
	return nil
}
