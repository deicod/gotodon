package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/pkg/safehttp"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
)

const oembedRichJSON = `{"version":"1.0","type":"rich","html":"<iframe src=\"https://social.example.com/embed\"></iframe>","width":400}`

// newRoutedOEmbedFetcher returns a fetcher whose requests reach servers under
// public host names, with the address policy in force for everything else.
func newRoutedOEmbedFetcher(t *testing.T, servers map[string]*httptest.Server) *OEmbedFetcher {
	t.Helper()
	f := NewOEmbedFetcher()
	f.client = safehttptest.Client(t, servers)
	return f
}

// oembedPage serves an HTML page announcing an oEmbed endpoint of linkType.
func oembedPage(linkType, href string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><link rel="alternate" type="%s" href="%s"></head><body>post</body></html>`,
			linkType, html.EscapeString(href))
	}
}

func respondWith(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}
}

func TestOEmbedFetcherFetch(t *testing.T) {
	const target = "https://social.example.com/@alice/1"

	t.Run("json endpoint", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/@alice/1", oembedPage("application/json+oembed", "/api/oembed?format=json"))
		var gotURL string
		mux.HandleFunc("/api/oembed", func(w http.ResponseWriter, r *http.Request) {
			gotURL = r.URL.Query().Get("url")
			respondWith("application/json", oembedRichJSON)(w, r)
		})
		site := httptest.NewTLSServer(mux)
		defer site.Close()

		payload, err := newRoutedOEmbedFetcher(t, map[string]*httptest.Server{"social.example.com": site}).Fetch(context.Background(), target)
		if err != nil {
			t.Fatalf("Fetch error = %v", err)
		}
		if payload["type"] != "rich" || payload["html"] != `<iframe src="https://social.example.com/embed"></iframe>` {
			t.Fatalf("payload = %v, want the rich embed", payload)
		}
		if gotURL != target {
			t.Fatalf("endpoint url parameter = %q, want %q", gotURL, target)
		}
	})

	t.Run("xml endpoint on another host after a redirect", func(t *testing.T) {
		short := httptest.NewTLSServer(http.RedirectHandler(target, http.StatusFound))
		defer short.Close()
		site := httptest.NewTLSServer(oembedPage("text/xml+oembed", "https://embed.example.com/oembed?url={url}"))
		defer site.Close()
		embed := httptest.NewTLSServer(respondWith("text/xml", `<?xml version="1.0"?><oembed><version>1.0</version><type>video</type><html>&lt;iframe&gt;&lt;/iframe&gt;</html></oembed>`))
		defer embed.Close()

		payload, err := newRoutedOEmbedFetcher(t, map[string]*httptest.Server{
			"example.com":        short,
			"social.example.com": site,
			"embed.example.com":  embed,
		}).Fetch(context.Background(), "https://example.com/s/1")
		if err != nil {
			t.Fatalf("Fetch error = %v", err)
		}
		if payload["type"] != "video" || payload["html"] != "<iframe></iframe>" {
			t.Fatalf("payload = %v, want the video embed", payload)
		}
	})

	unavailable := []struct {
		name     string
		page     http.HandlerFunc
		endpoint http.HandlerFunc
	}{
		{"no oembed link", respondWith("text/html", "<html><head></head><body>post</body></html>"), nil},
		{"page is not html", respondWith("application/json", "{}"), nil},
		{"page not found", http.NotFound, nil},
		{"endpoint not found", nil, http.NotFound},
		{"endpoint returns invalid json", nil, respondWith("application/json", "{")},
		{"endpoint returns another version", nil, respondWith("application/json", `{"version":"2.0","type":"rich"}`)},
		{"endpoint returns no type", nil, respondWith("application/json", `{"version":"1.0"}`)},
	}
	for _, tt := range unavailable {
		t.Run(tt.name, func(t *testing.T) {
			page, endpoint := tt.page, tt.endpoint
			if page == nil {
				page = oembedPage("application/json+oembed", "/api/oembed")
			}
			if endpoint == nil {
				endpoint = respondWith("application/json", oembedRichJSON)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/@alice/1", page)
			mux.HandleFunc("/api/oembed", endpoint)
			site := httptest.NewTLSServer(mux)
			defer site.Close()

			payload, err := newRoutedOEmbedFetcher(t, map[string]*httptest.Server{"social.example.com": site}).Fetch(context.Background(), target)
			if err != nil || payload != nil {
				t.Fatalf("Fetch = %v, %v, want no payload and no error", payload, err)
			}
		})
	}

	t.Run("non-http url", func(t *testing.T) {
		payload, err := NewOEmbedFetcher().Fetch(context.Background(), "ftp://social.example.com/@alice/1")
		if err != nil || payload != nil {
			t.Fatalf("Fetch = %v, %v, want no payload and no error", payload, err)
		}
	})

	t.Run("page too large", func(t *testing.T) {
		site := httptest.NewTLSServer(respondWith("text/html", string(bytes.Repeat([]byte("a"), defaultOEmbedHTMLLimit+1))))
		defer site.Close()

		_, err := newRoutedOEmbedFetcher(t, map[string]*httptest.Server{"social.example.com": site}).Fetch(context.Background(), target)
		if !errors.Is(err, safehttp.ErrBodyTooLarge) {
			t.Fatalf("Fetch error = %v, want ErrBodyTooLarge", err)
		}
	})
}

func TestOEmbedFetcherRefusesInternalAddresses(t *testing.T) {
	internal, hits := newLoopbackServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oembed" {
			respondWith("application/json", oembedRichJSON)(w, r)
			return
		}
		oembedPage("application/json+oembed", "/oembed")(w, r)
	})
	_, port, _ := net.SplitHostPort(internal.Listener.Addr().String())

	// The production client, resolving through the system resolver.
	for _, target := range []string{
		internal.URL + "/@alice/1",
		"http://localhost:" + port + "/@alice/1",
		"http://[::1]:" + port + "/@alice/1",
		"http://169.254.169.254/latest/meta-data/",
	} {
		t.Run(target, func(t *testing.T) {
			payload, err := NewOEmbedFetcher().Fetch(context.Background(), target)
			if !safehttp.IsRefused(err) || payload != nil {
				t.Fatalf("Fetch = %v, %v, want a refusal", payload, err)
			}
		})
	}

	routed := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"page redirects to an internal address", http.RedirectHandler(internal.URL+"/@alice/1", http.StatusFound).ServeHTTP},
		{"endpoint on an internal address", oembedPage("application/json+oembed", internal.URL+"/oembed")},
		{"endpoint redirects to an internal address", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oembed" {
				http.Redirect(w, r, internal.URL+"/oembed", http.StatusFound)
				return
			}
			oembedPage("application/json+oembed", "/oembed")(w, r)
		}},
	}
	for _, tt := range routed {
		t.Run(tt.name, func(t *testing.T) {
			site := httptest.NewServer(tt.handler)
			defer site.Close()

			payload, err := newRoutedOEmbedFetcher(t, map[string]*httptest.Server{"social.example.com": site}).Fetch(context.Background(), "http://social.example.com/@alice/1")
			if !errors.Is(err, safehttp.ErrForbiddenAddress) || payload != nil {
				t.Fatalf("Fetch = %v, %v, want ErrForbiddenAddress", payload, err)
			}
		})
	}

	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d requests, want 0", n)
	}
}
