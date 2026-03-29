package streaming

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestNewSubscriber(t *testing.T) {
	sub := NewSubscriber("test-1", 12345)

	if sub.ID != "test-1" {
		t.Errorf("ID = %q, want %q", sub.ID, "test-1")
	}

	if sub.AccountID != 12345 {
		t.Errorf("AccountID = %d, want 12345", sub.AccountID)
	}
}

func TestSubscriber_Subscribe(t *testing.T) {
	sub := NewSubscriber("test-1", 12345)

	sub.Subscribe(StreamPublic)
	sub.Subscribe(StreamUser)

	if !sub.IsSubscribed(StreamPublic) {
		t.Error("Should be subscribed to public")
	}

	if !sub.IsSubscribed(StreamUser) {
		t.Error("Should be subscribed to user")
	}

	if sub.IsSubscribed(StreamDirect) {
		t.Error("Should not be subscribed to direct")
	}
}

func TestSubscriber_Unsubscribe(t *testing.T) {
	sub := NewSubscriber("test-1", 12345)

	sub.Subscribe(StreamPublic)
	sub.Subscribe(StreamUser)

	sub.Unsubscribe(StreamPublic)

	if sub.IsSubscribed(StreamPublic) {
		t.Error("Should not be subscribed to public after unsubscribe")
	}

	if !sub.IsSubscribed(StreamUser) {
		t.Error("Should still be subscribed to user")
	}
}

func TestHub_RegisterUnregister(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	sub := NewSubscriber("test-1", 12345)
	hub.Register(sub)

	// Give time for registration
	time.Sleep(10 * time.Millisecond)

	hub.Unregister(sub)

	// Give time for unregistration
	time.Sleep(10 * time.Millisecond)
}

func TestHub_Broadcast(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	sub := NewSubscriber("test-1", 12345)
	sub.Subscribe(StreamPublic)
	hub.Register(sub)

	// Give time for registration
	time.Sleep(10 * time.Millisecond)

	// Broadcast
	hub.Broadcast([]string{StreamPublic}, &Event{
		Event:   EventUpdate,
		Payload: "test payload",
	})

	// Should receive event
	select {
	case event := <-sub.Events:
		if event.Event != EventUpdate {
			t.Errorf("Event = %q, want %q", event.Event, EventUpdate)
		}
		if event.Payload != "test payload" {
			t.Errorf("Payload = %q, want %q", event.Payload, "test payload")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Should have received event")
	}
}

func TestHub_BroadcastToAccount(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	sub1 := NewSubscriber("test-1", 12345)
	sub1.Subscribe(StreamUser)
	hub.Register(sub1)

	sub2 := NewSubscriber("test-2", 67890)
	sub2.Subscribe(StreamUser)
	hub.Register(sub2)

	time.Sleep(10 * time.Millisecond)

	// Broadcast to specific account
	hub.BroadcastToAccount(12345, []string{StreamUser}, &Event{
		Event:   EventNotification,
		Payload: "notification for 12345",
	})

	// sub1 should receive
	select {
	case event := <-sub1.Events:
		if event.Payload != "notification for 12345" {
			t.Errorf("Wrong payload: %s", event.Payload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("sub1 should receive event")
	}

	// sub2 should not receive
	select {
	case <-sub2.Events:
		t.Error("sub2 should not receive event")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}
}

func TestEventConstants(t *testing.T) {
	// Verify event constants match Mastodon API
	if EventUpdate != "update" {
		t.Errorf("EventUpdate = %q, want %q", EventUpdate, "update")
	}
	if EventDelete != "delete" {
		t.Errorf("EventDelete = %q, want %q", EventDelete, "delete")
	}
	if EventNotification != "notification" {
		t.Errorf("EventNotification = %q, want %q", EventNotification, "notification")
	}
	if EventKill != "kill" {
		t.Errorf("EventKill = %q, want %q", EventKill, "kill")
	}
}

func TestStreamConstants(t *testing.T) {
	// Verify stream constants match Mastodon API
	if StreamUser != "user" {
		t.Errorf("StreamUser = %q, want %q", StreamUser, "user")
	}
	if StreamPublic != "public" {
		t.Errorf("StreamPublic = %q, want %q", StreamPublic, "public")
	}
	if StreamDirect != "direct" {
		t.Errorf("StreamDirect = %q, want %q", StreamDirect, "direct")
	}
	if StreamAccessToken != "access_token" {
		t.Errorf("StreamAccessToken = %q, want %q", StreamAccessToken, "access_token")
	}
}

func TestStreamPayloadForKey(t *testing.T) {
	tests := []struct {
		key  string
		want []string
	}{
		{StreamPublic, []string{StreamPublic}},
		{StreamPublicLocal, []string{StreamPublicLocal}},
		{"hashtag:golang", []string{StreamHashtag, "golang"}},
		{"hashtag:local:go", []string{StreamHashtagLocal, "go"}},
		{"list:42", []string{StreamList, "42"}},
		{"user:123", []string{StreamUser}},
		{"user:notification:123", []string{StreamUserNotification}},
		{"direct:9", []string{StreamDirect}},
	}

	for _, tt := range tests {
		if got := streamPayloadForKey(tt.key); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("streamPayloadForKey(%q) = %#v, want %#v", tt.key, got, tt.want)
		}
	}
}

func TestExpandAccountScopedStreams(t *testing.T) {
	streams := []string{StreamUser, StreamUserNotification, StreamDirect, StreamPublic}
	got := expandAccountScopedStreams(streams, 42)
	want := []string{StreamUser, UserStreamKey(42), UserNotificationStreamKey(42), DirectStreamKey(42), StreamPublic}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandAccountScopedStreams = %#v, want %#v", got, want)
	}

	got = expandAccountScopedStreams(streams, 0)
	if !reflect.DeepEqual(got, streams) {
		t.Fatalf("expandAccountScopedStreams (no account) = %#v, want %#v", got, streams)
	}
}

func TestAccessTokenStreamKey(t *testing.T) {
	if got := AccessTokenStreamKey(42); got != "access_token:42" {
		t.Fatalf("AccessTokenStreamKey = %q, want %q", got, "access_token:42")
	}
}
