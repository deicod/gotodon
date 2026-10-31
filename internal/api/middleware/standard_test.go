package middleware

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogger_PassesFlushThrough(t *testing.T) {
	handler := Logger(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("logger response writer does not implement http.Flusher")
		}
		_, _ = io.WriteString(w, "chunk")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through logger: %v", err)
		}
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !rec.Flushed {
		t.Fatal("expected the underlying writer to be flushed")
	}
}

type noFlushWriter struct {
	header http.Header
}

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(int)             {}

func TestLogger_ReportsUnsupportedFlush(t *testing.T) {
	handler := Logger(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("flush error = %v, want %v", err, http.ErrNotSupported)
		}
	}))

	handler.ServeHTTP(&noFlushWriter{header: http.Header{}}, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestLogger_ExposesConnectionDeadlines(t *testing.T) {
	errs := make(chan error, 2)
	handler := Logger(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		errs <- rc.SetReadDeadline(time.Now().Add(time.Minute))
		errs <- rc.SetWriteDeadline(time.Now().Add(time.Minute))
		w.WriteHeader(http.StatusNoContent)
	}))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()

	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("set deadline through logger: %v", err)
		}
	}
}

func TestLogger_PassesHijackThrough(t *testing.T) {
	var logs bytes.Buffer
	logged := make(chan struct{})
	logger := Logger(slog.New(slog.NewTextHandler(&logs, nil)))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(logged)
		logger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("logger response writer does not implement http.Hijacker")
				return
			}
			conn, brw, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack through logger: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: close\r\n\r\nhijacked")
			_ = brw.Flush()
		})).ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read hijacked response: %v", err)
	}
	if !strings.HasSuffix(string(got), "hijacked") {
		t.Fatalf("response = %q, want hijacked connection output", got)
	}

	<-logged
	if !strings.Contains(logs.String(), "status=101") {
		t.Fatalf("log = %q, want status=101 for hijacked request", logs.String())
	}
}
