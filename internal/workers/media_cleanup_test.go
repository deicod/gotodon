package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

func TestMediaCleanupEnqueuerValidatesClient(t *testing.T) {
	enqueuer := NewMediaCleanupEnqueuer(nil)
	if err := enqueuer.Enqueue(context.Background()); !errors.Is(err, ErrMediaCleanupEnqueuerMissing) {
		t.Fatalf("missing client error = %v", err)
	}
}

func TestMediaCleanupHandlerCallsRunner(t *testing.T) {
	runner := &stubCleanupRunner{}
	handler := &MediaCleanupHandler{runner: runner}
	if err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeMediaCleanup, nil)); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner.calls = %d, want 1", runner.calls)
	}
}
