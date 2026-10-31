package safehttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubNet is the network of a client under test: names resolve through
// resolve, and every dial is recorded and then handed to connect.
type stubNet struct {
	resolve func(host string, call int) []string
	connect func(ctx context.Context, address string) (net.Conn, error)

	mu      sync.Mutex
	lookups map[string]int
	dialed  []string
}

func newStubNet(resolve func(host string, call int) []string, connect func(ctx context.Context, address string) (net.Conn, error)) *stubNet {
	return &stubNet{resolve: resolve, connect: connect, lookups: make(map[string]int)}
}

func (n *stubNet) lookup(_ context.Context, _, host string) ([]netip.Addr, error) {
	n.mu.Lock()
	n.lookups[host]++
	call := n.lookups[host]
	n.mu.Unlock()

	var addrs []netip.Addr
	for _, addr := range n.resolve(host, call) {
		addrs = append(addrs, netip.MustParseAddr(addr))
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

func (n *stubNet) dial(ctx context.Context, _, address string) (net.Conn, error) {
	n.mu.Lock()
	n.dialed = append(n.dialed, address)
	n.mu.Unlock()
	return n.connect(ctx, address)
}

func (n *stubNet) client(opts Options) *http.Client {
	opts.Lookup = n.lookup
	opts.Dial = n.dial
	return NewClient(opts)
}

func (n *stubNet) lookupCount(host string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lookups[host]
}

func (n *stubNet) dials() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.dialed...)
}

// answers resolves hosts from a fixed table.
func answers(table map[string][]string) func(string, int) []string {
	return func(host string, _ int) []string { return table[host] }
}

// connectTo connects every dial to the listener at server.
func connectTo(server string) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", server)
	}
}

func refuseConnect(t *testing.T) func(context.Context, string) (net.Conn, error) {
	return func(_ context.Context, address string) (net.Conn, error) {
		t.Errorf("dialed %s, want no connection", address)
		return nil, errors.New("unexpected dial")
	}
}

// countingServer counts the requests it receives.
type countingServer struct {
	*httptest.Server
	total atomic.Int64
}

func newCountingServer(t *testing.T, handler http.HandlerFunc) *countingServer {
	t.Helper()
	srv := &countingServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.total.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *countingServer) addr() string {
	return s.Listener.Addr().String()
}

func (s *countingServer) port() string {
	_, port, _ := net.SplitHostPort(s.addr())
	return port
}

func okHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}
}

func get(client *http.Client, rawURL string) (string, error) {
	resp, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ReadBody(resp, 0)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	return string(body), nil
}

func TestClientDefaultPolicyRefusesLoopbackServer(t *testing.T) {
	srv := newCountingServer(t, okHandler("internal"))
	client := NewClient(Options{})

	for _, rawURL := range []string{
		srv.URL + "/",
		"http://localhost:" + srv.port() + "/",
		"http://[::1]:" + srv.port() + "/",
		"http://[::ffff:127.0.0.1]:" + srv.port() + "/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			_, err := get(client, rawURL)
			if !errors.Is(err, ErrForbiddenAddress) {
				t.Fatalf("Get() error = %v, want ErrForbiddenAddress", err)
			}
		})
	}
	if got := srv.total.Load(); got != 0 {
		t.Fatalf("loopback server received %d requests, want 0", got)
	}
}

func TestClientRefusesNonPublicIPLiterals(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1/",
		"http://127.1.2.3:8080/",
		"http://0.0.0.0/",
		"http://10.0.0.1/",
		"http://100.64.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.0.1/",
		"http://[::1]/",
		"http://[::]/",
		"http://[::ffff:127.0.0.1]/",
		"http://[::ffff:169.254.169.254]/",
		"http://[fe80::1%25eth0]/",
		"http://[fd00:ec2::254]/",
		"http://[64:ff9b::a9fe:a9fe]/",
		"http://[2002:a00:1::1]/",
		"https://[ff02::1]/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			stub := newStubNet(answers(nil), refuseConnect(t))
			_, err := get(stub.client(Options{}), rawURL)
			if !errors.Is(err, ErrForbiddenAddress) {
				t.Fatalf("Get() error = %v, want ErrForbiddenAddress", err)
			}
			if len(stub.lookups) != 0 {
				t.Fatalf("IP literal was looked up: %v", stub.lookups)
			}
		})
	}
}

func TestClientRefusesUnsafeURLs(t *testing.T) {
	for _, rawURL := range []string{
		"ftp://remote.example/actor",
		"file:///etc/passwd",
		"gopher://remote.example/",
		"https://user:secret@remote.example/actor",
		"https://user@remote.example/actor",
		"https://remote.example:0/actor",
		"https://remote.example:65536/actor",
		"http:///actor",
	} {
		t.Run(rawURL, func(t *testing.T) {
			stub := newStubNet(answers(map[string][]string{"remote.example": {"93.184.215.14"}}), refuseConnect(t))
			_, err := get(stub.client(Options{}), rawURL)
			if !errors.Is(err, ErrUnsafeURL) {
				t.Fatalf("Get() error = %v, want ErrUnsafeURL", err)
			}
			if len(stub.lookups) != 0 {
				t.Fatalf("unsafe url was resolved: %v", stub.lookups)
			}
		})
	}
}

func TestClientRefusesNamesResolvingToNonPublicAddresses(t *testing.T) {
	tests := map[string][]string{
		"loopback.test":   {"127.0.0.1"},
		"private.test":    {"10.1.2.3"},
		"cgnat.test":      {"100.100.100.200"},
		"metadata.test":   {"169.254.169.254"},
		"v6loopback.test": {"::1"},
		"mapped.test":     {"::ffff:127.0.0.1"},
		"ula.test":        {"fd00:ec2::254"},
		"nat64.test":      {"64:ff9b::a00:1"},
		// Dual-stack and round-robin answers that hide one internal address
		// among public ones are refused as a whole.
		"dual-v6-internal.test": {"93.184.215.14", "::1"},
		"dual-v4-internal.test": {"2606:4700:4700::1111", "10.0.0.1"},
		"round-robin.test":      {"93.184.215.14", "93.184.215.15", "127.0.0.1"},
	}

	for host := range tests {
		t.Run(host, func(t *testing.T) {
			stub := newStubNet(answers(tests), refuseConnect(t))
			_, err := get(stub.client(Options{}), "https://"+host+"/users/alice")
			if !errors.Is(err, ErrForbiddenAddress) {
				t.Fatalf("Get() error = %v, want ErrForbiddenAddress", err)
			}
		})
	}
}

func TestClientDialsTheAddressItValidated(t *testing.T) {
	srv := newCountingServer(t, okHandler("actor"))
	// The first answer is public and every later one internal, as a
	// rebinding name server would answer.
	stub := newStubNet(func(host string, call int) []string {
		if call == 1 {
			return []string{"93.184.215.14"}
		}
		return []string{"127.0.0.1"}
	}, connectTo(srv.addr()))
	client := stub.client(Options{})

	body, err := get(client, "http://rebind.test/users/alice")
	if err != nil || body != "actor" {
		t.Fatalf("Get() = %q, %v, want actor", body, err)
	}
	if got := stub.lookupCount("rebind.test"); got != 1 {
		t.Fatalf("lookups = %d, want exactly one per connection", got)
	}
	if got := stub.dials(); len(got) != 1 || got[0] != "93.184.215.14:80" {
		t.Fatalf("dialed %v, want only the validated 93.184.215.14:80", got)
	}

	// A new connection resolves again, and the rebound answer is refused.
	client.CloseIdleConnections()
	if _, err := get(client, "http://rebind.test/users/alice"); !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("Get() after rebinding error = %v, want ErrForbiddenAddress", err)
	}
	if got := stub.dials(); len(got) != 1 {
		t.Fatalf("dialed %v after rebinding, want no new dial", got)
	}
	if got := srv.total.Load(); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

func TestClientRedirects(t *testing.T) {
	var srv *countingServer
	srv = newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Host == "start.test":
			http.Redirect(w, r, strings.ReplaceAll(r.URL.Query().Get("to"), "PORT", srv.port()), http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/hop/"):
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
			end, _ := strconv.Atoi(r.URL.Query().Get("end"))
			if n < end {
				http.Redirect(w, r, fmt.Sprintf("/hop/%d?end=%d", n+1, end), http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, "final")
		default:
			_, _ = io.WriteString(w, "actor at "+r.Host)
		}
	})
	newClient := func() *http.Client {
		return newStubNet(answers(map[string][]string{
			"start.test":    {"93.184.215.14"},
			"next.test":     {"2606:4700:4700::1111"},
			"chain.test":    {"93.184.215.16"},
			"internal.test": {"10.0.0.1"},
			"localhost":     {"127.0.0.1", "::1"},
		}), connectTo(srv.addr())).client(Options{})
	}

	t.Run("public to public", func(t *testing.T) {
		body, err := get(newClient(), "http://start.test/?to="+url.QueryEscape("http://next.test/users/alice"))
		if err != nil || body != "actor at next.test" {
			t.Fatalf("Get() = %q, %v, want actor at next.test", body, err)
		}
	})

	refused := []struct {
		to   string
		want error
	}{
		{"http://127.0.0.1:PORT/users/alice", ErrForbiddenAddress},
		{"http://localhost:PORT/users/alice", ErrForbiddenAddress},
		{"http://[::1]:PORT/users/alice", ErrForbiddenAddress},
		{"http://169.254.169.254/latest/meta-data/", ErrForbiddenAddress},
		{"http://internal.test/users/alice", ErrForbiddenAddress},
		{"file:///etc/passwd", ErrUnsafeURL},
		{"https://user:secret@next.test/users/alice", ErrUnsafeURL},
	}
	for _, tt := range refused {
		t.Run("to "+tt.to, func(t *testing.T) {
			before := srv.total.Load()
			_, err := get(newClient(), "http://start.test/?to="+url.QueryEscape(tt.to))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Get() error = %v, want %v", err, tt.want)
			}
			if got := srv.total.Load() - before; got != 1 {
				t.Fatalf("server received %d requests, want only the redirecting one", got)
			}
		})
	}

	t.Run("cap", func(t *testing.T) {
		body, err := get(newClient(), fmt.Sprintf("http://chain.test/hop/0?end=%d", DefaultMaxRedirects))
		if err != nil || body != "final" {
			t.Fatalf("Get() with %d redirects = %q, %v, want final", DefaultMaxRedirects, body, err)
		}
		_, err = get(newClient(), fmt.Sprintf("http://chain.test/hop/0?end=%d", DefaultMaxRedirects+1))
		if !errors.Is(err, ErrTooManyRedirects) {
			t.Fatalf("Get() with %d redirects error = %v, want ErrTooManyRedirects", DefaultMaxRedirects+1, err)
		}
	})
}

func TestClientRacesAddressFamilies(t *testing.T) {
	srv := newCountingServer(t, okHandler("actor"))
	resolve := answers(map[string][]string{"dual.test": {"2606:4700:4700::1111", "93.184.215.14"}})
	isIPv6 := func(address string) bool {
		return netip.MustParseAddrPort(address).Addr().Is6()
	}

	t.Run("refused IPv6 falls back at once", func(t *testing.T) {
		stub := newStubNet(resolve, func(ctx context.Context, address string) (net.Conn, error) {
			if isIPv6(address) {
				return nil, errors.New("connect: network is unreachable")
			}
			return connectTo(srv.addr())(ctx, address)
		})
		body, err := get(stub.client(Options{}), "http://dual.test/")
		if err != nil || body != "actor" {
			t.Fatalf("Get() = %q, %v, want actor", body, err)
		}
		if got := stub.dials(); len(got) != 2 || got[0] != "[2606:4700:4700::1111]:80" || got[1] != "93.184.215.14:80" {
			t.Fatalf("dialed %v, want IPv6 then IPv4", got)
		}
	})

	t.Run("hanging IPv6 does not stall IPv4", func(t *testing.T) {
		abandoned := make(chan struct{})
		stub := newStubNet(resolve, func(ctx context.Context, address string) (net.Conn, error) {
			if isIPv6(address) {
				<-ctx.Done()
				close(abandoned)
				return nil, ctx.Err()
			}
			return connectTo(srv.addr())(ctx, address)
		})
		start := time.Now()
		body, err := get(stub.client(Options{}), "http://dual.test/")
		if err != nil || body != "actor" {
			t.Fatalf("Get() = %q, %v, want actor", body, err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("Get() took %v, want the IPv4 fallback after %v", elapsed, fallbackDelay)
		}
		select {
		case <-abandoned:
		case <-time.After(2 * time.Second):
			t.Fatal("the hanging IPv6 dial was not cancelled")
		}
	})

	t.Run("both families fail", func(t *testing.T) {
		stub := newStubNet(resolve, func(_ context.Context, address string) (net.Conn, error) {
			return nil, fmt.Errorf("connect %s: connection refused", address)
		})
		_, err := get(stub.client(Options{}), "http://dual.test/")
		if err == nil || !strings.Contains(err.Error(), "2606:4700:4700::1111") {
			t.Fatalf("Get() error = %v, want the preferred family's error", err)
		}
		if got := stub.dials(); len(got) != 2 {
			t.Fatalf("dialed %v, want both families", got)
		}
	})
}

func TestCheckDialedAddr(t *testing.T) {
	for address, wantErr := range map[string]bool{
		"8.8.8.8:443":               false,
		"[2606:4700:4700::1111]:80": false,
		"127.0.0.1:80":              true,
		"10.0.0.1:443":              true,
		"[::1]:443":                 true,
		"[fe80::1%eth0]:80":         true,
		"not-an-address":            true,
	} {
		err := checkDialedAddr(context.Background(), "tcp", address, nil)
		if gotErr := err != nil; gotErr != wantErr {
			t.Errorf("checkDialedAddr(%q) error = %v, want error %v", address, err, wantErr)
		}
		if err != nil && !errors.Is(err, ErrForbiddenAddress) {
			t.Errorf("checkDialedAddr(%q) error = %v, want ErrForbiddenAddress", address, err)
		}
	}
}

// The default connect step re-checks the address even when handed one that
// never went through resolve.
func TestDefaultDialRefusesNonPublicAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{}, 1)
	go func() {
		if conn, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()

	conn, err := newDialer(nil, nil).dial(context.Background(), "tcp", ln.Addr().String())
	if err == nil {
		_ = conn.Close()
	}
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("dial() error = %v, want ErrForbiddenAddress", err)
	}
	select {
	case <-accepted:
		t.Fatal("listener accepted a connection")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNewClientSharesProductionTransport(t *testing.T) {
	a, b := NewClient(Options{}), NewClient(Options{Timeout: time.Minute})
	if a.Transport != b.Transport {
		t.Fatal("production clients do not share a transport")
	}
	if a.Timeout != DefaultTimeout || b.Timeout != time.Minute {
		t.Fatalf("timeouts = %v, %v, want %v, %v", a.Timeout, b.Timeout, DefaultTimeout, time.Minute)
	}
	custom := NewClient(Options{Lookup: net.DefaultResolver.LookupNetIP})
	if custom.Transport == a.Transport {
		t.Fatal("a client with a custom resolver shares the production transport")
	}
	// A proxy would be dialed instead of the destination, defeating the
	// address checks, so proxy settings from the environment must be ignored.
	if proxy := sharedTransport().base.Proxy; proxy != nil {
		t.Fatal("production transport uses a proxy")
	}
}

func TestReadBody(t *testing.T) {
	response := func(body string, contentLength int64) *http.Response {
		return &http.Response{Body: io.NopCloser(strings.NewReader(body)), ContentLength: contentLength}
	}

	tests := []struct {
		name    string
		resp    *http.Response
		limit   int64
		want    string
		wantErr error
	}{
		{name: "under limit", resp: response("abc", 3), limit: 4, want: "abc"},
		{name: "at limit", resp: response("abcd", 4), limit: 4, want: "abcd"},
		{name: "unknown length at limit", resp: response("abcd", -1), limit: 4, want: "abcd"},
		{name: "unknown length over limit", resp: response("abcde", -1), limit: 4, wantErr: ErrBodyTooLarge},
		{name: "declared length over limit", resp: response("", 5), limit: 4, wantErr: ErrBodyTooLarge},
		{name: "default limit", resp: response(strings.Repeat("a", DefaultMaxBodyBytes+1), -1), wantErr: ErrBodyTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := ReadBody(tt.resp, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadBody() error = %v, want %v", err, tt.wantErr)
			}
			if string(body) != tt.want {
				t.Fatalf("ReadBody() = %q, want %q", body, tt.want)
			}
		})
	}
}

// The limit applies to the decoded body, so a small compressed response
// cannot expand into a large one.
func TestReadBodyLimitsDecompressedSize(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(bytes.Repeat([]byte{'a'}, 64<<10))
	_ = zw.Close()

	srv := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	})
	client := newStubNet(answers(map[string][]string{"gzip.test": {"93.184.215.14"}}), connectTo(srv.addr())).client(Options{})

	resp, err := client.Get("http://gzip.test/")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := ReadBody(resp, 1<<10); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("ReadBody() of %d compressed bytes error = %v, want ErrBodyTooLarge", compressed.Len(), err)
	}
}

func TestIsRefused(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("fetch: %w", ErrForbiddenAddress), true},
		{fmt.Errorf("fetch: %w", ErrUnsafeURL), true},
		{fmt.Errorf("fetch: %w", ErrTooManyRedirects), false},
		{fmt.Errorf("fetch: %w", ErrBodyTooLarge), false},
		{errors.New("connection refused"), false},
		{nil, false},
	} {
		if got := IsRefused(tt.err); got != tt.want {
			t.Errorf("IsRefused(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
