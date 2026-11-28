package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TaskTypeAccountUnsuspension = "account:unsuspend"

	// defaultAccountUnsuspensionTimeout bounds one attempt, which reads the
	// account's follower inboxes and queues a delivery for each.
	defaultAccountUnsuspensionTimeout = 10 * time.Minute
	// defaultAccountUnsuspensionMaxRetry gives an unsuspension that keeps
	// failing about three weeks of retries, as Sidekiq's 25 do for
	// Mastodon's Admin::UnsuspensionWorker.
	defaultAccountUnsuspensionMaxRetry = 25
)

var (
	ErrAccountUnsuspensionEnqueuerMissing    = errors.New("account unsuspension enqueuer missing")
	ErrAccountUnsuspensionHandlerMissingDeps = errors.New("account unsuspension handler dependencies missing")
	ErrAccountUnsuspensionPayloadInvalid     = errors.New("account unsuspension payload invalid")
)

// AccountUnsuspensionPayload is the payload of an account unsuspension task.
type AccountUnsuspensionPayload struct {
	AccountID int64 `json:"account_id"`
}

// AccountUnsuspender carries out the side effects of an unsuspension.
// *services.AccountUnsuspensionService satisfies it.
type AccountUnsuspender interface {
	Unsuspend(ctx context.Context, accountID int64) error
}

// AccountUnsuspensionRegistrar registers the account unsuspension handler.
type AccountUnsuspensionRegistrar struct {
	Unsuspender AccountUnsuspender
}

// Register wires the account unsuspension handler into the worker manager.
func (r *AccountUnsuspensionRegistrar) Register(m *Manager) error {
	if r == nil || r.Unsuspender == nil {
		return ErrAccountUnsuspensionHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypeAccountUnsuspension, &AccountUnsuspensionHandler{unsuspender: r.Unsuspender})
}

// NewAccountUnsuspensionTask builds the task that carries out the
// unsuspension of the account accountID.
func NewAccountUnsuspensionTask(accountID int64) (*asynq.Task, error) {
	if accountID <= 0 {
		return nil, ErrAccountUnsuspensionPayloadInvalid
	}
	body, err := json.Marshal(AccountUnsuspensionPayload{AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeAccountUnsuspension, body), nil
}

// AccountUnsuspensionEnqueuer queues account unsuspensions, as Mastodon
// queues Admin::UnsuspensionWorker.
type AccountUnsuspensionEnqueuer struct {
	client   *asynq.Client
	queue    string
	timeout  time.Duration
	maxRetry int
}

// NewAccountUnsuspensionEnqueuer creates an enqueuer with defaults.
func NewAccountUnsuspensionEnqueuer(client *asynq.Client) *AccountUnsuspensionEnqueuer {
	return &AccountUnsuspensionEnqueuer{
		client:   client,
		queue:    QueueDefault,
		timeout:  defaultAccountUnsuspensionTimeout,
		maxRetry: defaultAccountUnsuspensionMaxRetry,
	}
}

// ScheduleAccountUnsuspension queues the unsuspension of the account
// accountID. A task queued twice runs twice, which sends the same
// activities again.
func (e *AccountUnsuspensionEnqueuer) ScheduleAccountUnsuspension(ctx context.Context, accountID int64) error {
	if e == nil || e.client == nil {
		return ErrAccountUnsuspensionEnqueuerMissing
	}
	task, err := NewAccountUnsuspensionTask(accountID)
	if err != nil {
		return err
	}
	_, err = e.client.EnqueueContext(ctx, task,
		asynq.Queue(e.queue),
		asynq.Timeout(e.timeout),
		asynq.MaxRetry(e.maxRetry),
	)
	return err
}

// AccountUnsuspensionHandler runs account unsuspension tasks.
type AccountUnsuspensionHandler struct {
	unsuspender AccountUnsuspender
}

// ProcessTask carries out the unsuspension. A failure is returned for Asynq
// to retry; a retry sends the same activities.
func (h *AccountUnsuspensionHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.unsuspender == nil {
		return ErrAccountUnsuspensionHandlerMissingDeps
	}
	if task == nil {
		return asynq.SkipRetry
	}
	var payload AccountUnsuspensionPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.AccountID <= 0 {
		return fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}
	return h.unsuspender.Unsuspend(ctx, payload.AccountID)
}
