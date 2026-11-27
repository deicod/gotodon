package middleware

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Client addresses.
//
// Behind a reverse proxy, a request's peer address is the proxy's. Proxies
// pass on the address they received the request from in X-Forwarded-For (or
// X-Real-IP), but a client can send those headers too, so they are only
// believed when the peer is a trusted proxy: ClientIPResolver then walks
// X-Forwarded-For from the right, past further trusted proxies, to the first
// address no trusted proxy vouches for. Addresses a client puts in the header
// end up to the left of it and are ignored.

// ClientIPResolver derives the originating address of requests from their
// peer address and the forwarding headers of trusted proxies.
type ClientIPResolver struct {
	trusted []netip.Prefix
}

// NewClientIPResolver creates a resolver that believes the forwarding headers
// of peers in trusted. With no trusted networks, the peer address is always
// the client address.
func NewClientIPResolver(trusted []netip.Prefix) *ClientIPResolver {
	return &ClientIPResolver{trusted: append([]netip.Prefix(nil), trusted...)}
}

// Resolve returns the originating address of r.
func (c *ClientIPResolver) Resolve(r *http.Request) string {
	peer, ok := parseHop(r.RemoteAddr)
	if !ok {
		return peerHost(r.RemoteAddr)
	}
	if !c.isTrusted(peer) {
		return peer.String()
	}

	var hops []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(header, ",")...)
	}
	if len(hops) == 0 {
		if realIP, ok := parseHop(r.Header.Get("X-Real-IP")); ok {
			return realIP.String()
		}
		return peer.String()
	}

	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseHop(hops[i])
		if !ok {
			// Trusted proxies record addresses; anything else came from
			// further out and is not believed.
			break
		}
		client = hop
		if !c.isTrusted(hop) {
			break
		}
	}
	return client.String()
}

func (c *ClientIPResolver) isTrusted(addr netip.Addr) bool {
	for _, prefix := range c.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHop parses an address as it appears in RemoteAddr or a forwarding
// header: bare, or with a port, IPv6 optionally in brackets.
func parseHop(raw string) (netip.Addr, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		addrPort, portErr := netip.ParseAddrPort(value)
		if portErr != nil {
			if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
				return netip.Addr{}, false
			}
			if addr, err = netip.ParseAddr(value[1 : len(value)-1]); err != nil {
				return netip.Addr{}, false
			}
		} else {
			addr = addrPort.Addr()
		}
	}
	return addr.WithZone("").Unmap(), true
}

// peerHost returns the host part of a peer address that is not an IP
// address, for listeners that do not report one.
func peerHost(remoteAddr string) string {
	value := strings.TrimSpace(remoteAddr)
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

// ResolveClientIP stores the originating address of each request, as
// resolver derives it, where ClientIP finds it. It must run before any
// handler or middleware that reads ClientIP.
func ResolveClientIP(resolver *ClientIPResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ContextKeyClientIP, resolver.Resolve(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIP returns the originating address of r as ResolveClientIP stored
// it. Without ResolveClientIP it returns the peer address: forwarding headers
// are never believed unless a resolver vouched for the proxy that sent them.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ContextKeyClientIP).(string); ok && ip != "" {
		return ip
	}
	if peer, ok := parseHop(r.RemoteAddr); ok {
		return peer.String()
	}
	return peerHost(r.RemoteAddr)
}
