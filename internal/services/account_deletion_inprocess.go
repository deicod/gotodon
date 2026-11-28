package services

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const defaultInProcessAccountDeletionTimeout = time.Hour

// accountDeleter deletes an account's data. *AccountDeletionService
// satisfies it.
type accountDeleter interface {
	Delete(ctx context.Context, accountID int64, opts AccountDeletionOptions) error
}

// InProcessAccountDeletion runs account deletions in the server process
// when no worker queue is configured. A deletion runs once, in the
// background; one that fails is logged and keeps its deletion request, so
// asking for it again goes on where it stopped. While a deletion of an
// account runs, another one of the same account is not started.
type InProcessAccountDeletion struct {
	Deleter accountDeleter
	Timeout time.Duration
	Logger  *slog.Logger

	mu      sync.Mutex
	running map[int64]bool
	wg      sync.WaitGroup
}

// ScheduleAccountDeletion starts deleting the account accountID with opts in
// a background goroutine.
func (d *InProcessAccountDeletion) ScheduleAccountDeletion(_ context.Context, accountID int64, opts AccountDeletionOptions) error {
	if d == nil || d.Deleter == nil {
		return ErrAccountDeletionMissingDeps
	}
	d.mu.Lock()
	if d.running[accountID] {
		d.mu.Unlock()
		return nil
	}
	if d.running == nil {
		d.running = make(map[int64]bool)
	}
	d.running[accountID] = true
	d.mu.Unlock()

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = defaultInProcessAccountDeletionTimeout
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.running, accountID)
			d.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := d.Deleter.Delete(ctx, accountID, opts); err != nil {
			logger.Error("Account deletion failed; it can be asked for again", "account_id", accountID, "error", err)
		}
	}()
	return nil
}

// Wait blocks until the deletions started so far have ended.
func (d *InProcessAccountDeletion) Wait() {
	d.wg.Wait()
}
