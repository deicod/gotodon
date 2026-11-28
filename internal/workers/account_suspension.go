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
	TaskTypeAccountSuspension = "account:suspend"

	// defaultAccountSuspensionTimeout bounds one attempt, which reads the
	// account's follower inboxes, or its follows of local accounts, and
	// queues a delivery for each.
	defaultAccountSuspensionTimeout = 10 * time.Minute
	// defaultAccountSuspensionMaxRetry gives a suspension that keeps
	// failing about three weeks of retries, as Sidekiq's 25 do for
	// Mastodon's Admin::SuspensionWorker.
	defaultAccountSuspensionMaxRetry = 25
)

var (
	ErrAccountSuspensionEnqueuerMissing    = errors.New("account suspension enqueuer missing")
	ErrAccountSuspensionHandlerMissingDeps = errors.New("account suspension handler dependencies missing")
	ErrAccountSuspensionPayloadInvalid     = errors.New("account suspension payload invalid")
)

// AccountSuspensionPayload is the payload of an account suspension task.
type AccountSuspensionPayload struct {
	AccountID int64 `json:"account_id"`
}

// AccountSuspender carries out the side effects of a suspension.
// *services.AccountSuspensionService satisfies it.
type AccountSuspender interface {
	Suspend(ctx context.Context, accountID int64) error
}

// AccountSuspensionRegistrar registers the account suspension handler.
type AccountSuspensionRegistrar struct {
	Suspender AccountSuspender
}

// Register wires the account suspension handler into the worker manager.
func (r *AccountSuspensionRegistrar) Register(m *Manager) error {
	if r == nil || r.Suspender == nil {
		return ErrAccountSuspensionHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypeAccountSuspension, &AccountSuspensionHandler{suspender: r.Suspender})
}

// NewAccountSuspensionTask builds the task that carries out the suspension
// of the account accountID.
func NewAccountSuspensionTask(accountID int64) (*asynq.Task, error) {
	if accountID <= 0 {
		return nil, ErrAccountSuspensionPayloadInvalid
	}
	body, err := json.Marshal(AccountSuspensionPayload{AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeAccountSuspension, body), nil
}

// AccountSuspensionEnqueuer queues account suspensions, as Mastodon queues
// Admin::SuspensionWorker.
type AccountSuspensionEnqueuer struct {
	client   *asynq.Client
	queue    string
	timeout  time.Duration
	maxRetry int
}

// NewAccountSuspensionEnqueuer creates an enqueuer with defaults.
func NewAccountSuspensionEnqueuer(client *asynq.Client) *AccountSuspensionEnqueuer {
	return &AccountSuspensionEnqueuer{
		client:   client,
		queue:    QueueDefault,
		timeout:  defaultAccountSuspensionTimeout,
		maxRetry: defaultAccountSuspensionMaxRetry,
	}
}

// ScheduleAccountSuspension queues the suspension of the account
// accountID. A task queued twice runs twice, which sends the same
// activities again.
func (e *AccountSuspensionEnqueuer) ScheduleAccountSuspension(ctx context.Context, accountID int64) error {
	if e == nil || e.client == nil {
		return ErrAccountSuspensionEnqueuerMissing
	}
	task, err := NewAccountSuspensionTask(accountID)
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

// AccountSuspensionHandler runs account suspension tasks.
type AccountSuspensionHandler struct {
	suspender AccountSuspender
}

// ProcessTask carries out the suspension. A failure is returned for Asynq
// to retry; a retry sends the same activities.
func (h *AccountSuspensionHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.suspender == nil {
		return ErrAccountSuspensionHandlerMissingDeps
	}
	if task == nil {
		return asynq.SkipRetry
	}
	var payload AccountSuspensionPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.AccountID <= 0 {
		return fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}
	return h.suspender.Suspend(ctx, payload.AccountID)
}
