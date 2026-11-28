package services

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const defaultInProcessAccountUnsuspensionTimeout = 10 * time.Minute

// accountUnsuspender carries out the side effects of an unsuspension.
// *AccountUnsuspensionService satisfies it.
type accountUnsuspender interface {
	Unsuspend(ctx context.Context, accountID int64) error
}

// InProcessAccountUnsuspension runs account unsuspensions in the server
// process when no worker queue is configured. An unsuspension runs once,
// in the background; one that fails is logged.
type InProcessAccountUnsuspension struct {
	Unsuspender accountUnsuspender
	Timeout     time.Duration
	Logger      *slog.Logger

	wg sync.WaitGroup
}

// ScheduleAccountUnsuspension starts carrying out the unsuspension of the
// account accountID in a background goroutine.
func (u *InProcessAccountUnsuspension) ScheduleAccountUnsuspension(_ context.Context, accountID int64) error {
	if u == nil || u.Unsuspender == nil {
		return ErrAccountUnsuspensionMissingDeps
	}
	timeout := u.Timeout
	if timeout <= 0 {
		timeout = defaultInProcessAccountUnsuspensionTimeout
	}
	logger := u.Logger
	if logger == nil {
		logger = slog.Default()
	}
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := u.Unsuspender.Unsuspend(ctx, accountID); err != nil {
			logger.Error("Account unsuspension failed", "account_id", accountID, "error", err)
		}
	}()
	return nil
}

// Wait blocks until the unsuspensions started so far have ended.
func (u *InProcessAccountUnsuspension) Wait() {
	u.wg.Wait()
}
