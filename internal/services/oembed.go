package services

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/deicod/gotodon/pkg/safehttp"
)

const (
	defaultOEmbedTimeout   = 12 * time.Second
	defaultOEmbedHTMLLimit = 2 * 1024 * 1024
	defaultOEmbedUserAgent = "Gotodon OEmbed"
	maxOEmbedRedirects     = 5
)

// OEmbedFetcher discovers and fetches oEmbed payloads for remote URLs.
type OEmbedFetcher struct {
	client *http.Client
}

// NewOEmbedFetcher creates a new oEmbed fetcher with defaults. Remote statuses
// and the pages they link to choose the URLs it requests, so pages, endpoints
// and redirects are fetched through a safehttp client that only connects to
// public addresses.
func NewOEmbedFetcher() *OEmbedFetcher {
	return &OEmbedFetcher{
		client: safehttp.NewClient(safehttp.Options{Timeout: defaultOEmbedTimeout, MaxRedirects: maxOEmbedRedirects}),
	}
}

// Fetch discovers the oEmbed endpoint for a URL and returns its payload.
// A nil map with nil error means oEmbed is not available.
func (f *OEmbedFetcher) Fetch(ctx context.Context, rawURL string) (map[string]any, error) {
	if f == nil {
		return nil, errors.New("oembed fetcher unavailable")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !isHTTPURL(parsed) {
		return nil, nil
	}

	endpoint, format, err := f.discoverEndpoint(ctx, parsed)
	if err != nil || endpoint == "" {
		return nil, err
	}
	endpointURL, err := buildOEmbedEndpoint(endpoint, parsed.String())
	if err != nil {
		return nil, err
	}
	endpointParsed, err := url.Parse(endpointURL)
	if err != nil || !isHTTPURL(endpointParsed) {
		return nil, nil
	}

	return f.fetchEndpoint(ctx, endpointURL, format)
}

func (f *OEmbedFetcher) discoverEndpoint(ctx context.Context, target *url.URL) (string, string, error) {
	htmlBody, err := f.fetchHTML(ctx, target.String())
	if err != nil || htmlBody == "" {
		return "", "", err
	}

	jsonEndpoint := findOEmbedLink(htmlBody, []string{"application/json+oembed", "text/json+oembed"})
	if jsonEndpoint != "" {
		resolved, err := resolveEndpoint(target, jsonEndpoint)
		return resolved, "json", err
	}

	xmlEndpoint := findOEmbedLink(htmlBody, []string{"text/xml+oembed"})
	if xmlEndpoint != "" {
		resolved, err := resolveEndpoint(target, xmlEndpoint)
		return resolved, "xml", err
	}

	return "", "", nil
}

func (f *OEmbedFetcher) fetchHTML(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultOEmbedUserAgent)
	req.Header.Set("Accept", "text/html")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		return "", nil
	}

	body, err := readBody(resp, defaultOEmbedHTMLLimit)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (f *OEmbedFetcher) fetchEndpoint(ctx context.Context, endpointURL, format string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultOEmbedUserAgent)
	req.Header.Set("Accept", "application/json, text/xml, application/xml")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	body, err := readBody(resp, defaultOEmbedHTMLLimit)
	if err != nil {
		return nil, err
	}

	var payload map[string]any
	switch format {
	case "xml":
		payload = parseOEmbedXML(body)
	default:
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, nil
		}
	}

	if payload == nil {
		return nil, nil
	}
	if version, ok := payload["version"].(string); !ok || version != "1.0" {
		return nil, nil
	}
	if typ, ok := payload["type"].(string); !ok || strings.TrimSpace(typ) == "" {
		return nil, nil
	}
	return payload, nil
}

func findOEmbedLink(htmlBody string, types []string) string {
	if strings.TrimSpace(htmlBody) == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return ""
	}
	allowed := map[string]struct{}{}
	for _, t := range types {
		allowed[strings.ToLower(t)] = struct{}{}
	}

	var link string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			var href, typ string
			for _, attr := range n.Attr {
				switch strings.ToLower(attr.Key) {
				case "href":
					href = strings.TrimSpace(attr.Val)
				case "type":
					typ = strings.ToLower(strings.TrimSpace(attr.Val))
				}
			}
			if href != "" {
				if _, ok := allowed[typ]; ok {
					link = href
					return
				}
			}
		}
		for child := n.FirstChild; child != nil && link == ""; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return link
}

func resolveEndpoint(base *url.URL, endpoint string) (string, error) {
	if strings.TrimSpace(endpoint) == "" {
		return "", nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if base == nil {
		return parsed.String(), nil
	}
	resolved := base.ResolveReference(parsed)
	if strings.EqualFold(base.Scheme, "https") && strings.EqualFold(resolved.Scheme, "http") {
		resolved.Scheme = "https"
	}
	return resolved.String(), nil
}

func buildOEmbedEndpoint(endpoint, target string) (string, error) {
	if strings.Contains(endpoint, "{url}") {
		return strings.ReplaceAll(endpoint, "{url}", url.QueryEscape(target)), nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	if q.Get("url") == "" {
		q.Set("url", target)
		parsed.RawQuery = q.Encode()
	}
	return parsed.String(), nil
}

func parseOEmbedXML(body []byte) map[string]any {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	data := map[string]any{}
	var current string
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			current = t.Name.Local
		case xml.EndElement:
			current = ""
		case xml.CharData:
			if current == "" {
				continue
			}
			value := strings.TrimSpace(string(t))
			if value == "" {
				continue
			}
			if _, exists := data[current]; !exists {
				data[current] = value
			}
		}
	}
	if len(data) == 0 {
		return nil
	}
	return data
}
