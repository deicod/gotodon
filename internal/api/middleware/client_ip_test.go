package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustPrefixes(t *testing.T, raw ...string) []netip.Prefix {
	t.Helper()
	prefixes := make([]netip.Prefix, len(raw))
	for i, value := range raw {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			t.Fatalf("ParsePrefix(%q): %v", value, err)
		}
		prefixes[i] = prefix
	}
	return prefixes
}

func TestClientIPResolverResolve(t *testing.T) {
	resolver := NewClientIPResolver(mustPrefixes(t, "127.0.0.0/8", "::1/128", "10.0.0.0/8"))

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  []string
		realIP     string
		want       string
	}{
		{name: "direct client", remoteAddr: "203.0.113.5:4000", want: "203.0.113.5"},
		{name: "direct client spoofing X-Forwarded-For", remoteAddr: "203.0.113.5:4000", forwarded: []string{"198.51.100.1"}, want: "203.0.113.5"},
		{name: "direct client spoofing X-Real-IP", remoteAddr: "203.0.113.5:4000", realIP: "198.51.100.1", want: "203.0.113.5"},
		{name: "trusted proxy", remoteAddr: "127.0.0.1:4000", forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "client entries left of the proxy's are ignored", remoteAddr: "127.0.0.1:4000", forwarded: []string{"192.0.2.66, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "chain of trusted proxies", remoteAddr: "127.0.0.1:4000", forwarded: []string{"192.0.2.66, 198.51.100.1, 10.0.0.2"}, want: "198.51.100.1"},
		{name: "repeated headers are one list", remoteAddr: "127.0.0.1:4000", forwarded: []string{"192.0.2.66", "198.51.100.1, 10.0.0.2"}, want: "198.51.100.1"},
		{name: "only trusted hops yields the furthest", remoteAddr: "127.0.0.1:4000", forwarded: []string{"10.0.0.3, 10.0.0.2"}, want: "10.0.0.3"},
		{name: "garbage past trusted hops is not believed", remoteAddr: "127.0.0.1:4000", forwarded: []string{"not-an-ip, 10.0.0.2"}, want: "10.0.0.2"},
		{name: "garbage from the trusted proxy keeps the peer", remoteAddr: "127.0.0.1:4000", forwarded: []string{"unknown"}, want: "127.0.0.1"},
		{name: "hop with port", remoteAddr: "127.0.0.1:4000", forwarded: []string{"198.51.100.1:5555"}, want: "198.51.100.1"},
		{name: "bracketed IPv6 hop", remoteAddr: "127.0.0.1:4000", forwarded: []string{"[2001:db8::7]"}, want: "2001:db8::7"},
		{name: "bracketed IPv6 hop with port", remoteAddr: "[::1]:4000", forwarded: []string{"[2001:db8::7]:443"}, want: "2001:db8::7"},
		{name: "4in6 hop is unmapped", remoteAddr: "127.0.0.1:4000", forwarded: []string{"::ffff:198.51.100.1"}, want: "198.51.100.1"},
		{name: "4in6 trusted peer", remoteAddr: "[::ffff:127.0.0.1]:4000", forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "X-Real-IP from trusted proxy", remoteAddr: "127.0.0.1:4000", realIP: "198.51.100.1", want: "198.51.100.1"},
		{name: "X-Forwarded-For wins over X-Real-IP", remoteAddr: "127.0.0.1:4000", forwarded: []string{"198.51.100.1"}, realIP: "192.0.2.66", want: "198.51.100.1"},
		{name: "invalid X-Real-IP keeps the peer", remoteAddr: "127.0.0.1:4000", realIP: "nope", want: "127.0.0.1"},
		{name: "trusted proxy without headers", remoteAddr: "127.0.0.1:4000", want: "127.0.0.1"},
		{name: "peer without port", remoteAddr: "203.0.113.5", want: "203.0.113.5"},
		{name: "peer that is no address", remoteAddr: "@", forwarded: []string{"198.51.100.1"}, want: "@"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, value := range tt.forwarded {
				req.Header.Add("X-Forwarded-For", value)
			}
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			if got := resolver.Resolve(req); got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPResolverWithoutTrustedProxies(t *testing.T) {
	resolver := NewClientIPResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:4000"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.1")
	if got := resolver.Resolve(req); got != "127.0.0.1" {
		t.Errorf("Resolve() = %q, want the peer 127.0.0.1", got)
	}
}

func TestClientIPWithoutResolverIgnoresForwardingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:4000"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.2")
	if got := ClientIP(req); got != "127.0.0.1" {
		t.Errorf("ClientIP() = %q, want the peer 127.0.0.1", got)
	}
}

func TestResolveClientIPStoresAddressForHandlers(t *testing.T) {
	resolver := NewClientIPResolver(mustPrefixes(t, "127.0.0.0/8"))
	var (
		gotClientIP string
		gotContext  any
	)
	h := ResolveClientIP(resolver)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientIP = ClientIP(r)
		gotContext = r.Context().Value(ContextKeyClientIP)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:4000"
	req.Header.Set("X-Forwarded-For", "192.0.2.66, 198.51.100.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotClientIP != "198.51.100.1" || gotContext != "198.51.100.1" {
		t.Errorf("ClientIP() = %q, context value = %v, want 198.51.100.1", gotClientIP, gotContext)
	}
}

func TestAuthenticatePassesResolvedClientIPToValidator(t *testing.T) {
	var got any
	validator := validatorFunc(func(ctx context.Context, token string) (*TokenInfo, error) {
		got = ctx.Value(ContextKeyClientIP)
		return &TokenInfo{TokenID: 1, AppID: 1, Scopes: "read"}, nil
	})
	h := ResolveClientIP(NewClientIPResolver(nil))(Authenticate(validator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:4000"
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "203.0.113.5" {
		t.Errorf("validator saw client IP %v, want 203.0.113.5", got)
	}
}

type validatorFunc func(ctx context.Context, token string) (*TokenInfo, error)

func (f validatorFunc) ValidateToken(ctx context.Context, token string) (*TokenInfo, error) {
	return f(ctx, token)
}
