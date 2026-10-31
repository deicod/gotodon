package web

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewServer_SetsDefensiveLimits(t *testing.T) {
	handler := http.NewServeMux()
	srv := NewServer("127.0.0.1:3000", handler)

	if srv.Addr != "127.0.0.1:3000" {
		t.Fatalf("Addr = %q, want %q", srv.Addr, "127.0.0.1:3000")
	}
	if srv.Handler != handler {
		t.Fatal("Handler was not set")
	}

	tests := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout, 10 * time.Second},
		{"ReadTimeout", srv.ReadTimeout, 60 * time.Second},
		{"WriteTimeout", srv.WriteTimeout, 60 * time.Second},
		{"IdleTimeout", srv.IdleTimeout, 120 * time.Second},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
	if srv.MaxHeaderBytes != 64<<10 {
		t.Errorf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, 64<<10)
	}
	if uploadTimeout <= srv.ReadTimeout || uploadTimeout <= srv.WriteTimeout {
		t.Errorf("uploadTimeout = %v, want longer than the ordinary read and write timeouts", uploadTimeout)
	}
}

// startServer serves srv on a loopback listener for the duration of the test.
func startServer(t *testing.T, srv *http.Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config = srv
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

func TestNewServer_ClosesConnectionsWithIncompleteHeaders(t *testing.T) {
	srv := NewServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.ReadHeaderTimeout = 100 * time.Millisecond
	ts := startServer(t, srv)

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a request line and one header, then stall before the blank line.
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n"); err != nil {
		t.Fatalf("write partial headers: %v", err)
	}

	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("server kept the connection open past ReadHeaderTimeout")
	}
	if err == nil {
		t.Fatal("expected the server to close the connection, got data")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("connection closed after %v, want shortly after ReadHeaderTimeout", elapsed)
	}
}

func TestNewServer_RejectsOversizedHeaders(t *testing.T) {
	srv := NewServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	ts := startServer(t, srv)

	tests := []struct {
		name       string
		headerSize int
		wantStatus int
	}{
		{"within limit", 32 << 10, http.StatusNoContent},
		{"over limit", 128 << 10, http.StatusRequestHeaderFieldsTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("X-Padding", strings.Repeat("a", tt.headerSize))
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// slowBody streams chunks with a pause before each one, like a client on a
// slow link.
func slowBody(chunks int, chunk []byte, pause time.Duration) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		for range chunks {
			time.Sleep(pause)
			if _, err := pw.Write(chunk); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	return pr
}

func TestExtendUploadDeadlines(t *testing.T) {
	const (
		chunks    = 10
		chunkSize = 1024
	)
	tests := []struct {
		name    string
		extend  bool
		wantErr bool
	}{
		{name: "slow body outlives ordinary timeouts", extend: true},
		{name: "slow body without extension is cut off", extend: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.extend {
					ExtendUploadDeadlines(w)
				}
				n, err := io.Copy(io.Discard, r.Body)
				if err != nil {
					w.WriteHeader(http.StatusRequestTimeout)
					return
				}
				_, _ = io.WriteString(w, strconv.FormatInt(n, 10))
			}))
			srv.ReadTimeout = 100 * time.Millisecond
			srv.WriteTimeout = 100 * time.Millisecond
			ts := startServer(t, srv)

			body := slowBody(chunks, make([]byte, chunkSize), 50*time.Millisecond)
			req, err := http.NewRequest(http.MethodPost, ts.URL, body)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := ts.Client().Do(req)
			if tt.wantErr {
				// The read deadline fails the body and the expired write
				// deadline prevents any reply, so the client sees an error
				// or, at best, no successful response.
				if err == nil {
					defer func() { _ = resp.Body.Close() }()
					if resp.StatusCode == http.StatusOK {
						t.Fatal("slow body completed despite the ordinary read timeout")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			if want := strconv.Itoa(chunks * chunkSize); string(got) != want {
				t.Fatalf("server read %s bytes, want %s", got, want)
			}
		})
	}
}

func TestExtendUploadDeadlines_IgnoresWritersWithoutDeadlines(t *testing.T) {
	rec := httptest.NewRecorder()
	ExtendUploadDeadlines(rec)
	JSON(rec, http.StatusOK, map[string]string{"status": "ok"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %q, want JSON response", rec.Body.String())
	}
}
