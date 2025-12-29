package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

type stubCleanupRunner struct {
	calls int
	err   error
}

func (s *stubCleanupRunner) Run(ctx context.Context) error {
	s.calls++
	return s.err
}

func TestStatusCleanupEnqueuerValidatesClient(t *testing.T) {
	enqueuer := NewStatusCleanupEnqueuer(nil)
	if err := enqueuer.Enqueue(context.Background()); !errors.Is(err, ErrStatusCleanupEnqueuerMissing) {
		t.Fatalf("missing client error = %v", err)
	}
}

func TestStatusCleanupHandlerCallsRunner(t *testing.T) {
	runner := &stubCleanupRunner{}
	handler := &StatusCleanupHandler{runner: runner}
	if err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeStatusCleanup, nil)); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner.calls = %d, want 1", runner.calls)
	}
}
