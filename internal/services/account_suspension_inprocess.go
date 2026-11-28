package services

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// accountSuspender carries out the side effects of a suspension.
// *AccountSuspensionService satisfies it.
type accountSuspender interface {
	Suspend(ctx context.Context, accountID int64) error
}

// InProcessAccountSuspension runs the side effects of account suspensions in
// the server process when no worker queue is configured. A suspension runs
// in the background; one that fails is logged and tried again after a
// backoff, as the worker task is retried, until it succeeds or runs out of
// attempts. A retry sends the same activities, with the same IDs, again,
// and deletes only the follows that are left. The attempts live in the
// process: a restart drops those still waiting.
type InProcessAccountSuspension struct {
	Suspender accountSuspender
	// Timeout bounds one attempt; 0 means ten minutes.
	Timeout time.Duration
	// MaxAttempts bounds the attempts of one suspension; 0 means 26.
	MaxAttempts int
	// BaseBackoff is the wait after the first failure, which doubles after
	// each failure up to MaxBackoff; 0 means five seconds and ten minutes.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Logger      *slog.Logger

	wg sync.WaitGroup
}

// ScheduleAccountSuspension starts carrying out the suspension of the
// account accountID in a background goroutine.
func (s *InProcessAccountSuspension) ScheduleAccountSuspension(_ context.Context, accountID int64) error {
	if s == nil || s.Suspender == nil {
		return ErrAccountSuspensionMissingDeps
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.retries().run(accountID, s.Suspender.Suspend)
	}()
	return nil
}

// retries is how s attempts a suspension.
func (s *InProcessAccountSuspension) retries() inProcessRetries {
	return inProcessRetries{
		name:        "Account suspension",
		timeout:     s.Timeout,
		maxAttempts: s.MaxAttempts,
		baseBackoff: s.BaseBackoff,
		maxBackoff:  s.MaxBackoff,
		logger:      s.Logger,
	}
}

// Wait blocks until the suspensions started so far have ended, retries
// included.
func (s *InProcessAccountSuspension) Wait() {
	s.wg.Wait()
}
