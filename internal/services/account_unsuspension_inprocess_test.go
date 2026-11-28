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

// syncUnsuspender records unsuspensions and fails each with err.
type syncUnsuspender struct {
	mu    sync.Mutex
	calls []int64
	err   error
}

func (u *syncUnsuspender) Unsuspend(ctx context.Context, accountID int64) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, accountID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return u.err
}

func TestInProcessAccountUnsuspension(t *testing.T) {
	unsuspender := &syncUnsuspender{}
	var logs bytes.Buffer
	runner := &InProcessAccountUnsuspension{Unsuspender: unsuspender, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	ctx := context.Background()

	if err := runner.ScheduleAccountUnsuspension(ctx, 1); err != nil {
		t.Fatalf("ScheduleAccountUnsuspension: %v", err)
	}
	runner.Wait()
	if !equalInt64s(unsuspender.calls, []int64{1}) {
		t.Fatalf("unsuspensions = %v, want account 1", unsuspender.calls)
	}
	if logs.Len() != 0 {
		t.Fatalf("logged %q for a successful unsuspension", logs.String())
	}

	// A failure is logged, and asking again runs it again.
	unsuspender.err = errors.New("queue down")
	for i := 0; i < 2; i++ {
		if err := runner.ScheduleAccountUnsuspension(ctx, 2); err != nil {
			t.Fatalf("ScheduleAccountUnsuspension of account 2: %v", err)
		}
		runner.Wait()
	}
	if !equalInt64s(unsuspender.calls, []int64{1, 2, 2}) {
		t.Fatalf("unsuspensions = %v, want account 2 twice", unsuspender.calls)
	}
	if !strings.Contains(logs.String(), "Account unsuspension failed") || !strings.Contains(logs.String(), "account_id=2") {
		t.Fatalf("failure not logged: %q", logs.String())
	}

	if err := (&InProcessAccountUnsuspension{}).ScheduleAccountUnsuspension(ctx, 1); !errors.Is(err, ErrAccountUnsuspensionMissingDeps) {
		t.Fatalf("no unsuspender: err = %v", err)
	}
	var nilRunner *InProcessAccountUnsuspension
	if err := nilRunner.ScheduleAccountUnsuspension(ctx, 1); !errors.Is(err, ErrAccountUnsuspensionMissingDeps) {
		t.Fatalf("nil runner: err = %v", err)
	}
}
