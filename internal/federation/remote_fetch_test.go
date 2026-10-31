package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
)

const remoteActorJSON = `{"@context":"https://www.w3.org/ns/activitystreams",` +
	`"id":"https://remote.example.com/users/alice","type":"Person","preferredUsername":"alice",` +
	`"inbox":"https://remote.example.com/users/alice/inbox",` +
	`"publicKey":{"id":"https://remote.example.com/users/alice#main-key","owner":"https://remote.example.com/users/alice","publicKeyPem":"pem"}}`

// remoteFetch runs one of the HTTP fetchers that follow URIs taken from
// remote documents and activities.
type remoteFetch func(ctx context.Context, client *http.Client, uri string) (id string, err error)

var remoteFetchers = map[string]struct {
	fetch      remoteFetch
	production func() *http.Client
}{
	"actor": {
		fetch: func(ctx context.Context, client *http.Client, uri string) (string, error) {
			payload, err := (&httpActorFetcher{httpClient: client}).FetchActorPayload(ctx, uri)
			return payload.ID, err
		},
		production: func() *http.Client {
			return NewRemoteActorResolver(nil, "local.test").fetcher.(*httpActorFetcher).httpClient
		},
	},
	"object": {
		fetch: func(ctx context.Context, client *http.Client, uri string) (string, error) {
			payload, err := (&httpObjectFetcher{client: client}).FetchObject(ctx, uri)
			object, _ := payload.(map[string]any)
			id, _ := object["id"].(string)
			return id, err
		},
		production: func() *http.Client { return newHTTPObjectFetcher().client },
	},
}

func TestRemoteFetchers_RefuseNonPublicURIs(t *testing.T) {
	var hits atomic.Int64
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		_, _ = io.WriteString(w, remoteActorJSON)
	}))
	defer internal.Close()
	port := internal.URL[strings.LastIndex(internal.URL, ":")+1:]

	uris := []string{
		internal.URL + "/users/alice",
		"http://localhost:" + port + "/users/alice",
		"http://[::1]:" + port + "/users/alice",
		"http://[::ffff:127.0.0.1]:" + port + "/users/alice",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/users/alice",
		"http://[fd00:ec2::254]/latest/meta-data/",
	}
	for name, fetcher := range remoteFetchers {
		client := fetcher.production()
		for _, uri := range uris {
			t.Run(name+" "+uri, func(t *testing.T) {
				if _, err := fetcher.fetch(context.Background(), client, uri); !errors.Is(err, safehttp.ErrForbiddenAddress) {
					t.Fatalf("fetch error = %v, want ErrForbiddenAddress", err)
				}
			})
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("internal server received %d requests, want 0", got)
	}
}

func TestRemoteActorResolver_RefusesNonPublicActors(t *testing.T) {
	var hits atomic.Int64
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", activitypub.ActivityPubContentType)
		_, _ = io.WriteString(w, remoteActorJSON)
	}))
	defer internal.Close()
	port := internal.URL[strings.LastIndex(internal.URL, ":")+1:]

	store := newResolverStore()
	resolver := NewRemoteActorResolver(store, "local.test")
	for _, acct := range []string{
		internal.URL + "/users/alice",
		"https://localhost:" + port + "/users/alice",
		"alice@127.0.0.1:" + port,
		"alice@localhost:" + port,
		"alice@[::1]:" + port,
	} {
		if _, err := resolver.ResolveAccount(context.Background(), acct); !errors.Is(err, safehttp.ErrForbiddenAddress) {
			t.Errorf("ResolveAccount(%s) error = %v, want ErrForbiddenAddress", acct, err)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("internal server received %d requests, want 0", got)
	}
	if len(store.accounts) != 0 {
		t.Fatalf("stored %d accounts, want 0", len(store.accounts))
	}
}

func TestRemoteFetchers_FetchPublicDocuments(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/@alice":
			http.Redirect(w, r, "https://cdn.example.com/users/alice", http.StatusMovedPermanently)
		default:
			if accept := r.Header.Get("Accept"); !strings.Contains(accept, activitypub.ActivityPubContentType) {
				t.Errorf("Accept = %q, want ActivityPub", accept)
			}
			w.Header().Set("Content-Type", activitypub.ActivityPubLDContentType)
			_, _ = io.WriteString(w, remoteActorJSON)
		}
	}))
	defer origin.Close()
	client := safehttptest.Client(t, map[string]*httptest.Server{
		"remote.example.com": origin,
		"cdn.example.com":    origin,
	})

	for name, fetcher := range remoteFetchers {
		for _, uri := range []string{"https://remote.example.com/users/alice", "https://remote.example.com/@alice"} {
			t.Run(name+" "+uri, func(t *testing.T) {
				id, err := fetcher.fetch(context.Background(), client, uri)
				if err != nil {
					t.Fatalf("fetch error = %v", err)
				}
				if id != "https://remote.example.com/users/alice" {
					t.Fatalf("id = %q, want the actor id", id)
				}
			})
		}
	}
}

func TestRemoteFetchers_RejectInvalidResponses(t *testing.T) {
	oversized := `{"id":"` + strings.Repeat("a", activitypub.MaxDocumentBytes) + `"}`
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		chunked     bool
		wantErr     error
	}{
		{name: "oversized", status: http.StatusOK, contentType: activitypub.ActivityPubContentType, body: oversized, wantErr: safehttp.ErrBodyTooLarge},
		{name: "oversized chunked", status: http.StatusOK, contentType: activitypub.ActivityPubContentType, body: oversized, chunked: true, wantErr: safehttp.ErrBodyTooLarge},
		{name: "html", status: http.StatusOK, contentType: "text/html; charset=utf-8", body: remoteActorJSON},
		{name: "plain json", status: http.StatusOK, contentType: "application/json", body: remoteActorJSON},
		{name: "missing content type", status: http.StatusOK, body: remoteActorJSON},
		{name: "not found", status: http.StatusNotFound, contentType: activitypub.ActivityPubContentType, body: remoteActorJSON},
		{name: "invalid json", status: http.StatusOK, contentType: activitypub.ActivityPubContentType, body: `{"id":`},
	}
	for _, tt := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header()["Content-Type"] = nil
			if tt.contentType != "" {
				w.Header().Set("Content-Type", tt.contentType)
			}
			if !tt.chunked {
				w.Header().Set("Content-Length", strconv.Itoa(len(tt.body)))
			}
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, tt.body)
		}))
		client := safehttptest.Client(t, map[string]*httptest.Server{"remote.example": srv})
		for name, fetcher := range remoteFetchers {
			t.Run(name+" "+tt.name, func(t *testing.T) {
				_, err := fetcher.fetch(context.Background(), client, "http://remote.example/users/alice")
				if err == nil {
					t.Fatal("fetch succeeded, want error")
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("fetch error = %v, want %v", err, tt.wantErr)
				}
			})
		}
		srv.Close()
	}
}
