package activitypub

import (
	"net/http"
	"slices"
	"strings"
)

// activityPubAcceptTypes select the ActivityPub document of a resource that
// also has an HTML page. Mastodon 4.5.3 registers all of them as synonyms of
// its :json format (config/initializers/mime_types.rb), which its actor and
// status routes render as application/activity+json. Parameters such as the
// ActivityStreams profile of application/ld+json do not change the match.
var activityPubAcceptTypes = []string{
	ActivityPubContentType,
	"application/ld+json",
	"application/json",
	"application/jrd+json",
	"application/jsonrequest",
	"text/x-json",
}

// htmlAcceptTypes select the HTML page: Rails's :html format and its synonym,
// and the wildcards Rails resolves to it. */* resolves to the first format
// a controller declares, which is html for Mastodon's actor and status routes;
// text/* and application/* expand to the registered types, html first.
var htmlAcceptTypes = []string{
	"text/html",
	"application/xhtml+xml",
	"*/*",
	"text/*",
	"application/*",
}

// PrefersActivityPub reports whether a request with header should get the
// ActivityPub document of a resource that also has an HTML page, such as an
// actor or a status, rather than the page.
//
// It negotiates as Rails negotiates Mastodon 4.5.3's
// respond_to { html; json }. It takes the media ranges of every Accept
// header in order of descending q-value. Ties keep the order they are listed
// in, and a */* without a q-value sorts last. The first range that names an
// ActivityPub type selects the document. The first range that names HTML or
// a wildcard selects the page. Two deliberate differences from Rails: a q
// of 0 marks a range as not acceptable, as RFC 9110 defines it, and an
// Accept header that lists */* next to other ranges is negotiated like any
// other, where Rails discards it as a browser's and serves HTML. A missing
// header, or one that names neither representation, selects the page.
func PrefersActivityPub(header http.Header) bool {
	type acceptRange struct {
		mediaType string
		weight    int // q-value in thousandths
	}
	var ranges []acceptRange
	for _, value := range header.Values("Accept") {
		for _, element := range splitUnquoted(value, ',') {
			mediaType, weight, ok := parseAcceptRange(element)
			if !ok {
				continue
			}
			ranges = append(ranges, acceptRange{mediaType: mediaType, weight: weight})
		}
	}
	slices.SortStableFunc(ranges, func(a, b acceptRange) int { return b.weight - a.weight })
	for _, r := range ranges {
		switch {
		case slices.Contains(activityPubAcceptTypes, r.mediaType):
			return true
		case slices.Contains(htmlAcceptTypes, r.mediaType):
			return false
		}
	}
	return false
}

// parseAcceptRange parses one element of an Accept header into its lower-cased
// media range and its weight: the q-value in thousandths, or 0 for a */*
// without a q-value so that it sorts after every acceptable range. It fails
// for an empty or malformed range, a malformed q-value, and q=0.
func parseAcceptRange(element string) (string, int, bool) {
	parts := splitUnquoted(element, ';')
	mediaType := strings.ToLower(strings.TrimSpace(parts[0]))
	typ, subtype, found := strings.Cut(mediaType, "/")
	if !found || typ == "" || subtype == "" || strings.ContainsAny(mediaType, " \t\"") {
		return "", 0, false
	}
	for _, param := range parts[1:] {
		name, value, _ := strings.Cut(param, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		weight, ok := parseQValue(strings.Trim(strings.TrimSpace(value), `"`))
		if !ok || weight == 0 {
			return "", 0, false
		}
		return mediaType, weight, true
	}
	if mediaType == "*/*" {
		return mediaType, 0, true
	}
	return mediaType, 1000, true
}

// parseQValue parses an RFC 9110 qvalue, "0" or "1" with up to three
// decimals and at most 1, into thousandths.
func parseQValue(value string) (int, bool) {
	whole, fraction, _ := strings.Cut(value, ".")
	if (whole != "0" && whole != "1") || len(fraction) > 3 {
		return 0, false
	}
	thousandths := 0
	for _, c := range fraction {
		if c < '0' || c > '9' {
			return 0, false
		}
		thousandths = thousandths*10 + int(c-'0')
	}
	for range 3 - len(fraction) {
		thousandths *= 10
	}
	if whole == "1" {
		if thousandths != 0 {
			return 0, false
		}
		return 1000, true
	}
	return thousandths, true
}

// splitUnquoted splits s at each sep that is not inside a quoted string.
func splitUnquoted(s string, sep byte) []string {
	var parts []string
	start, quoted, escaped := 0, false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case escaped:
			escaped = false
		case quoted && c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case !quoted && c == sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}
