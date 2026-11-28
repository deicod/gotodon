package services

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// blockingDeleter records deletions and holds each until release is
// closed.
type blockingDeleter struct {
	mu      sync.Mutex
	calls   []int64
	started chan struct{}
	release chan struct{}
	err     error
}

func (d *blockingDeleter) Delete(ctx context.Context, accountID int64, opts AccountDeletionOptions) error {
	d.mu.Lock()
	d.calls = append(d.calls, accountID)
	d.mu.Unlock()
	d.started <- struct{}{}
	<-d.release
	return d.err
}

func TestInProcessAccountDeletion(t *testing.T) {
	deleter := &blockingDeleter{started: make(chan struct{}, 4), release: make(chan struct{})}
	var logs bytes.Buffer
	runner := &InProcessAccountDeletion{Deleter: deleter, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	ctx := context.Background()

	if err := runner.ScheduleAccountDeletion(ctx, 1, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("ScheduleAccountDeletion: %v", err)
	}
	<-deleter.started
	// While account 1 is being deleted, asking again starts nothing; another
	// account's deletion starts.
	if err := runner.ScheduleAccountDeletion(ctx, 1, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("ScheduleAccountDeletion again: %v", err)
	}
	if err := runner.ScheduleAccountDeletion(ctx, 2, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("ScheduleAccountDeletion of account 2: %v", err)
	}
	<-deleter.started
	deleter.err = errors.New("database down")
	close(deleter.release)
	runner.Wait()

	if len(deleter.calls) != 2 {
		t.Fatalf("deletions = %v, want accounts 1 and 2 once each", deleter.calls)
	}
	if !strings.Contains(logs.String(), "Account deletion failed") {
		t.Fatalf("failure not logged: %q", logs.String())
	}

	// Once it has ended, the deletion can be asked for again, and goes on.
	deleter.release = make(chan struct{})
	close(deleter.release)
	if err := runner.ScheduleAccountDeletion(ctx, 1, AdminAccountDeletionOptions()); err != nil {
		t.Fatalf("ScheduleAccountDeletion after failure: %v", err)
	}
	<-deleter.started
	runner.Wait()
	if len(deleter.calls) != 3 {
		t.Fatalf("deletions = %v, want account 1 run again", deleter.calls)
	}

	if err := (&InProcessAccountDeletion{}).ScheduleAccountDeletion(ctx, 1, AdminAccountDeletionOptions()); !errors.Is(err, ErrAccountDeletionMissingDeps) {
		t.Fatalf("no deleter: err = %v", err)
	}
}
