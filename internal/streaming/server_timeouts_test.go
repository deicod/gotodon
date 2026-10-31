package streaming

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/web"
)

// Shrunken server timeouts: streams must stay open well past them.
const (
	testServerTimeout = 100 * time.Millisecond
	testStreamIdle    = 4 * testServerTimeout
)

// newTimeoutTestServer serves the streaming handlers behind the logger
// middleware on a server built by web.NewServer, mirroring cmd/serve.go with
// shrunken read, write and idle timeouts.
func newTimeoutTestServer(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()

	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	wsHandler := NewWSHandler(hub)
	sseHandler := NewSSEHandler(hub)
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/streaming/{stream...}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			wsHandler.ServeHTTP(w, r)
		} else {
			sseHandler.ServeHTTP(w, r)
		}
	}))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	srv := web.NewServer("", middleware.Logger(slog.New(slog.DiscardHandler))(mux))
	srv.ReadTimeout = testServerTimeout
	srv.WriteTimeout = testServerTimeout
	srv.IdleTimeout = testServerTimeout
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config = srv
	ts.Start()

	t.Cleanup(func() {
		ts.Close()
		cancel()
	})
	return hub, ts
}

func TestServerTimeouts_OrdinaryRequestsSucceed(t *testing.T) {
	_, ts := newTimeoutTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServerTimeouts_SSEStreamOutlivesServerTimeouts(t *testing.T) {
	hub, ts := newTimeoutTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/streaming/public", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	readLine := func(want string) {
		t.Helper()
		timeout := time.After(2 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed before %q", want)
				}
				if line == want {
					return
				}
			case <-timeout:
				t.Fatalf("timed out waiting for %q", want)
			}
		}
	}

	readLine(":ok")
	time.Sleep(testStreamIdle)
	hub.Broadcast([]string{StreamPublic}, &Event{Event: EventUpdate, Payload: "after-idle", Stream: []string{StreamPublic}})
	readLine("event: " + EventUpdate)
	readLine("data: after-idle")
}

func TestServerTimeouts_WebSocketOutlivesServerTimeouts(t *testing.T) {
	hub, ts := newTimeoutTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/streaming/public"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	time.Sleep(testStreamIdle)
	hub.Broadcast([]string{StreamPublic}, &Event{Event: EventUpdate, Payload: "after-idle", Stream: []string{StreamPublic}})

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var msg struct {
		Event   string `json:"event"`
		Payload string `json:"payload"`
	}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket: %v", err)
	}
	if msg.Event != EventUpdate || msg.Payload != "after-idle" {
		t.Fatalf("message = %+v, want update with payload after-idle", msg)
	}

	// The client can still send commands after the server timeouts elapsed.
	if err := conn.WriteJSON(WSMessage{Type: "subscribe", Stream: StreamPublicLocal}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	hub.Broadcast([]string{StreamPublicLocal}, &Event{Event: EventUpdate, Payload: "local", Stream: []string{StreamPublicLocal}})
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket after subscribe: %v", err)
	}
	if msg.Payload != "local" {
		t.Fatalf("payload = %q, want local", msg.Payload)
	}
}
