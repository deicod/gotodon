package workers

import (
	"context"
	"errors"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TaskTypeMediaCleanup = "cleanup:media"

	defaultMediaCleanupInterval  = time.Hour
	defaultMediaCleanupUniqueTTL = 55 * time.Minute
	defaultMediaCleanupTimeout   = 10 * time.Minute
	defaultMediaCleanupMaxRetry  = 3
)

var (
	ErrMediaCleanupEnqueuerMissing    = errors.New("media cleanup enqueuer missing")
	ErrMediaCleanupHandlerMissingDeps = errors.New("media cleanup handler dependencies missing")
)

// MediaCleanupRunner executes media cleanup.
type MediaCleanupRunner interface {
	Run(ctx context.Context) error
}

// MediaCleanupRegistrar registers the media cleanup handler.
type MediaCleanupRegistrar struct {
	Runner MediaCleanupRunner
}

// Register wires the media cleanup handler into the worker manager.
func (r *MediaCleanupRegistrar) Register(m *Manager) error {
	if r == nil || r.Runner == nil {
		return ErrMediaCleanupHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypeMediaCleanup, &MediaCleanupHandler{runner: r.Runner})
}

// MediaCleanupEnqueuer schedules media cleanup tasks.
type MediaCleanupEnqueuer struct {
	client    *asynq.Client
	queue     string
	uniqueTTL time.Duration
	timeout   time.Duration
	maxRetry  int
}

// NewMediaCleanupEnqueuer creates an enqueuer with defaults.
func NewMediaCleanupEnqueuer(client *asynq.Client) *MediaCleanupEnqueuer {
	return &MediaCleanupEnqueuer{
		client:    client,
		queue:     QueueLow,
		uniqueTTL: defaultMediaCleanupUniqueTTL,
		timeout:   defaultMediaCleanupTimeout,
		maxRetry:  defaultMediaCleanupMaxRetry,
	}
}

// Enqueue schedules a media cleanup task.
func (e *MediaCleanupEnqueuer) Enqueue(ctx context.Context) error {
	if e == nil || e.client == nil {
		return ErrMediaCleanupEnqueuerMissing
	}
	task := asynq.NewTask(TaskTypeMediaCleanup, nil)
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

// StartMediaCleanupPolling enqueues cleanup tasks at a fixed interval.
func StartMediaCleanupPolling(ctx context.Context, enqueuer *MediaCleanupEnqueuer, interval time.Duration) {
	if enqueuer == nil {
		return
	}
	if interval <= 0 {
		interval = defaultMediaCleanupInterval
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

// MediaCleanupHandler runs media cleanup tasks.
type MediaCleanupHandler struct {
	runner MediaCleanupRunner
}

// ProcessTask executes media cleanup.
func (h *MediaCleanupHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.runner == nil {
		return ErrMediaCleanupHandlerMissingDeps
	}
	if task == nil {
		return asynq.SkipRetry
	}
	return h.runner.Run(ctx)
}
