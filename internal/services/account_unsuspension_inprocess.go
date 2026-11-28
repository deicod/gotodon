package services

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// accountUnsuspender carries out the side effects of an unsuspension.
// *AccountUnsuspensionService satisfies it.
type accountUnsuspender interface {
	Unsuspend(ctx context.Context, accountID int64) error
}

// InProcessAccountUnsuspension runs account unsuspensions in the server
// process when no worker queue is configured. An unsuspension runs in the
// background; one that fails is logged and tried again after a backoff, as
// the worker task is retried, until it succeeds or runs out of attempts. A
// retry sends the same activities, with the same IDs, again. The attempts
// live in the process: a restart drops those still waiting.
type InProcessAccountUnsuspension struct {
	Unsuspender accountUnsuspender
	// Timeout bounds one attempt; 0 means ten minutes.
	Timeout time.Duration
	// MaxAttempts bounds the attempts of one unsuspension; 0 means 26.
	MaxAttempts int
	// BaseBackoff is the wait after the first failure, which doubles after
	// each failure up to MaxBackoff; 0 means five seconds and ten minutes.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Logger      *slog.Logger

	wg sync.WaitGroup
}

// ScheduleAccountUnsuspension starts carrying out the unsuspension of the
// account accountID in a background goroutine.
func (u *InProcessAccountUnsuspension) ScheduleAccountUnsuspension(_ context.Context, accountID int64) error {
	if u == nil || u.Unsuspender == nil {
		return ErrAccountUnsuspensionMissingDeps
	}
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		u.retries().run(accountID, u.Unsuspender.Unsuspend)
	}()
	return nil
}

// retries is how u attempts an unsuspension.
func (u *InProcessAccountUnsuspension) retries() inProcessRetries {
	return inProcessRetries{
		name:        "Account unsuspension",
		timeout:     u.Timeout,
		maxAttempts: u.MaxAttempts,
		baseBackoff: u.BaseBackoff,
		maxBackoff:  u.MaxBackoff,
		logger:      u.Logger,
	}
}

// Wait blocks until the unsuspensions started so far have ended, retries
// included.
func (u *InProcessAccountUnsuspension) Wait() {
	u.wg.Wait()
}
