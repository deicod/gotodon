// Package safehttptest lets tests reach local test servers through a
// safehttp client under public host names, with the address policy still in
// force for every other destination.
package safehttptest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/deicod/gotodon/pkg/safehttp"
)

// Client returns a safehttp client that resolves each host in servers to its
// own public address and connects that address to the host's test server,
// trusting the certificates of TLS servers. Any other name fails to resolve,
// and IP literals and redirects are checked as in production.
func Client(t testing.TB, servers map[string]*httptest.Server) *http.Client {
	t.Helper()

	hosts := make([]string, 0, len(servers))
	for host := range servers {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	if len(hosts) > 250 {
		t.Fatalf("safehttptest: %d hosts, at most 250 supported", len(hosts))
	}

	addrs := make(map[string]netip.Addr, len(hosts))
	listeners := make(map[netip.Addr]string, len(hosts))
	roots := x509.NewCertPool()
	for i, host := range hosts {
		srv := servers[host]
		addr := netip.AddrFrom4([4]byte{93, 184, 215, byte(i + 1)})
		addrs[strings.ToLower(host)] = addr
		listeners[addr] = srv.Listener.Addr().String()
		if cert := srv.Certificate(); cert != nil {
			roots.AddCert(cert)
		}
	}

	return safehttp.NewClient(safehttp.Options{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Lookup: func(_ context.Context, _, host string) ([]netip.Addr, error) {
			if addr, ok := addrs[strings.ToLower(host)]; ok {
				return []netip.Addr{addr}, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			target, err := netip.ParseAddrPort(address)
			if err != nil {
				return nil, err
			}
			listener, ok := listeners[target.Addr()]
			if !ok {
				return nil, fmt.Errorf("safehttptest: no test server at %s", address)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, listener)
		},
	})
}
