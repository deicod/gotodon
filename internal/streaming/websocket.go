// Package streaming provides WebSocket streaming support.
package streaming

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket upgrader configuration
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

// WSMessage is a WebSocket message format
type WSMessage struct {
	Type   string `json:"type"`
	Stream string `json:"stream,omitempty"`
	List   string `json:"list,omitempty"`
	Tag    string `json:"tag,omitempty"`
}

// WSHandler handles WebSocket connections
type WSHandler struct {
	hub *Hub
}

// NewWSHandler creates a new WebSocket handler
func NewWSHandler(hub *Hub) *WSHandler {
	return &WSHandler{hub: hub}
}

// ServeHTTP handles WebSocket upgrade and messaging
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	// Get account ID from context
	accountID := accountIDFromContext(r.Context())
	accessTokenID := accessTokenIDFromContext(r.Context())

	// Create subscriber
	sub := NewSubscriber(fmt.Sprintf("ws-%d-%d", accountID, time.Now().UnixNano()), accountID)
	h.hub.Register(sub)
	defer h.hub.Unregister(sub)

	if initialStreams, err := resolveStreamSubscriptions(r.URL.Path, r.URL.Query(), ""); err != nil {
		status, message := streamErrorDetails(err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"error":%q,"status":%d}`, message, status)))
	} else {
		for _, stream := range expandAccountScopedStreams(initialStreams, accountID) {
			sub.Subscribe(stream)
		}
	}
	if accessTokenID > 0 {
		sub.Subscribe(AccessTokenStreamKey(accessTokenID))
	}

	// Set up ping/pong
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Read goroutine - handles subscribe/unsubscribe commands
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msg WSMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			switch msg.Type {
			case "subscribe":
				stream := h.resolveStream(msg)
				for _, key := range expandAccountScopedStreams([]string{stream}, accountID) {
					sub.Subscribe(key)
				}
			case "unsubscribe":
				stream := h.resolveStream(msg)
				for _, key := range expandAccountScopedStreams([]string{stream}, accountID) {
					sub.Unsubscribe(key)
				}
			}
		}
	}()

	// Write goroutine - sends events
	wg.Add(1)
	go func() {
		defer wg.Done()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return

			case event, ok := <-sub.Events:
				if !ok {
					return
				}
				if event.Event == EventKill {
					_ = conn.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(streamWriteTimeout))
					_ = conn.Close()
					return
				}
				msg := map[string]any{
					"event":   event.Event,
					"payload": event.Payload,
					"stream":  event.Stream,
				}
				data, _ := json.Marshal(msg)
				_ = conn.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}

			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	wg.Wait()
}

func (h *WSHandler) resolveStream(msg WSMessage) string {
	switch msg.Stream {
	case StreamHashtag, StreamHashtagLocal:
		if msg.Tag != "" {
			return msg.Stream + ":" + msg.Tag
		}
		return msg.Stream
	case StreamList:
		if msg.List != "" {
			return msg.Stream + ":" + msg.List
		}
		return msg.Stream
	default:
		return msg.Stream
	}
}
