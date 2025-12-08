// Package streaming provides Mastodon-compatible streaming API.
package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event types for streaming
const (
	EventUpdate               = "update"
	EventDelete               = "delete"
	EventNotification         = "notification"
	EventFiltersChanged       = "filters_changed"
	EventConversation         = "conversation"
	EventAnnouncement         = "announcement"
	EventAnnouncementReaction = "announcement.reaction"
	EventAnnouncementDelete   = "announcement.delete"
	EventStatusUpdate         = "status.update"
)

// Stream names
const (
	StreamUser             = "user"
	StreamUserNotification = "user:notification"
	StreamPublic           = "public"
	StreamPublicLocal      = "public:local"
	StreamPublicRemote     = "public:remote"
	StreamHashtag          = "hashtag"
	StreamHashtagLocal     = "hashtag:local"
	StreamList             = "list"
	StreamDirect           = "direct"
)

// Event represents a streaming event
type Event struct {
	Event   string   `json:"event"`
	Payload string   `json:"payload"`
	Stream  []string `json:"stream,omitempty"`
}

// Subscriber represents a connected client
type Subscriber struct {
	ID        string
	AccountID int64
	Streams   map[string]bool
	Events    chan *Event
	Done      chan struct{}
	mu        sync.RWMutex
}

// NewSubscriber creates a new subscriber
func NewSubscriber(id string, accountID int64) *Subscriber {
	return &Subscriber{
		ID:        id,
		AccountID: accountID,
		Streams:   make(map[string]bool),
		Events:    make(chan *Event, 100),
		Done:      make(chan struct{}),
	}
}

// Subscribe adds a stream subscription
func (s *Subscriber) Subscribe(stream string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Streams[stream] = true
}

// Unsubscribe removes a stream subscription
func (s *Subscriber) Unsubscribe(stream string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Streams, stream)
}

// IsSubscribed checks if subscribed to a stream
func (s *Subscriber) IsSubscribed(stream string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Streams[stream]
}

// Close closes the subscriber
func (s *Subscriber) Close() {
	close(s.Done)
}

// Hub manages all streaming connections
type Hub struct {
	subscribers map[string]*Subscriber
	mu          sync.RWMutex

	// Channels for events
	broadcast  chan *BroadcastMessage
	register   chan *Subscriber
	unregister chan *Subscriber
}

// BroadcastMessage is a message to broadcast
type BroadcastMessage struct {
	Streams   []string
	Event     *Event
	AccountID *int64 // Optional: target specific account
}

// NewHub creates a new streaming hub
func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[string]*Subscriber),
		broadcast:   make(chan *BroadcastMessage, 256),
		register:    make(chan *Subscriber),
		unregister:  make(chan *Subscriber),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case sub := <-h.register:
			h.mu.Lock()
			h.subscribers[sub.ID] = sub
			h.mu.Unlock()

		case sub := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.subscribers[sub.ID]; ok {
				delete(h.subscribers, sub.ID)
				close(sub.Events)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			for _, sub := range h.subscribers {
				// Check if this subscriber should receive the message
				if msg.AccountID != nil && sub.AccountID != *msg.AccountID {
					continue
				}

				// Check stream subscription
				subscribed := false
				for _, stream := range msg.Streams {
					if sub.IsSubscribed(stream) {
						subscribed = true
						break
					}
				}

				if subscribed {
					select {
					case sub.Events <- msg.Event:
					default:
						// Drop message if buffer is full
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Register adds a subscriber to the hub
func (h *Hub) Register(sub *Subscriber) {
	h.register <- sub
}

// Unregister removes a subscriber from the hub
func (h *Hub) Unregister(sub *Subscriber) {
	h.unregister <- sub
}

// Broadcast sends an event to all matching subscribers
func (h *Hub) Broadcast(streams []string, event *Event) {
	h.broadcast <- &BroadcastMessage{
		Streams: streams,
		Event:   event,
	}
}

// BroadcastToAccount sends an event to a specific account
func (h *Hub) BroadcastToAccount(accountID int64, streams []string, event *Event) {
	h.broadcast <- &BroadcastMessage{
		Streams:   streams,
		Event:     event,
		AccountID: &accountID,
	}
}

// PublishStatus publishes a status update
func (h *Hub) PublishStatus(status any, streams []string) {
	payload, _ := json.Marshal(status)
	h.Broadcast(streams, &Event{
		Event:   EventUpdate,
		Payload: string(payload),
		Stream:  streams,
	})
}

// PublishDelete publishes a status deletion
func (h *Hub) PublishDelete(statusID string, streams []string) {
	h.Broadcast(streams, &Event{
		Event:   EventDelete,
		Payload: statusID,
		Stream:  streams,
	})
}

// PublishNotification publishes a notification to a user
func (h *Hub) PublishNotification(accountID int64, notification any) {
	payload, _ := json.Marshal(notification)
	h.BroadcastToAccount(accountID, []string{StreamUser, StreamUserNotification}, &Event{
		Event:   EventNotification,
		Payload: string(payload),
		Stream:  []string{StreamUserNotification},
	})
}

// SSEHandler handles Server-Sent Events connections
type SSEHandler struct {
	hub *Hub
}

// NewSSEHandler creates a new SSE handler
func NewSSEHandler(hub *Hub) *SSEHandler {
	return &SSEHandler{hub: hub}
}

// ServeHTTP handles SSE requests
func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Get stream from query param
	stream := r.URL.Query().Get("stream")
	if stream == "" {
		stream = StreamPublic
	}

	// Get account ID from context (if authenticated)
	var accountID int64
	if id, ok := r.Context().Value("account_id").(int64); ok {
		accountID = id
	}

	// Create subscriber
	sub := NewSubscriber(fmt.Sprintf("sse-%d-%d", accountID, time.Now().UnixNano()), accountID)
	sub.Subscribe(stream)

	// For list and hashtag streams, add the specific tag/list
	if tag := r.URL.Query().Get("tag"); tag != "" {
		sub.Subscribe(StreamHashtag + ":" + tag)
	}
	if list := r.URL.Query().Get("list"); list != "" {
		sub.Subscribe(StreamList + ":" + list)
	}

	h.hub.Register(sub)
	defer h.hub.Unregister(sub)

	// Send initial connection event
	_, _ = fmt.Fprintf(w, ":ok\n\n")
	flusher.Flush()

	// Keep-alive ticker
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: %s\n", event.Event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event.Payload)
			flusher.Flush()

		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ":heartbeat\n\n")
			flusher.Flush()
		}
	}
}
