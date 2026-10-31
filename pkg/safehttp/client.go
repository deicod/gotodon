// Package safehttp provides the HTTP client for URLs that remote parties
// control, such as ActivityPub actor, object, WebFinger and inbox URLs.
//
// Its clients only ever connect to globally reachable unicast addresses. They
// resolve host names themselves, refuse an answer that contains any
// non-public address and dial the validated IP literals, so a DNS answer that
// changes between check and connect (rebinding) cannot reach an internal
// address. Every redirect hop is held to the same rules, proxies from the
// environment are ignored, and ReadBody caps how much of a response is read.
package safehttp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	// DefaultTimeout bounds a whole exchange, response body included.
	DefaultTimeout = 10 * time.Second

	// DefaultMaxRedirects is how many redirects a client follows.
	DefaultMaxRedirects = 5

	// DefaultMaxBodyBytes is the body limit ReadBody applies by default.
	DefaultMaxBodyBytes = 1 << 20

	maxResponseHeaderBytes = 256 << 10
)

var (
	// ErrUnsafeURL is returned for URLs that are not absolute http or https
	// URLs with a host and valid port, or that carry credentials.
	ErrUnsafeURL = errors.New("safehttp: url not allowed")

	// ErrForbiddenAddress is returned when a destination is, or resolves to,
	// an address that is not globally reachable unicast.
	ErrForbiddenAddress = errors.New("safehttp: destination address not allowed")

	// ErrTooManyRedirects is returned when a redirect chain exceeds the cap.
	ErrTooManyRedirects = errors.New("safehttp: too many redirects")

	// ErrBodyTooLarge is returned by ReadBody for bodies over its limit.
	ErrBodyTooLarge = errors.New("safehttp: response body too large")
)

// IsRefused reports whether err is a refusal by the URL or address policy
// (ErrUnsafeURL or ErrForbiddenAddress), which retrying will not change.
func IsRefused(err error) bool {
	return errors.Is(err, ErrUnsafeURL) || errors.Is(err, ErrForbiddenAddress)
}

// Options configures NewClient. The zero value is the production policy.
type Options struct {
	// Timeout bounds a whole exchange, response body included; zero selects
	// DefaultTimeout.
	Timeout time.Duration

	// MaxRedirects is how many redirects are followed; zero selects
	// DefaultMaxRedirects.
	MaxRedirects int

	// TLSClientConfig replaces the default TLS configuration.
	TLSClientConfig *tls.Config

	// Lookup replaces net.DefaultResolver for resolving host names. Its
	// answers are subject to the address policy.
	Lookup LookupFunc

	// Dial replaces the connect step. It only receives addresses that passed
	// the address policy.
	Dial DialFunc
}

// sharedTransport serves every client built with the production policy, so
// they share one connection pool however many clients are created.
var sharedTransport = sync.OnceValue(func() *guardedTransport {
	return newTransport(Options{})
})

// NewClient returns an HTTP client that enforces the package's address and
// URL policy on every request and redirect.
func NewClient(opts Options) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = DefaultMaxRedirects
	}

	transport := sharedTransport()
	if opts.TLSClientConfig != nil || opts.Lookup != nil || opts.Dial != nil {
		transport = newTransport(opts)
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: stopped after %d", ErrTooManyRedirects, maxRedirects)
			}
			return nil
		},
	}
}

// guardedTransport checks each request's URL, the first and every redirect
// hop alike, before handing it to a transport whose dialer enforces the
// address policy.
type guardedTransport struct {
	base *http.Transport
}

func newTransport(opts Options) *guardedTransport {
	return &guardedTransport{base: &http.Transport{
		// A proxy would make the dialed address the proxy's, so the
		// destination could no longer be checked.
		Proxy:                  nil,
		DialContext:            newDialer(opts.Lookup, opts.Dial).DialContext,
		TLSClientConfig:        opts.TLSClientConfig,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
	}}
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := CheckURL(req.URL); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the pool.
func (t *guardedTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
}

// CheckURL reports whether u may be requested: an absolute http or https URL
// without credentials, with a host and, if given, a port in 1-65535. An IP
// literal host must be public; host names are checked when they are resolved,
// immediately before connecting.
func CheckURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: missing url", ErrUnsafeURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrUnsafeURL, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in url", ErrUnsafeURL)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrUnsafeURL)
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return fmt.Errorf("%w: invalid port %q", ErrUnsafeURL, port)
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil && !IsPublicAddr(addr) {
		return forbiddenAddr(host, addr)
	}
	return nil
}

// ReadBody reads resp's body, failing with ErrBodyTooLarge rather than
// truncating when it is longer than limit bytes. The limit applies to the
// decoded body, so a compressed response cannot expand past it. A
// non-positive limit selects DefaultMaxBodyBytes.
func ReadBody(resp *http.Response, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxBodyBytes
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("%w: %d bytes declared, limit %d", ErrBodyTooLarge, resp.ContentLength, limit)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: limit %d", ErrBodyTooLarge, limit)
	}
	return body, nil
}
