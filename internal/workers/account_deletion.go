package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/services"
)

const (
	TaskTypeAccountDeletion = "account:delete"

	// defaultAccountDeletionTimeout bounds one attempt. An attempt that
	// runs out stops where it is, and the retry goes on from there.
	defaultAccountDeletionTimeout = time.Hour
	// defaultAccountDeletionMaxRetry gives a deletion that keeps failing
	// about three weeks of retries, as Sidekiq's 25 do in Mastodon.
	defaultAccountDeletionMaxRetry = 25
	// defaultAccountDeletionUniqueTTL keeps a second deletion of the same
	// account out of the queue while the first is queued or retrying, as
	// Mastodon's lock: :until_executed, lock_ttl: 1.week does.
	defaultAccountDeletionUniqueTTL = 7 * 24 * time.Hour
)

var (
	ErrAccountDeletionEnqueuerMissing    = errors.New("account deletion enqueuer missing")
	ErrAccountDeletionHandlerMissingDeps = errors.New("account deletion handler dependencies missing")
	ErrAccountDeletionPayloadInvalid     = errors.New("account deletion payload invalid")
)

// AccountDeletionPayload is the payload of an account deletion task.
type AccountDeletionPayload struct {
	AccountID int64                           `json:"account_id"`
	Options   services.AccountDeletionOptions `json:"options"`
}

// AccountDeleter deletes an account's data. *services.AccountDeletionService
// satisfies it.
type AccountDeleter interface {
	Delete(ctx context.Context, accountID int64, opts services.AccountDeletionOptions) error
}

// AccountDeletionRegistrar registers the account deletion handler.
type AccountDeletionRegistrar struct {
	Deleter AccountDeleter
}

// Register wires the account deletion handler into the worker manager.
func (r *AccountDeletionRegistrar) Register(m *Manager) error {
	if r == nil || r.Deleter == nil {
		return ErrAccountDeletionHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypeAccountDeletion, &AccountDeletionHandler{deleter: r.Deleter})
}

// NewAccountDeletionTask builds the task that deletes the data of the
// account accountID with opts.
func NewAccountDeletionTask(accountID int64, opts services.AccountDeletionOptions) (*asynq.Task, error) {
	if accountID <= 0 {
		return nil, ErrAccountDeletionPayloadInvalid
	}
	body, err := json.Marshal(AccountDeletionPayload{AccountID: accountID, Options: opts})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeAccountDeletion, body), nil
}

// AccountDeletionEnqueuer queues account deletions, as Mastodon queues
// AccountDeletionWorker and Admin::AccountDeletionWorker.
type AccountDeletionEnqueuer struct {
	client    *asynq.Client
	queue     string
	timeout   time.Duration
	maxRetry  int
	uniqueTTL time.Duration
}

// NewAccountDeletionEnqueuer creates an enqueuer with defaults.
func NewAccountDeletionEnqueuer(client *asynq.Client) *AccountDeletionEnqueuer {
	return &AccountDeletionEnqueuer{
		client:    client,
		queue:     QueueLow,
		timeout:   defaultAccountDeletionTimeout,
		maxRetry:  defaultAccountDeletionMaxRetry,
		uniqueTTL: defaultAccountDeletionUniqueTTL,
	}
}

// ScheduleAccountDeletion queues the deletion of the account accountID with
// opts. A deletion of the account with the same options that is already
// queued or retrying stands for this one.
func (e *AccountDeletionEnqueuer) ScheduleAccountDeletion(ctx context.Context, accountID int64, opts services.AccountDeletionOptions) error {
	if e == nil || e.client == nil {
		return ErrAccountDeletionEnqueuerMissing
	}
	task, err := NewAccountDeletionTask(accountID, opts)
	if err != nil {
		return err
	}
	_, err = e.client.EnqueueContext(ctx, task,
		asynq.Queue(e.queue),
		asynq.Timeout(e.timeout),
		asynq.MaxRetry(e.maxRetry),
		asynq.Unique(e.uniqueTTL),
	)
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// AccountDeletionHandler runs account deletion tasks.
type AccountDeletionHandler struct {
	deleter AccountDeleter
}

// ProcessTask deletes the account's data. A failure is returned for Asynq
// to retry: the deletion keeps its deletion request until it finishes, and
// a retry goes on where the failed attempt stopped.
func (h *AccountDeletionHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.deleter == nil {
		return ErrAccountDeletionHandlerMissingDeps
	}
	if task == nil {
		return asynq.SkipRetry
	}
	var payload AccountDeletionPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.AccountID <= 0 {
		return fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}
	return h.deleter.Delete(ctx, payload.AccountID, payload.Options)
}
