package streaming

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEHandler_MissingTag(t *testing.T) {
	handler := NewSSEHandler(NewHub())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/streaming/hashtag", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Missing tag name parameter") {
		t.Fatalf("response = %q, want missing tag error", rec.Body.String())
	}
}

func TestSSEHandler_PathStreamEvent(t *testing.T) {
	hub := NewHub()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	go hub.Run(hubCtx)

	handler := NewSSEHandler(hub)
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/streaming/public", nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	hub.Broadcast([]string{StreamPublic}, &Event{Event: EventUpdate, Payload: "payload", Stream: []string{StreamPublic}})
	time.Sleep(20 * time.Millisecond)

	reqCancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: "+EventUpdate) {
		t.Fatalf("response missing event: %q", body)
	}
	if !strings.Contains(body, "data: payload") {
		t.Fatalf("response missing payload: %q", body)
	}
}
