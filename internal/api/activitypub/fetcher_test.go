package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
)

// actorServer serves the current actor document and counts the requests it
// receives.
type actorServer struct {
	*httptest.Server
	actor atomic.Pointer[activitypub.Actor]
	hits  atomic.Int64
}

func newActorServer(t *testing.T) *actorServer {
	t.Helper()
	srv := &actorServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.hits.Add(1)
		if r.URL.Path == "/@alice" {
			http.Redirect(w, r, "/users/alice", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		_ = json.NewEncoder(w).Encode(srv.actor.Load())
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *actorServer) port() string {
	return s.URL[strings.LastIndex(s.URL, ":")+1:]
}

func createActivityBody(t *testing.T, actorURI string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type":   activitypub.TypeCreate,
		"actor":  actorURI,
		"object": map[string]any{"type": "Note", "content": "hello"},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

// postSignedCreate delivers a Create signed by actorURI's key to handler's
// inbox and reports the status and whether the activity was handled.
func postSignedCreate(t *testing.T, handler *FederationHandler, actorURI, privateKeyPEM string) (int, bool) {
	t.Helper()
	var handled atomic.Bool
	handler.RegisterInboxHandler(activitypub.TypeCreate, func(context.Context, *activitypub.Activity) error {
		handled.Store(true)
		return nil
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInboxRequest(t, createActivityBody(t, actorURI), actorURI+"#main-key", privateKeyPEM))
	return rec.Code, handled.Load()
}

// The actor of an inbox delivery is fetched to verify its signature, so the
// fetch happens on behalf of an unauthenticated sender and must not reach
// internal addresses, however the actor URI names them.
func TestFederationHandler_PostInbox_DoesNotFetchActorsFromInternalAddresses(t *testing.T) {
	internal := newActorServer(t)

	for _, actorURI := range []string{
		internal.URL + "/users/alice",
		"http://localhost:" + internal.port() + "/users/alice",
		"http://[::1]:" + internal.port() + "/users/alice",
		"http://[::ffff:127.0.0.1]:" + internal.port() + "/users/alice",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/users/alice",
	} {
		t.Run(actorURI, func(t *testing.T) {
			privateKeyPEM, actor := newSigningActor(t, actorURI)
			internal.actor.Store(actor)

			status, handled := postSignedCreate(t, NewFederationHandler(nil, "example.com"), actorURI, privateKeyPEM)
			if status != http.StatusUnauthorized || handled {
				t.Fatalf("status = %d, handled = %v, want %d and unhandled", status, handled, http.StatusUnauthorized)
			}
		})
	}
	if got := internal.hits.Load(); got != 0 {
		t.Fatalf("internal server received %d actor fetches, want 0", got)
	}

	// The same server, reached under a public name, authenticates the
	// delivery: the refusals above are the address policy at work.
	actorURI := "http://remote.example/users/alice"
	privateKeyPEM, actor := newSigningActor(t, actorURI)
	internal.actor.Store(actor)
	handler := NewFederationHandler(nil, "example.com")
	handler.actorFetcher = &actorFetcher{
		localDomain: "example.com",
		httpClient:  safehttptest.Client(t, map[string]*httptest.Server{"remote.example": internal.Server}),
	}
	if status, handled := postSignedCreate(t, handler, actorURI, privateKeyPEM); status != http.StatusAccepted || !handled {
		t.Fatalf("public actor: status = %d, handled = %v, want %d and handled", status, handled, http.StatusAccepted)
	}
	if got := internal.hits.Load(); got != 1 {
		t.Fatalf("public actor server received %d fetches, want 1", got)
	}
}

func TestActorFetcher_FetchRemoteActor(t *testing.T) {
	srv := newActorServer(t)
	_, actor := newSigningActor(t, "http://remote.example/users/alice")
	srv.actor.Store(actor)
	fetcher := &actorFetcher{
		localDomain: "example.com",
		httpClient:  safehttptest.Client(t, map[string]*httptest.Server{"remote.example": srv.Server}),
	}

	for _, uri := range []string{"http://remote.example/users/alice", "http://remote.example/@alice"} {
		got, err := fetcher.FetchActor(context.Background(), uri)
		if err != nil {
			t.Fatalf("FetchActor(%s) error = %v", uri, err)
		}
		if got.ID != actor.ID || got.PublicKey == nil || got.PublicKey.PublicKeyPem != actor.PublicKey.PublicKeyPem {
			t.Fatalf("FetchActor(%s) = %+v, want %+v", uri, got, actor)
		}
	}

	// A redirect is followed only to public destinations.
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/users/alice", http.StatusFound)
	}))
	defer redirecting.Close()
	fetcher.httpClient = safehttptest.Client(t, map[string]*httptest.Server{"remote.example": redirecting})
	before := srv.hits.Load()
	if _, err := fetcher.FetchActor(context.Background(), "http://remote.example/users/alice"); !errors.Is(err, safehttp.ErrForbiddenAddress) {
		t.Fatalf("FetchActor() redirected to loopback error = %v, want ErrForbiddenAddress", err)
	}
	if got := srv.hits.Load() - before; got != 0 {
		t.Fatalf("redirect target received %d requests, want 0", got)
	}
}

func TestActorFetcher_RejectsOversizedActor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		_, _ = w.Write([]byte(`{"id":"` + strings.Repeat("a", activitypub.MaxDocumentBytes) + `"}`))
	}))
	defer srv.Close()
	fetcher := &actorFetcher{httpClient: safehttptest.Client(t, map[string]*httptest.Server{"remote.example": srv})}

	if _, err := fetcher.FetchActor(context.Background(), "http://remote.example/users/alice"); !errors.Is(err, safehttp.ErrBodyTooLarge) {
		t.Fatalf("FetchActor() error = %v, want ErrBodyTooLarge", err)
	}
}
