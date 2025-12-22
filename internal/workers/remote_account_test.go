package workers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/db"
)

type stubAccountResolver struct {
	calls int
	acct  string
	err   error
}

func (s *stubAccountResolver) ResolveAccount(ctx context.Context, acct string) (db.Account, error) {
	s.calls++
	s.acct = acct
	return db.Account{ID: 1}, s.err
}

func TestNewProcessRemoteAccountTask(t *testing.T) {
	task, err := NewProcessRemoteAccountTask("https://remote.example/users/alice")
	if err != nil {
		t.Fatalf("NewProcessRemoteAccountTask error = %v", err)
	}
	var payload ProcessRemoteAccountPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.ActorURI != "https://remote.example/users/alice" {
		t.Fatalf("payload.ActorURI = %q, want actor uri", payload.ActorURI)
	}
}

func TestNewProcessRemoteAccountTaskInvalid(t *testing.T) {
	if _, err := NewProcessRemoteAccountTask(""); !errors.Is(err, ErrRemoteAccountPayloadInvalid) {
		t.Fatalf("missing actor uri error = %v", err)
	}
	if _, err := NewProcessRemoteAccountTask("not-a-url"); !errors.Is(err, ErrRemoteAccountPayloadInvalid) {
		t.Fatalf("invalid actor uri error = %v", err)
	}
}

func TestRemoteAccountEnqueuerValidates(t *testing.T) {
	enqueuer := NewRemoteAccountEnqueuer(nil)
	if err := enqueuer.EnqueueRemoteAccount(context.Background(), "https://remote.example/users/alice"); !errors.Is(err, ErrRemoteAccountEnqueuerMissing) {
		t.Fatalf("nil client error = %v", err)
	}
}

func TestRemoteAccountHandlerSkipsInvalidPayload(t *testing.T) {
	handler := &RemoteAccountHandler{resolver: &stubAccountResolver{}}
	task := asynq.NewTask(TaskTypeProcessRemoteAccount, []byte("bad-json"))
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask invalid payload error = %v, want SkipRetry", err)
	}
}

func TestRemoteAccountHandlerSuccess(t *testing.T) {
	resolver := &stubAccountResolver{}
	handler := &RemoteAccountHandler{resolver: resolver}
	task := newProcessRemoteAccountTask(t, "https://remote.example/users/alice")
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver.calls = %d, want 1", resolver.calls)
	}
	if resolver.acct != "https://remote.example/users/alice" {
		t.Fatalf("resolver.acct = %q, want actor uri", resolver.acct)
	}
}

func newProcessRemoteAccountTask(t *testing.T, actorURI string) *asynq.Task {
	t.Helper()
	payload := ProcessRemoteAccountPayload{ActorURI: actorURI}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("payload marshal error = %v", err)
	}
	return asynq.NewTask(TaskTypeProcessRemoteAccount, body)
}
