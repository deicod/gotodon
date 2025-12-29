package workers

import (
	"context"
	"errors"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TaskTypeStatusCleanup = "cleanup:statuses"

	defaultStatusCleanupInterval  = time.Hour
	defaultStatusCleanupUniqueTTL = 55 * time.Minute
	defaultStatusCleanupTimeout   = 10 * time.Minute
	defaultStatusCleanupMaxRetry  = 3
)

var (
	ErrStatusCleanupEnqueuerMissing    = errors.New("status cleanup enqueuer missing")
	ErrStatusCleanupHandlerMissingDeps = errors.New("status cleanup handler dependencies missing")
)

// StatusCleanupRunner executes a cleanup pass.
type StatusCleanupRunner interface {
	Run(ctx context.Context) error
}

// StatusCleanupRegistrar registers the status cleanup handler.
type StatusCleanupRegistrar struct {
	Runner StatusCleanupRunner
}

// Register wires the status cleanup handler into the worker manager.
func (r *StatusCleanupRegistrar) Register(m *Manager) error {
	if r == nil || r.Runner == nil {
		return ErrStatusCleanupHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypeStatusCleanup, &StatusCleanupHandler{runner: r.Runner})
}

// StatusCleanupEnqueuer schedules cleanup tasks.
type StatusCleanupEnqueuer struct {
	client    *asynq.Client
	queue     string
	uniqueTTL time.Duration
	timeout   time.Duration
	maxRetry  int
}

// NewStatusCleanupEnqueuer creates an enqueuer with defaults.
func NewStatusCleanupEnqueuer(client *asynq.Client) *StatusCleanupEnqueuer {
	return &StatusCleanupEnqueuer{
		client:    client,
		queue:     QueueLow,
		uniqueTTL: defaultStatusCleanupUniqueTTL,
		timeout:   defaultStatusCleanupTimeout,
		maxRetry:  defaultStatusCleanupMaxRetry,
	}
}

// Enqueue schedules a status cleanup task.
func (e *StatusCleanupEnqueuer) Enqueue(ctx context.Context) error {
	if e == nil || e.client == nil {
		return ErrStatusCleanupEnqueuerMissing
	}
	task := asynq.NewTask(TaskTypeStatusCleanup, nil)
	_, err := e.client.Enqueue(task,
		asynq.Queue(e.queue),
		asynq.Timeout(e.timeout),
		asynq.MaxRetry(e.maxRetry),
		asynq.Unique(e.uniqueTTL),
	)
	if err != nil && errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// StartStatusCleanupPolling enqueues cleanup tasks at a fixed interval.
func StartStatusCleanupPolling(ctx context.Context, enqueuer *StatusCleanupEnqueuer, interval time.Duration) {
	if enqueuer == nil {
		return
	}
	if interval <= 0 {
		interval = defaultStatusCleanupInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	_ = enqueuer.Enqueue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = enqueuer.Enqueue(ctx)
		}
	}
}

// StatusCleanupHandler runs status cleanup tasks.
type StatusCleanupHandler struct {
	runner StatusCleanupRunner
}

// ProcessTask executes a cleanup pass.
func (h *StatusCleanupHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.runner == nil {
		return ErrStatusCleanupHandlerMissingDeps
	}
	if task == nil {
		return asynq.SkipRetry
	}
	return h.runner.Run(ctx)
}
