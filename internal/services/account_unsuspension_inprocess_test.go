package services

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// flakyUnsuspender records unsuspensions and fails the first failures of
// them with err.
type flakyUnsuspender struct {
	mu       sync.Mutex
	calls    []int64
	failures int
	err      error
}

func (u *flakyUnsuspender) Unsuspend(ctx context.Context, accountID int64) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, accountID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if u.failures != 0 {
		u.failures--
		return u.err
	}
	return nil
}

func newTestInProcessUnsuspension(unsuspender accountUnsuspender, logs *bytes.Buffer) *InProcessAccountUnsuspension {
	return &InProcessAccountUnsuspension{
		Unsuspender: unsuspender,
		MaxAttempts: 4,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  2 * time.Millisecond,
		Logger:      slog.New(slog.NewTextHandler(logs, nil)),
	}
}

func TestInProcessAccountUnsuspension(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds", func(t *testing.T) {
		unsuspender := &flakyUnsuspender{}
		var logs bytes.Buffer
		runner := newTestInProcessUnsuspension(unsuspender, &logs)
		if err := runner.ScheduleAccountUnsuspension(ctx, 1); err != nil {
			t.Fatalf("ScheduleAccountUnsuspension: %v", err)
		}
		runner.Wait()
		if !equalInt64s(unsuspender.calls, []int64{1}) {
			t.Fatalf("attempts = %v, want account 1 once", unsuspender.calls)
		}
		if logs.Len() != 0 {
			t.Fatalf("logged %q for a successful unsuspension", logs.String())
		}
	})

	t.Run("retries a failure until it succeeds", func(t *testing.T) {
		unsuspender := &flakyUnsuspender{failures: 2, err: errors.New("delivery queue full")}
		var logs bytes.Buffer
		runner := newTestInProcessUnsuspension(unsuspender, &logs)
		if err := runner.ScheduleAccountUnsuspension(ctx, 2); err != nil {
			t.Fatalf("ScheduleAccountUnsuspension: %v", err)
		}
		runner.Wait()
		if !equalInt64s(unsuspender.calls, []int64{2, 2, 2}) {
			t.Fatalf("attempts = %v, want two failures and a success", unsuspender.calls)
		}
		if got := strings.Count(logs.String(), "Account unsuspension failed; retrying"); got != 2 {
			t.Fatalf("logged %d retries, want 2: %q", got, logs.String())
		}
		if !strings.Contains(logs.String(), "succeeded after retrying") || strings.Contains(logs.String(), "giving up") {
			t.Fatalf("logs = %q, want the success after retrying", logs.String())
		}
	})

	t.Run("gives up after its attempts", func(t *testing.T) {
		unsuspender := &flakyUnsuspender{failures: -1, err: errors.New("database down")}
		var logs bytes.Buffer
		runner := newTestInProcessUnsuspension(unsuspender, &logs)
		if err := runner.ScheduleAccountUnsuspension(ctx, 3); err != nil {
			t.Fatalf("ScheduleAccountUnsuspension: %v", err)
		}
		runner.Wait()
		if len(unsuspender.calls) != 4 {
			t.Fatalf("attempts = %v, want 4", unsuspender.calls)
		}
		if !strings.Contains(logs.String(), "Account unsuspension failed; giving up") || !strings.Contains(logs.String(), "account_id=3") {
			t.Fatalf("giving up not logged: %q", logs.String())
		}
	})

	t.Run("missing dependencies", func(t *testing.T) {
		if err := (&InProcessAccountUnsuspension{}).ScheduleAccountUnsuspension(ctx, 1); !errors.Is(err, ErrAccountUnsuspensionMissingDeps) {
			t.Fatalf("no unsuspender: err = %v", err)
		}
		var nilRunner *InProcessAccountUnsuspension
		if err := nilRunner.ScheduleAccountUnsuspension(ctx, 1); !errors.Is(err, ErrAccountUnsuspensionMissingDeps) {
			t.Fatalf("nil runner: err = %v", err)
		}
	})
}

func TestInProcessAccountUnsuspensionBackoff(t *testing.T) {
	defaults := &InProcessAccountUnsuspension{}
	var total time.Duration
	for attempt := 1; attempt < defaultInProcessAccountUnsuspensionAttempts; attempt++ {
		total += defaults.backoff(attempt)
	}
	if total < 2*time.Hour || total > 4*time.Hour {
		t.Fatalf("default attempts span %v, want about three hours", total)
	}

	runner := &InProcessAccountUnsuspension{BaseBackoff: time.Second, MaxBackoff: 10 * time.Second}
	for attempt, want := range map[int]time.Duration{
		1:   time.Second,
		2:   2 * time.Second,
		4:   8 * time.Second,
		5:   10 * time.Second,
		100: 10 * time.Second,
	} {
		if got := runner.backoff(attempt); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, want)
		}
	}
}
