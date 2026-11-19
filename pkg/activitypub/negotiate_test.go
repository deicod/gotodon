package activitypub

import (
	"net/http"
	"testing"
)

func TestPrefersActivityPub(t *testing.T) {
	tests := []struct {
		name   string
		accept []string
		want   bool
	}{
		// Fetchers.
		{name: "Mastodon fetch", accept: []string{"application/activity+json, application/ld+json"}, want: true},
		{name: "activity+json", accept: []string{"application/activity+json"}, want: true},
		{name: "ld+json with ActivityStreams profile", accept: []string{`application/ld+json; profile="https://www.w3.org/ns/activitystreams"`}, want: true},
		{name: "ld+json without profile", accept: []string{"application/ld+json"}, want: true},
		{name: "ld+json with another profile", accept: []string{`application/ld+json; profile="https://example.com/other"`}, want: true},
		{name: "ld+json with unquoted profile", accept: []string{"application/ld+json; profile=https://www.w3.org/ns/activitystreams"}, want: true},
		{name: "activity+json and profiled ld+json", accept: []string{`application/activity+json, application/ld+json; profile="https://www.w3.org/ns/activitystreams"`}, want: true},
		{name: "profiled ld+json first", accept: []string{`application/ld+json; profile="https://www.w3.org/ns/activitystreams", application/activity+json`}, want: true},
		{name: "media types are case-insensitive", accept: []string{"Application/Activity+JSON"}, want: true},
		{name: "Mastodon JSON synonym application/json", accept: []string{"application/json"}, want: true},
		{name: "Mastodon JSON synonym text/x-json", accept: []string{"text/x-json"}, want: true},
		{name: "Accept split over two fields", accept: []string{"text/plain", "application/activity+json"}, want: true},
		{name: "empty elements are skipped", accept: []string{" , ,application/activity+json"}, want: true},

		// q-values.
		{name: "ActivityPub preferred over HTML", accept: []string{"text/html;q=0.9, application/activity+json"}, want: true},
		{name: "HTML preferred over ActivityPub", accept: []string{"application/activity+json;q=0.5, text/html"}, want: false},
		{name: "tie goes to the first listed: HTML", accept: []string{"text/html, application/activity+json"}, want: false},
		{name: "tie goes to the first listed: ActivityPub", accept: []string{"application/activity+json, text/html"}, want: true},
		{name: "ActivityPub at a low q is still acceptable", accept: []string{"image/png, application/activity+json;q=0.1"}, want: true},
		{name: "q=0 is not acceptable", accept: []string{"application/activity+json;q=0"}, want: false},
		{name: "q=0.000 is not acceptable", accept: []string{"application/activity+json;q=0.000"}, want: false},
		{name: "q=0 on one type, another acceptable", accept: []string{"application/activity+json;q=0, application/ld+json;q=0.1"}, want: true},
		{name: "HTML refused with q=0", accept: []string{"text/html;q=0, application/activity+json;q=0.001"}, want: true},
		{name: "q parameter name is case-insensitive", accept: []string{"text/html;Q=0.4, application/activity+json;Q=0.5"}, want: true},
		{name: "quoted q-value", accept: []string{`text/html;q="0.4", application/activity+json;q="0.5"`}, want: true},
		{name: "q=1.000", accept: []string{"text/html;q=0.9, application/activity+json;q=1.000"}, want: true},
		{name: "q-value above 1 is malformed", accept: []string{"application/activity+json;q=2"}, want: false},
		{name: "q-value 1.5 is malformed", accept: []string{"application/activity+json;q=1.5"}, want: false},
		{name: "q-value with four decimals is malformed", accept: []string{"application/activity+json;q=0.1234"}, want: false},
		{name: "q-value NaN is malformed", accept: []string{"application/activity+json;q=NaN"}, want: false},
		{name: "q-value with a sign is malformed", accept: []string{"application/activity+json;q=+0.5"}, want: false},
		{name: "q-value in exponent form is malformed", accept: []string{"application/activity+json;q=1e-1"}, want: false},
		{name: "malformed q skips only that range", accept: []string{"text/html;q=abc, application/activity+json;q=0.2"}, want: true},
		{name: "parameters before q", accept: []string{`text/html;q=0.5, application/ld+json; profile="https://www.w3.org/ns/activitystreams"; q=0.8`}, want: true},

		// Wildcards.
		{name: "ActivityPub then */*", accept: []string{"application/activity+json, */*"}, want: true},
		{name: "ActivityPub then */* with q", accept: []string{"application/activity+json, */*;q=0.1"}, want: true},
		{name: "*/* without q sorts last", accept: []string{"*/*, application/activity+json"}, want: true},
		{name: "*/* with q=1 keeps its place", accept: []string{"*/*;q=1, application/activity+json"}, want: false},
		{name: "*/* preferred by q", accept: []string{"application/activity+json;q=0.5, */*;q=0.8"}, want: false},
		{name: "*/* alone", accept: []string{"*/*"}, want: false},
		{name: "application/* resolves to HTML", accept: []string{"application/*"}, want: false},
		{name: "text/* resolves to HTML", accept: []string{"text/*"}, want: false},
		{name: "image/* names neither", accept: []string{"image/*, application/activity+json;q=0.5"}, want: true},

		// Browsers and other clients.
		{name: "no Accept header", accept: nil, want: false},
		{name: "empty Accept header", accept: []string{""}, want: false},
		{name: "Firefox", accept: []string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}, want: false},
		{name: "Chrome", accept: []string{"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"}, want: false},
		{name: "xhtml", accept: []string{"application/xhtml+xml"}, want: false},
		{name: "unrelated type only", accept: []string{"image/png"}, want: false},
		{name: "application/xml is not JSON", accept: []string{"application/xml"}, want: false},
		{name: "a bare type is malformed", accept: []string{"activity+json"}, want: false},
		{name: "a missing subtype is malformed", accept: []string{"application/"}, want: false},

		// Quoted strings.
		{name: "comma inside a quoted parameter", accept: []string{`text/plain; note=", application/activity+json, "`}, want: false},
		{name: "semicolon inside a quoted parameter", accept: []string{`application/ld+json; profile="a;q=0", text/html;q=0.5`}, want: true},
		{name: "escaped quote inside a quoted parameter", accept: []string{`text/plain; note="\", application/activity+json, \""`}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			for _, value := range tt.accept {
				header.Add("Accept", value)
			}
			if got := PrefersActivityPub(header); got != tt.want {
				t.Fatalf("PrefersActivityPub(%q) = %v, want %v", tt.accept, got, tt.want)
			}
		})
	}
}
