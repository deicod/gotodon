package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deicod/gotodon/pkg/safehttp"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
)

func TestFetchDocument(t *testing.T) {
	tests := []struct {
		contentType string
		wantErr     bool
	}{
		{contentType: ActivityPubContentType},
		{contentType: ActivityPubLDContentType},
		{contentType: "application/ld+json"},
		{contentType: "Application/Activity+JSON; charset=utf-8"},
		{contentType: "application/json", wantErr: true},
		{contentType: "text/html; charset=utf-8", wantErr: true},
		{contentType: "application/jrd+json", wantErr: true},
		{contentType: "application/", wantErr: true},
		{contentType: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if accept := r.Header.Get("Accept"); accept != ActivityPubContentType+", "+ActivityPubLDContentType {
					t.Errorf("Accept = %q, want ActivityPub types", accept)
				}
				w.Header()["Content-Type"] = nil
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				_, _ = io.WriteString(w, `{"id":"https://remote.example/notes/1","type":"Note"}`)
			}))
			defer srv.Close()
			client := safehttptest.Client(t, map[string]*httptest.Server{"remote.example": srv})

			var object Object
			err := FetchDocument(context.Background(), client, "http://remote.example/notes/1", &object)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("FetchDocument() error = %v, want error %v", err, tt.wantErr)
			}
			if !tt.wantErr && object.ID != "https://remote.example/notes/1" {
				t.Fatalf("object id = %q, want the note", object.ID)
			}
		})
	}
}

func newWebFingerServer(t *testing.T, contentType string, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if r.Host == "example.com" {
			// Hosts that serve accounts from a subdomain redirect lookups.
			http.Redirect(w, r, "https://social.example.com"+r.URL.RequestURI(), http.StatusMovedPermanently)
			return
		}
		if r.URL.Path != "/.well-known/webfinger" {
			http.NotFound(w, r)
			return
		}
		resource := r.URL.Query().Get("resource")
		w.Header().Set("Content-Type", contentType)
		_ = json.NewEncoder(w).Encode(BuildWebFingerResponse(
			strings.TrimSuffix(strings.TrimPrefix(resource, "acct:"), "@"+r.Host),
			r.Host, "https://"+r.Host+"/users/alice", "https://"+r.Host+"/@alice"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebFingerClient_Lookup(t *testing.T) {
	for _, contentType := range []string{WebFingerContentType, "application/json; charset=utf-8"} {
		t.Run(contentType, func(t *testing.T) {
			srv := newWebFingerServer(t, contentType, nil)
			client := &WebFingerClient{httpClient: safehttptest.Client(t, map[string]*httptest.Server{
				"remote.example.com": srv,
				"example.com":        srv,
				"social.example.com": srv,
			})}

			for acct, wantActor := range map[string]string{
				"alice@remote.example.com":      "https://remote.example.com/users/alice",
				"acct:alice@remote.example.com": "https://remote.example.com/users/alice",
				"alice@example.com":             "https://social.example.com/users/alice",
			} {
				resp, err := client.Lookup(context.Background(), acct)
				if err != nil {
					t.Fatalf("Lookup(%s) error = %v", acct, err)
				}
				if got := resp.GetActorURI(); got != wantActor {
					t.Fatalf("Lookup(%s) actor = %q, want %q", acct, got, wantActor)
				}
			}
		})
	}
}

func TestWebFingerClient_LookupRefusesNonPublicDomains(t *testing.T) {
	var hits atomic.Int64
	internal := newWebFingerServer(t, WebFingerContentType, &hits)
	port := internal.URL[strings.LastIndex(internal.URL, ":")+1:]

	client := NewWebFingerClient()
	for _, acct := range []string{
		"alice@127.0.0.1:" + port,
		"alice@localhost:" + port,
		"alice@[::1]:" + port,
		"alice@169.254.169.254",
		"alice@10.0.0.1",
	} {
		if _, err := client.Lookup(context.Background(), acct); !errors.Is(err, safehttp.ErrForbiddenAddress) {
			t.Errorf("Lookup(%s) error = %v, want ErrForbiddenAddress", acct, err)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("internal server received %d lookups, want 0", got)
	}
}

func TestWebFingerClient_LookupRejectsInvalidDomains(t *testing.T) {
	var hits atomic.Int64
	srv := newWebFingerServer(t, WebFingerContentType, &hits)
	client := &WebFingerClient{httpClient: safehttptest.Client(t, map[string]*httptest.Server{"remote.example.com": srv})}

	for _, acct := range []string{
		"alice@",
		"alice@remote.example.com/admin#",
		"alice@remote.example.com?debug=1",
		"alice@remote.example.com#fragment",
		"alice@evil@remote.example.com",
		"alice@remote.example.com/",
		"alice@remote example.com",
	} {
		_, err := client.Lookup(context.Background(), acct)
		if err == nil || !strings.Contains(err.Error(), "invalid acct domain") {
			t.Errorf("Lookup(%q) error = %v, want invalid acct domain", acct, err)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("server received %d lookups, want 0", got)
	}
}

func TestWebFingerClient_LookupRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{
			name: "html",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, `{"subject":"acct:alice@remote.example.com"}`)
			},
		},
		{
			name: "oversized",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", WebFingerContentType)
				_, _ = io.WriteString(w, `{"subject":"`+strings.Repeat("a", MaxDocumentBytes)+`"}`)
			},
			wantErr: safehttp.ErrBodyTooLarge,
		},
		{
			name: "not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", WebFingerContentType)
				_, _ = io.WriteString(w, `{"subject":`)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(tt.handler)
			defer srv.Close()
			client := &WebFingerClient{httpClient: safehttptest.Client(t, map[string]*httptest.Server{"remote.example.com": srv})}

			_, err := client.Lookup(context.Background(), "alice@remote.example.com")
			if err == nil {
				t.Fatal("Lookup() succeeded, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Lookup() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
