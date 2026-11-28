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

// flakySuspender records suspensions and fails the first failures of them
// with err.
type flakySuspender struct {
	mu       sync.Mutex
	calls    []int64
	failures int
	err      error
}

func (s *flakySuspender) Suspend(ctx context.Context, accountID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, accountID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.failures != 0 {
		s.failures--
		return s.err
	}
	return nil
}

func newTestInProcessSuspension(suspender accountSuspender, logs *bytes.Buffer) *InProcessAccountSuspension {
	return &InProcessAccountSuspension{
		Suspender:   suspender,
		MaxAttempts: 4,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  2 * time.Millisecond,
		Logger:      slog.New(slog.NewTextHandler(logs, nil)),
	}
}

func TestInProcessAccountSuspension(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds", func(t *testing.T) {
		suspender := &flakySuspender{}
		var logs bytes.Buffer
		runner := newTestInProcessSuspension(suspender, &logs)
		if err := runner.ScheduleAccountSuspension(ctx, 1); err != nil {
			t.Fatalf("ScheduleAccountSuspension: %v", err)
		}
		runner.Wait()
		if !equalInt64s(suspender.calls, []int64{1}) {
			t.Fatalf("attempts = %v, want account 1 once", suspender.calls)
		}
		if logs.Len() != 0 {
			t.Fatalf("logged %q for a successful suspension", logs.String())
		}
	})

	t.Run("retries a failure until it succeeds", func(t *testing.T) {
		suspender := &flakySuspender{failures: 2, err: errors.New("delivery queue full")}
		var logs bytes.Buffer
		runner := newTestInProcessSuspension(suspender, &logs)
		if err := runner.ScheduleAccountSuspension(ctx, 2); err != nil {
			t.Fatalf("ScheduleAccountSuspension: %v", err)
		}
		runner.Wait()
		if !equalInt64s(suspender.calls, []int64{2, 2, 2}) {
			t.Fatalf("attempts = %v, want two failures and a success", suspender.calls)
		}
		if got := strings.Count(logs.String(), "Account suspension failed; retrying"); got != 2 {
			t.Fatalf("logged %d retries, want 2: %q", got, logs.String())
		}
		if !strings.Contains(logs.String(), "Account suspension succeeded after retrying") || strings.Contains(logs.String(), "giving up") {
			t.Fatalf("logs = %q, want the success after retrying", logs.String())
		}
	})

	t.Run("gives up after its attempts", func(t *testing.T) {
		suspender := &flakySuspender{failures: -1, err: errors.New("database down")}
		var logs bytes.Buffer
		runner := newTestInProcessSuspension(suspender, &logs)
		if err := runner.ScheduleAccountSuspension(ctx, 3); err != nil {
			t.Fatalf("ScheduleAccountSuspension: %v", err)
		}
		runner.Wait()
		if len(suspender.calls) != 4 {
			t.Fatalf("attempts = %v, want 4", suspender.calls)
		}
		if !strings.Contains(logs.String(), "Account suspension failed; giving up") || !strings.Contains(logs.String(), "account_id=3") {
			t.Fatalf("giving up not logged: %q", logs.String())
		}
	})

	t.Run("runs the suspension service", func(t *testing.T) {
		deliverer := &recordingSuspensionDeliverer{}
		service := NewAccountSuspensionService(&suspensionStore{accounts: suspensionAccounts()}, deliverer)
		runner := &InProcessAccountSuspension{Suspender: service}
		// Account 20 is a suspended local account.
		if err := runner.ScheduleAccountSuspension(ctx, 20); err != nil {
			t.Fatalf("ScheduleAccountSuspension: %v", err)
		}
		runner.Wait()
		if len(deliverer.suspensions) != 1 || deliverer.suspensions[0].ID != 20 {
			t.Fatalf("delivered suspensions %v, want account 20", deliverer.suspensions)
		}
	})

	t.Run("missing dependencies", func(t *testing.T) {
		if err := (&InProcessAccountSuspension{}).ScheduleAccountSuspension(ctx, 1); !errors.Is(err, ErrAccountSuspensionMissingDeps) {
			t.Fatalf("no suspender: err = %v", err)
		}
		var nilRunner *InProcessAccountSuspension
		if err := nilRunner.ScheduleAccountSuspension(ctx, 1); !errors.Is(err, ErrAccountSuspensionMissingDeps) {
			t.Fatalf("nil runner: err = %v", err)
		}
	})
}
