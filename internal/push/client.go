package push

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// pushTTL matches Mastodon's Web::PushNotificationWorker::TTL (48 hours).
	pushTTL = 48 * time.Hour

	pushUrgency        = "normal"
	defaultHTTPTimeout = 30 * time.Second

	// maxCachedAudiences bounds the VAPID token cache; endpoints (and thus
	// audiences) are client-supplied, so the cache must not grow unbounded.
	maxCachedAudiences = 128
)

// Subscription is the client-supplied half of a Web Push subscription.
type Subscription struct {
	Endpoint string
	P256dh   string // base64 uncompressed P-256 public key
	Auth     string // base64 16-byte auth secret
}

// Sender delivers an encrypted push message and reports the HTTP status.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte) (int, error)
}

// Client sends Web Push messages signed with a VAPID key.
type Client struct {
	http    *http.Client
	vapid   *VAPIDKey
	subject string

	mu     sync.Mutex
	tokens map[string]cachedAuth
}

type cachedAuth struct {
	header  string
	expires time.Time
}

// NewClient creates a push client. subject is the VAPID contact claim
// (a mailto: or https: URI identifying the instance operator).
func NewClient(vapid *VAPIDKey, subject string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{http: httpClient, vapid: vapid, subject: subject, tokens: map[string]cachedAuth{}}
}

// authorization returns the Authorization header for an audience, reusing a
// signed VAPID token until shortly before it expires.
func (c *Client) authorization(audience string) (string, error) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.tokens[audience]; ok && now.Before(cached.expires) {
		return cached.header, nil
	}
	header, err := c.vapid.AuthorizationHeader(audience, c.subject, now)
	if err != nil {
		return "", err
	}
	if len(c.tokens) >= maxCachedAudiences {
		for cachedAudience, cached := range c.tokens {
			if !now.Before(cached.expires) {
				delete(c.tokens, cachedAudience)
			}
		}
		if len(c.tokens) >= maxCachedAudiences {
			clear(c.tokens)
		}
	}
	c.tokens[audience] = cachedAuth{header: header, expires: now.Add(vapidTokenExpiration - time.Hour)}
	return header, nil
}

// Send encrypts payload for the subscription and POSTs it to the endpoint,
// returning the push service's HTTP status code.
func (c *Client) Send(ctx context.Context, sub Subscription, payload []byte) (int, error) {
	uaPublic, err := decodeBase64(sub.P256dh)
	if err != nil {
		return 0, fmt.Errorf("decoding p256dh key: %w", err)
	}
	authSecret, err := decodeBase64(sub.Auth)
	if err != nil {
		return 0, fmt.Errorf("decoding auth secret: %w", err)
	}

	body, err := Encrypt(payload, uaPublic, authSecret)
	if err != nil {
		return 0, err
	}

	audience, err := endpointAudience(sub.Endpoint)
	if err != nil {
		return 0, err
	}
	authorization, err := c.authorization(audience)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(pushTTL.Seconds())))
	req.Header.Set("Urgency", pushUrgency)
	req.Header.Set("Authorization", authorization)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	return resp.StatusCode, nil
}
