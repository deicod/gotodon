package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/db"
)

const (
	TaskTypeScheduledStatusPoller  = "scheduled_statuses:poll"
	TaskTypePublishScheduledStatus = "scheduled_statuses:publish"

	defaultScheduledPollInterval = time.Minute
	defaultPublishMaxRetry       = 5
	defaultPublishTimeout        = 60 * time.Second
	defaultPollerUniqueTTL       = 45 * time.Second
	defaultPublishUniqueTTL      = 24 * time.Hour
)

var (
	ErrScheduledStatusPayloadInvalid     = errors.New("scheduled status payload invalid")
	ErrScheduledStatusEnqueuerMissing    = errors.New("scheduled status enqueuer missing")
	ErrScheduledStatusHandlerMissingDeps = errors.New("scheduled status handler dependencies missing")
)

// ScheduledStatusStore loads and removes scheduled statuses.
type ScheduledStatusStore interface {
	ListDueScheduledStatuses(ctx context.Context) ([]db.ScheduledStatus, error)
	DeleteScheduledStatus(ctx context.Context, arg db.DeleteScheduledStatusParams) error
}

// ScheduledStatusPublisher publishes a scheduled status payload as a new status.
type ScheduledStatusPublisher interface {
	PublishScheduledStatus(ctx context.Context, scheduled db.ScheduledStatus) error
}

// ScheduledStatusPublishEnqueuer enqueues publish jobs for scheduled statuses.
type ScheduledStatusPublishEnqueuer interface {
	EnqueuePublish(ctx context.Context, scheduled db.ScheduledStatus) error
}

// PublishScheduledStatusPayload is the payload for publishing a scheduled status.
type PublishScheduledStatusPayload struct {
	ID        int64           `json:"id"`
	AccountID int64           `json:"account_id"`
	Params    json.RawMessage `json:"params"`
}

// ScheduledStatusRegistrar registers scheduled status handlers.
type ScheduledStatusRegistrar struct {
	Store     ScheduledStatusStore
	Publisher ScheduledStatusPublisher
}

// Register wires scheduled status handlers into the worker manager.
func (r *ScheduledStatusRegistrar) Register(m *Manager) error {
	if r == nil || r.Store == nil || r.Publisher == nil {
		return ErrScheduledStatusHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	enqueuer := NewScheduledStatusEnqueuer(m.Client())
	poller := &ScheduledStatusPoller{
		store:    r.Store,
		enqueuer: enqueuer,
	}
	publisher := &ScheduledStatusPublisherHandler{
		publisher: r.Publisher,
	}
	if err := m.Handle(TaskTypeScheduledStatusPoller, poller); err != nil {
		return err
	}
	return m.Handle(TaskTypePublishScheduledStatus, publisher)
}

// ScheduledStatusEnqueuer enqueues scheduled status jobs.
type ScheduledStatusEnqueuer struct {
	client           *asynq.Client
	pollerQueue      string
	publishQueue     string
	publishMaxRetry  int
	publishTimeout   time.Duration
	pollerUniqueTTL  time.Duration
	publishUniqueTTL time.Duration
}

// NewScheduledStatusEnqueuer creates an enqueuer with defaults.
func NewScheduledStatusEnqueuer(client *asynq.Client) *ScheduledStatusEnqueuer {
	return &ScheduledStatusEnqueuer{
		client:           client,
		pollerQueue:      QueueLow,
		publishQueue:     QueueDefault,
		publishMaxRetry:  defaultPublishMaxRetry,
		publishTimeout:   defaultPublishTimeout,
		pollerUniqueTTL:  defaultPollerUniqueTTL,
		publishUniqueTTL: defaultPublishUniqueTTL,
	}
}

// EnqueuePoller schedules a poller task to enqueue due scheduled statuses.
func (e *ScheduledStatusEnqueuer) EnqueuePoller(ctx context.Context) error {
	if e == nil || e.client == nil {
		return ErrScheduledStatusEnqueuerMissing
	}
	task := asynq.NewTask(TaskTypeScheduledStatusPoller, nil)
	_, err := e.client.Enqueue(task,
		asynq.Queue(e.pollerQueue),
		asynq.Unique(e.pollerUniqueTTL),
	)
	if err != nil && errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// EnqueuePublish schedules a job to publish a specific scheduled status.
func (e *ScheduledStatusEnqueuer) EnqueuePublish(ctx context.Context, scheduled db.ScheduledStatus) error {
	if e == nil || e.client == nil {
		return ErrScheduledStatusEnqueuerMissing
	}
	task, err := NewPublishScheduledStatusTask(scheduled)
	if err != nil {
		return err
	}
	_, err = e.client.Enqueue(task,
		asynq.Queue(e.publishQueue),
		asynq.MaxRetry(e.publishMaxRetry),
		asynq.Timeout(e.publishTimeout),
		asynq.Unique(e.publishUniqueTTL),
	)
	if err != nil && errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// StartScheduledStatusPolling enqueues poller tasks at a fixed interval.
func StartScheduledStatusPolling(ctx context.Context, enqueuer *ScheduledStatusEnqueuer, interval time.Duration) {
	if enqueuer == nil {
		return
	}
	if interval <= 0 {
		interval = defaultScheduledPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	_ = enqueuer.EnqueuePoller(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = enqueuer.EnqueuePoller(ctx)
		}
	}
}

// NewPublishScheduledStatusTask builds a task to publish a scheduled status.
func NewPublishScheduledStatusTask(scheduled db.ScheduledStatus) (*asynq.Task, error) {
	if scheduled.ID <= 0 || scheduled.AccountID <= 0 {
		return nil, ErrScheduledStatusPayloadInvalid
	}
	payload := PublishScheduledStatusPayload{
		ID:        scheduled.ID,
		AccountID: scheduled.AccountID,
		Params:    scheduled.Params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypePublishScheduledStatus, body), nil
}

// ScheduledStatusPoller enqueues publish jobs for due scheduled statuses.
type ScheduledStatusPoller struct {
	store    ScheduledStatusStore
	enqueuer ScheduledStatusPublishEnqueuer
}

// ProcessTask enqueues publish jobs for all due scheduled statuses.
func (h *ScheduledStatusPoller) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.store == nil || h.enqueuer == nil {
		return ErrScheduledStatusHandlerMissingDeps
	}
	due, err := h.store.ListDueScheduledStatuses(ctx)
	if err != nil {
		return err
	}
	for _, scheduled := range due {
		if err := h.enqueuer.EnqueuePublish(ctx, scheduled); err != nil {
			return err
		}
	}
	return nil
}

// ScheduledStatusPublisherHandler publishes a scheduled status.
type ScheduledStatusPublisherHandler struct {
	publisher ScheduledStatusPublisher
}

// ProcessTask publishes the scheduled status and removes it from the queue.
func (h *ScheduledStatusPublisherHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.publisher == nil {
		return ErrScheduledStatusHandlerMissingDeps
	}
	if task == nil {
		return fmt.Errorf("missing task: %w", asynq.SkipRetry)
	}
	var payload PublishScheduledStatusPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.ID <= 0 || payload.AccountID <= 0 {
		return fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}

	scheduled := db.ScheduledStatus{
		ID:        payload.ID,
		AccountID: payload.AccountID,
		Params:    payload.Params,
	}
	if err := h.publisher.PublishScheduledStatus(ctx, scheduled); err != nil {
		if errors.Is(err, asynq.SkipRetry) {
			return err
		}
		var statusErr interface {
			StatusCode() int
		}
		if errors.As(err, &statusErr) {
			code := statusErr.StatusCode()
			if code >= 400 && code < 500 {
				return fmt.Errorf("publish scheduled status: %w", asynq.SkipRetry)
			}
		}
		return err
	}
	return nil
}
