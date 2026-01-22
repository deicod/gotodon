package v1

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func TestShortenedLink_LongURL(t *testing.T) {
	url := "https://example.com/this/is/a/very/long/path/for/testing"

	got := shortenedLink(url, false)
	want := `<a href="https://example.com/this/is/a/very/long/path/for/testing" target="_blank" rel="nofollow noopener" translate="no">` +
		`<span class="invisible">https://</span>` +
		`<span class="ellipsis">example.com/this/is/a/very/lon</span>` +
		`<span class="invisible">g/path/for/testing</span>` +
		`</a>`

	if got != want {
		t.Fatalf("shortenedLink = %q, want %q", got, want)
	}
}

func TestFormatStatusContent_LocalLinkify(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	url := "https://example.com/this/is/a/very/long/path/for/testing"

	status := db.Status{
		Text:  "Hello @alice check #golang " + url,
		Local: pgtype.Bool{Bool: true, Valid: true},
	}
	author := Account{Username: "bob", Acct: "bob", URL: "https://web.test/@bob"}
	mentions := []Mention{
		{Username: "alice", Acct: "alice", URL: "https://web.test/@alice"},
	}
	tags := []Tag{{Name: "golang"}}

	got := formatStatusContent(cfg, status, statusContentOptions{
		author:   &author,
		mentions: mentions,
		tags:     tags,
	})

	mentionHTML := `<span class="h-card" translate="no"><a href="https://web.test/@alice" class="u-url mention">@<span>alice</span></a></span>`
	hashtagHTML := `<a href="https://web.test/tags/golang" class="mention hashtag" rel="tag">#<span>golang</span></a>`
	want := "<p>Hello " + mentionHTML + " check " + hashtagHTML + " " + shortenedLink(url, false) + "</p>"

	if got != want {
		t.Fatalf("formatStatusContent = %q, want %q", got, want)
	}
}

func TestFormatStatusContent_MentionShowsDomainWhenDuplicate(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}

	status := db.Status{
		Text:  "@alice@remote.test hi",
		Local: pgtype.Bool{Bool: true, Valid: true},
	}
	author := Account{Username: "alice", Acct: "alice", URL: "https://web.test/@alice"}
	mentions := []Mention{
		{Username: "alice", Acct: "alice@remote.test", URL: "https://remote.test/@alice"},
	}

	got := formatStatusContent(cfg, status, statusContentOptions{
		author:   &author,
		mentions: mentions,
	})

	want := `<p><span class="h-card" translate="no"><a href="https://remote.test/@alice" class="u-url mention">@<span>alice@remote.test</span></a></span> hi</p>`
	if got != want {
		t.Fatalf("formatStatusContent = %q, want %q", got, want)
	}
}

func TestFormatStatusContent_QuoteFallback(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.test", WebDomain: "web.test"}}
	quoteURL := "https://web.test/@alice/123"

	status := db.Status{
		Text:  "hello",
		Local: pgtype.Bool{Bool: true, Valid: true},
	}
	author := Account{Username: "bob", Acct: "bob", URL: "https://web.test/@bob"}
	quote := Status{URL: &quoteURL}

	got := formatStatusContent(cfg, status, statusContentOptions{
		author: &author,
		quote:  &quote,
	})

	want := `<p class="quote-inline">RE: ` + shortenedLink(quoteURL, false) + `</p><p>hello</p>`
	if got != want {
		t.Fatalf("formatStatusContent = %q, want %q", got, want)
	}
}

func TestFormatStatusContent_RemoteSanitizes(t *testing.T) {
	status := db.Status{
		Text:  `<p class="foo mention">hi</p><a href="javascript:alert(1)">bad</a><a href="https://example.com" class="ellipsis">good</a>`,
		Local: pgtype.Bool{Bool: false, Valid: true},
	}

	got := formatStatusContent(nil, status, statusContentOptions{})

	if strings.Contains(got, "javascript:") {
		t.Fatalf("expected javascript href to be removed, got %q", got)
	}
	if strings.Contains(got, "foo") {
		t.Fatalf("expected disallowed class to be removed, got %q", got)
	}
	if !strings.Contains(got, `class="mention"`) {
		t.Fatalf("expected mention class to remain, got %q", got)
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("expected allowed link to remain, got %q", got)
	}
	if !strings.Contains(got, `rel="nofollow noopener"`) || !strings.Contains(got, `target="_blank"`) {
		t.Fatalf("expected rel/target to be enforced, got %q", got)
	}
	if !strings.Contains(got, "bad") {
		t.Fatalf("expected link text to remain, got %q", got)
	}
}

func TestFormatStatusContent_RemoteSanitizes_FastPath(t *testing.T) {
	// This test exercises the fast path (no links/classes/translate).
	status := db.Status{
		Text:  `<p>Hello world</p>`,
		Local: pgtype.Bool{Bool: false, Valid: true},
	}
	got := formatStatusContent(nil, status, statusContentOptions{})
	want := `<p>Hello world</p>`
	if got != want {
		t.Fatalf("formatStatusContent = %q, want %q", got, want)
	}
}
