// Package activitypub provides WebFinger discovery.
package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebFinger response structure
type WebFingerResponse struct {
	Subject string          `json:"subject"`
	Aliases []string        `json:"aliases,omitempty"`
	Links   []WebFingerLink `json:"links,omitempty"`
}

// WebFingerLink represents a link in the WebFinger response
type WebFingerLink struct {
	Rel      string `json:"rel"`
	Type     string `json:"type,omitempty"`
	Href     string `json:"href,omitempty"`
	Template string `json:"template,omitempty"`
}

// WebFinger link relation types
const (
	WebFingerRelSelf        = "self"
	WebFingerRelProfilePage = "http://webfinger.net/rel/profile-page"
	WebFingerRelOStatusSub  = "http://ostatus.org/schema/1.0/subscribe"
)

// WebFinger content types
const (
	WebFingerContentType     = "application/jrd+json"
	ActivityPubContentType   = "application/activity+json"
	ActivityPubLDContentType = "application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\""
)

// WebFingerClient handles WebFinger lookups
type WebFingerClient struct {
	httpClient *http.Client
}

// NewWebFingerClient creates a new WebFinger client
func NewWebFingerClient() *WebFingerClient {
	return &WebFingerClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Lookup performs a WebFinger lookup for the given acct
// acct format: user@domain or acct:user@domain
func (c *WebFingerClient) Lookup(ctx context.Context, acct string) (*WebFingerResponse, error) {
	// Parse acct
	acct = strings.TrimPrefix(acct, "acct:")
	parts := strings.SplitN(acct, "@", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid acct format: %s", acct)
	}
	domain := parts[1]

	// Build WebFinger URL
	webfingerURL := fmt.Sprintf("https://%s/.well-known/webfinger?resource=%s",
		domain, url.QueryEscape("acct:"+acct))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webfingerURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", WebFingerContentType+", "+ActivityPubContentType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webfinger request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webfinger returned status %d", resp.StatusCode)
	}

	var result WebFingerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode webfinger response: %w", err)
	}

	return &result, nil
}

// GetActorURI extracts the ActivityPub actor URI from a WebFinger response
func (r *WebFingerResponse) GetActorURI() string {
	for _, link := range r.Links {
		if link.Rel == WebFingerRelSelf &&
			(link.Type == ActivityPubContentType || link.Type == ActivityPubLDContentType) {
			return link.Href
		}
	}
	return ""
}

// GetProfileURL extracts the profile page URL from a WebFinger response
func (r *WebFingerResponse) GetProfileURL() string {
	for _, link := range r.Links {
		if link.Rel == WebFingerRelProfilePage && link.Type == "text/html" {
			return link.Href
		}
	}
	return ""
}

// BuildWebFingerResponse builds a WebFinger response for a local account
func BuildWebFingerResponse(username, domain, accountURI, profileURL string) *WebFingerResponse {
	return &WebFingerResponse{
		Subject: fmt.Sprintf("acct:%s@%s", username, domain),
		Aliases: []string{
			profileURL,
			accountURI,
		},
		Links: []WebFingerLink{
			{
				Rel:  WebFingerRelProfilePage,
				Type: "text/html",
				Href: profileURL,
			},
			{
				Rel:  WebFingerRelSelf,
				Type: ActivityPubContentType,
				Href: accountURI,
			},
			{
				Rel:      WebFingerRelOStatusSub,
				Template: fmt.Sprintf("https://%s/authorize_interaction?uri={uri}", domain),
			},
		},
	}
}

// ParseAcct parses an acct URI into username and domain
func ParseAcct(acct string) (username, domain string, err error) {
	acct = strings.TrimPrefix(acct, "acct:")
	acct = strings.TrimPrefix(acct, "@")

	parts := strings.SplitN(acct, "@", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid acct format: %s", acct)
	}

	return parts[0], parts[1], nil
}
