package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type wsTestEnv struct {
	hub    *Hub
	server *httptest.Server
	cancel context.CancelFunc
}

func newWSTestEnv(t *testing.T) wsTestEnv {
	t.Helper()

	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	mux := http.NewServeMux()
	wsHandler := NewWSHandler(hub)
	mux.Handle("/api/v1/streaming", wsHandler)
	mux.Handle("/api/v1/streaming/", wsHandler)

	server := httptest.NewServer(mux)

	t.Cleanup(func() {
		server.Close()
		cancel()
	})

	return wsTestEnv{hub: hub, server: server, cancel: cancel}
}

func dialWebSocket(t *testing.T, serverURL, path string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + path
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readWSJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, message, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket: %v", err)
	}
	if err := json.Unmarshal(message, v); err != nil {
		t.Fatalf("unmarshal websocket message: %v", err)
	}
}

func TestWSHandler_InitialQuerySubscription(t *testing.T) {
	env := newWSTestEnv(t)

	conn := dialWebSocket(t, env.server.URL, "/api/v1/streaming?stream=public")
	defer func() { _ = conn.Close() }()

	time.Sleep(10 * time.Millisecond)
	env.hub.Broadcast([]string{StreamPublic}, &Event{Event: EventUpdate, Payload: "hello", Stream: []string{StreamPublic}})

	var payload struct {
		Event   string   `json:"event"`
		Payload string   `json:"payload"`
		Stream  []string `json:"stream"`
	}
	readWSJSON(t, conn, &payload)

	if payload.Event != EventUpdate {
		t.Fatalf("event = %q, want %q", payload.Event, EventUpdate)
	}
	if payload.Payload != "hello" {
		t.Fatalf("payload = %q, want %q", payload.Payload, "hello")
	}
	if !reflect.DeepEqual(payload.Stream, []string{StreamPublic}) {
		t.Fatalf("stream = %#v, want %#v", payload.Stream, []string{StreamPublic})
	}
}

func TestWSHandler_InitialPathSubscription(t *testing.T) {
	env := newWSTestEnv(t)

	conn := dialWebSocket(t, env.server.URL, "/api/v1/streaming/public")
	defer func() { _ = conn.Close() }()

	time.Sleep(10 * time.Millisecond)
	env.hub.Broadcast([]string{StreamPublic}, &Event{Event: EventUpdate, Payload: "path", Stream: []string{StreamPublic}})

	var payload struct {
		Event   string   `json:"event"`
		Payload string   `json:"payload"`
		Stream  []string `json:"stream"`
	}
	readWSJSON(t, conn, &payload)

	if payload.Event != EventUpdate {
		t.Fatalf("event = %q, want %q", payload.Event, EventUpdate)
	}
	if payload.Payload != "path" {
		t.Fatalf("payload = %q, want %q", payload.Payload, "path")
	}
	if !reflect.DeepEqual(payload.Stream, []string{StreamPublic}) {
		t.Fatalf("stream = %#v, want %#v", payload.Stream, []string{StreamPublic})
	}
}

func TestWSHandler_InitialPathHashtagSubscription(t *testing.T) {
	env := newWSTestEnv(t)

	conn := dialWebSocket(t, env.server.URL, "/api/v1/streaming/hashtag?tag=go")
	defer func() { _ = conn.Close() }()

	time.Sleep(10 * time.Millisecond)
	env.hub.Broadcast([]string{StreamHashtag + ":go"}, &Event{Event: EventUpdate, Payload: "tagged", Stream: []string{StreamHashtag + ":go"}})

	var payload struct {
		Event   string   `json:"event"`
		Payload string   `json:"payload"`
		Stream  []string `json:"stream"`
	}
	readWSJSON(t, conn, &payload)

	if payload.Event != EventUpdate {
		t.Fatalf("event = %q, want %q", payload.Event, EventUpdate)
	}
	if payload.Payload != "tagged" {
		t.Fatalf("payload = %q, want %q", payload.Payload, "tagged")
	}
	if !reflect.DeepEqual(payload.Stream, []string{StreamHashtag, "go"}) {
		t.Fatalf("stream = %#v, want %#v", payload.Stream, []string{StreamHashtag, "go"})
	}
}

func TestWSHandler_SubscribeUnsubscribe(t *testing.T) {
	env := newWSTestEnv(t)

	conn := dialWebSocket(t, env.server.URL, "/api/v1/streaming")
	defer func() { _ = conn.Close() }()

	subscribe := map[string]string{
		"type":   "subscribe",
		"stream": StreamHashtag,
		"tag":    "go",
	}
	message, _ := json.Marshal(subscribe)
	if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	env.hub.Broadcast([]string{StreamHashtag + ":go"}, &Event{Event: EventUpdate, Payload: "tagged"})

	var payload struct {
		Event   string   `json:"event"`
		Payload string   `json:"payload"`
		Stream  []string `json:"stream"`
	}
	readWSJSON(t, conn, &payload)
	if payload.Payload != "tagged" {
		t.Fatalf("payload = %q, want %q", payload.Payload, "tagged")
	}
	if !reflect.DeepEqual(payload.Stream, []string{StreamHashtag, "go"}) {
		t.Fatalf("stream = %#v, want %#v", payload.Stream, []string{StreamHashtag, "go"})
	}

	unsubscribe := map[string]string{
		"type":   "unsubscribe",
		"stream": StreamHashtag,
		"tag":    "go",
	}
	message, _ = json.Marshal(unsubscribe)
	if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
		t.Fatalf("write unsubscribe: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	env.hub.Broadcast([]string{StreamHashtag + ":go"}, &Event{Event: EventUpdate, Payload: "ignored"})

	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected no message after unsubscribe")
	}
}

func TestWSHandler_InvalidQuery(t *testing.T) {
	env := newWSTestEnv(t)

	values := url.Values{"stream": []string{StreamHashtag}}
	conn := dialWebSocket(t, env.server.URL, "/api/v1/streaming?"+values.Encode())
	defer func() { _ = conn.Close() }()

	var payload struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	}
	readWSJSON(t, conn, &payload)

	if payload.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", payload.Status, http.StatusBadRequest)
	}
	if payload.Error != "Missing tag name parameter" {
		t.Fatalf("error = %q, want missing tag error", payload.Error)
	}
}
