package streaming

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
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

func TestSSEHandler_TokenKillClosesConnection(t *testing.T) {
	hub := NewHub()
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	go hub.Run(hubCtx)

	handler := NewSSEHandler(hub)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyTokenID, int64(42))
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/streaming/public", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	time.Sleep(20 * time.Millisecond)
	hub.Broadcast([]string{AccessTokenStreamKey(42)}, &Event{Event: EventKill, Payload: ""})

	_, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
}
