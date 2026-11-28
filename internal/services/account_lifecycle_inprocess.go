package services

import (
	"context"
	"log/slog"
	"time"
)

const (
	// defaultInProcessLifecycleTimeout bounds one attempt of an account's
	// suspension or unsuspension side effects.
	defaultInProcessLifecycleTimeout = 10 * time.Minute
	// defaultInProcessLifecycleAttempts is the attempt budget of the
	// account:suspend and account:unsuspend worker tasks: their first run
	// and 25 retries.
	defaultInProcessLifecycleAttempts = 26
	// The wait between attempts doubles from the base backoff up to the
	// max, which spreads the default attempts over about three hours.
	defaultInProcessLifecycleBaseBackoff = 5 * time.Second
	defaultInProcessLifecycleMaxBackoff  = 10 * time.Minute
)

// inProcessRetries runs the side effects of an account's suspension or
// unsuspension in the server process, as its worker task is retried: an
// attempt that fails is logged and tried again after a backoff, until one
// succeeds or the attempts run out. Zero fields take the defaults above.
type inProcessRetries struct {
	// name names the side effects in log messages, e.g. "Account
	// suspension".
	name        string
	timeout     time.Duration
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	logger      *slog.Logger
}

// run attempts fn for accountID until it succeeds or the attempts run out.
func (r inProcessRetries) run(accountID int64, fn func(ctx context.Context, accountID int64) error) {
	logger := r.logger
	if logger == nil {
		logger = slog.Default()
	}
	attempts := r.maxAttempts
	if attempts <= 0 {
		attempts = defaultInProcessLifecycleAttempts
	}
	for attempt := 1; ; attempt++ {
		err := r.attempt(accountID, fn)
		if err == nil {
			if attempt > 1 {
				logger.Info(r.name+" succeeded after retrying", "account_id", accountID, "attempt", attempt)
			}
			return
		}
		if attempt >= attempts {
			logger.Error(r.name+" failed; giving up", "account_id", accountID, "attempts", attempt, "error", err)
			return
		}
		wait := r.backoff(attempt)
		logger.Warn(r.name+" failed; retrying", "account_id", accountID, "attempt", attempt, "retry_in", wait, "error", err)
		time.Sleep(wait)
	}
}

// attempt runs fn for accountID once, within the timeout.
func (r inProcessRetries) attempt(accountID int64, fn func(ctx context.Context, accountID int64) error) error {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultInProcessLifecycleTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx, accountID)
}

// backoff is the wait after the failed attempt number attempt: the base
// backoff, doubled for each failure before it, at most the max backoff.
func (r inProcessRetries) backoff(attempt int) time.Duration {
	wait := r.baseBackoff
	if wait <= 0 {
		wait = defaultInProcessLifecycleBaseBackoff
	}
	limit := r.maxBackoff
	if limit <= 0 {
		limit = defaultInProcessLifecycleMaxBackoff
	}
	for i := 1; i < attempt && wait < limit; i++ {
		wait *= 2
	}
	return min(wait, limit)
}
