package workers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type stubRemoteStatusFetcher struct {
	fetchCalls  int
	threadCalls int
	lastURI     string
	lastStatus  db.Status
	fetchErr    error
	threadErr   error
	returned    db.Status
}

func (s *stubRemoteStatusFetcher) FetchStatus(ctx context.Context, uri string) (db.Status, error) {
	s.fetchCalls++
	s.lastURI = uri
	if s.fetchErr != nil {
		return db.Status{}, s.fetchErr
	}
	if s.returned.ID != 0 {
		return s.returned, nil
	}
	return db.Status{ID: 1, Uri: pgtype.Text{String: uri, Valid: true}}, nil
}

func (s *stubRemoteStatusFetcher) FetchThread(ctx context.Context, status db.Status) error {
	s.threadCalls++
	s.lastStatus = status
	return s.threadErr
}

type stubStatusLookup struct {
	status db.Status
	err    error
	calls  int
}

func (s *stubStatusLookup) GetStatusByID(ctx context.Context, id int64) (db.Status, error) {
	s.calls++
	return s.status, s.err
}

func TestNewFetchRemoteStatusTask(t *testing.T) {
	task, err := NewFetchRemoteStatusTask("https://remote.example/status/1")
	if err != nil {
		t.Fatalf("NewFetchRemoteStatusTask error = %v", err)
	}
	var payload FetchRemoteStatusPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.StatusURI != "https://remote.example/status/1" {
		t.Fatalf("payload.StatusURI = %q, want status uri", payload.StatusURI)
	}
}

func TestNewFetchRemoteStatusTaskInvalid(t *testing.T) {
	if _, err := NewFetchRemoteStatusTask(""); !errors.Is(err, ErrRemoteStatusPayloadInvalid) {
		t.Fatalf("missing status uri error = %v", err)
	}
	if _, err := NewFetchRemoteStatusTask("nope"); !errors.Is(err, ErrRemoteStatusPayloadInvalid) {
		t.Fatalf("invalid status uri error = %v", err)
	}
}

func TestNewFetchRemoteThreadTask(t *testing.T) {
	task, err := NewFetchRemoteThreadTask(42)
	if err != nil {
		t.Fatalf("NewFetchRemoteThreadTask error = %v", err)
	}
	var payload FetchRemoteStatusPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.StatusID != 42 || !payload.FetchThread {
		t.Fatalf("payload = %#v, want status_id 42 with fetch_thread", payload)
	}
}

func TestRemoteStatusEnqueuerValidates(t *testing.T) {
	enqueuer := NewRemoteStatusEnqueuer(nil)
	if err := enqueuer.EnqueueFetchStatus(context.Background(), "https://remote.example/status/1"); !errors.Is(err, ErrRemoteStatusEnqueuerMissing) {
		t.Fatalf("nil client error = %v", err)
	}
}

func TestRemoteStatusHandlerFetchStatus(t *testing.T) {
	fetcher := &stubRemoteStatusFetcher{}
	lookup := &stubStatusLookup{}
	handler := &RemoteStatusHandler{fetcher: fetcher, lookup: lookup}

	task, err := newFetchRemoteStatusTask(FetchRemoteStatusPayload{StatusURI: "https://remote.example/status/1"})
	if err != nil {
		t.Fatalf("newFetchRemoteStatusTask error = %v", err)
	}
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if fetcher.fetchCalls != 1 {
		t.Fatalf("fetcher.fetchCalls = %d, want 1", fetcher.fetchCalls)
	}
	if fetcher.lastURI != "https://remote.example/status/1" {
		t.Fatalf("fetcher.lastURI = %q, want status uri", fetcher.lastURI)
	}
}

func TestRemoteStatusHandlerFetchThread(t *testing.T) {
	status := db.Status{
		ID:    7,
		Uri:   pgtype.Text{String: "https://remote.example/status/7", Valid: true},
		Local: pgtype.Bool{Bool: false, Valid: true},
	}
	fetcher := &stubRemoteStatusFetcher{}
	lookup := &stubStatusLookup{status: status}
	handler := &RemoteStatusHandler{fetcher: fetcher, lookup: lookup}

	task, err := newFetchRemoteStatusTask(FetchRemoteStatusPayload{StatusID: 7, FetchThread: true})
	if err != nil {
		t.Fatalf("newFetchRemoteStatusTask error = %v", err)
	}
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup.calls = %d, want 1", lookup.calls)
	}
	if fetcher.threadCalls != 1 {
		t.Fatalf("fetcher.threadCalls = %d, want 1", fetcher.threadCalls)
	}
	if fetcher.lastStatus.ID != 7 {
		t.Fatalf("fetcher.lastStatus.ID = %d, want 7", fetcher.lastStatus.ID)
	}
}

func TestRemoteStatusHandlerSkipsMissingStatus(t *testing.T) {
	fetcher := &stubRemoteStatusFetcher{}
	lookup := &stubStatusLookup{err: pgx.ErrNoRows}
	handler := &RemoteStatusHandler{fetcher: fetcher, lookup: lookup}

	task, err := newFetchRemoteStatusTask(FetchRemoteStatusPayload{StatusID: 55, FetchThread: true})
	if err != nil {
		t.Fatalf("newFetchRemoteStatusTask error = %v", err)
	}
	err = handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask missing status error = %v, want SkipRetry", err)
	}
}
