package v1

import (
	"context"
	"reflect"
	"testing"

	"github.com/deicod/gotodon/internal/streaming"
)

func (s *stubEventPublisher) PublishStatusToRedis(ctx context.Context, status any, streams []string) error {
	return nil
}

func (s *stubEventPublisher) PublishDeleteToRedis(ctx context.Context, statusID string, streams []string) error {
	return nil
}

func (s *stubEventPublisher) PublishNotificationToRedis(ctx context.Context, accountID int64, notification any) error {
	return nil
}

func TestStatusStreamsForPublish_Public(t *testing.T) {
	streams := statusStreamsForPublish(42, VisibilityPublic, []string{"go"})
	want := []string{streaming.UserStreamKey(42), streaming.StreamPublic, streaming.StreamPublicLocal, "hashtag:go"}
	if !reflect.DeepEqual(streams, want) {
		t.Fatalf("streams = %#v, want %#v", streams, want)
	}
}

func TestStatusesHandler_PublishStatusUpdate(t *testing.T) {
	publisher := &stubEventPublisher{}
	h := NewStatusesHandler(nil, nil, publisher, nil, nil)

	streams := []string{streaming.UserStreamKey(1)}
	h.publishStatusUpdate(context.Background(), Status{ID: "1"}, streams)

	if len(publisher.events) != 1 {
		t.Fatalf("events = %d, want 1", len(publisher.events))
	}
	event := publisher.events[0]
	if event.eventType != streaming.EventStatusUpdate {
		t.Fatalf("eventType = %q, want %q", event.eventType, streaming.EventStatusUpdate)
	}
	if !reflect.DeepEqual(event.streams, streams) {
		t.Fatalf("streams = %#v, want %#v", event.streams, streams)
	}
	if _, ok := event.payload.(Status); !ok {
		t.Fatalf("payload type = %T, want Status", event.payload)
	}
}
