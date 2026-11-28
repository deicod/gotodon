package services

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultInProcessAccountUnsuspensionTimeout = 10 * time.Minute
	// defaultInProcessAccountUnsuspensionAttempts is the attempt budget of
	// the account:unsuspend worker task: its first run and 25 retries.
	defaultInProcessAccountUnsuspensionAttempts = 26
	// The wait between attempts doubles from the base backoff up to the
	// max, which spreads the default attempts over about three hours.
	defaultInProcessAccountUnsuspensionBaseBackoff = 5 * time.Second
	defaultInProcessAccountUnsuspensionMaxBackoff  = 10 * time.Minute
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
		u.run(accountID)
	}()
	return nil
}

// run attempts the unsuspension of accountID until it succeeds or the
// attempts run out.
func (u *InProcessAccountUnsuspension) run(accountID int64) {
	logger := u.Logger
	if logger == nil {
		logger = slog.Default()
	}
	attempts := u.MaxAttempts
	if attempts <= 0 {
		attempts = defaultInProcessAccountUnsuspensionAttempts
	}
	for attempt := 1; ; attempt++ {
		err := u.attempt(accountID)
		if err == nil {
			if attempt > 1 {
				logger.Info("Account unsuspension succeeded after retrying", "account_id", accountID, "attempt", attempt)
			}
			return
		}
		if attempt >= attempts {
			logger.Error("Account unsuspension failed; giving up", "account_id", accountID, "attempts", attempt, "error", err)
			return
		}
		wait := u.backoff(attempt)
		logger.Warn("Account unsuspension failed; retrying", "account_id", accountID, "attempt", attempt, "retry_in", wait, "error", err)
		time.Sleep(wait)
	}
}

// attempt runs the unsuspension of accountID once, within the timeout.
func (u *InProcessAccountUnsuspension) attempt(accountID int64) error {
	timeout := u.Timeout
	if timeout <= 0 {
		timeout = defaultInProcessAccountUnsuspensionTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return u.Unsuspender.Unsuspend(ctx, accountID)
}

// backoff is the wait after the failed attempt number attempt: the base
// backoff, doubled for each failure before it, at most the max backoff.
func (u *InProcessAccountUnsuspension) backoff(attempt int) time.Duration {
	wait := u.BaseBackoff
	if wait <= 0 {
		wait = defaultInProcessAccountUnsuspensionBaseBackoff
	}
	limit := u.MaxBackoff
	if limit <= 0 {
		limit = defaultInProcessAccountUnsuspensionMaxBackoff
	}
	for i := 1; i < attempt && wait < limit; i++ {
		wait *= 2
	}
	return min(wait, limit)
}

// Wait blocks until the unsuspensions started so far have ended, retries
// included.
func (u *InProcessAccountUnsuspension) Wait() {
	u.wg.Wait()
}
