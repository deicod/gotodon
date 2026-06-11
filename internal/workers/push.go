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
	TaskTypePushDeliver = "push:deliver"

	defaultPushMaxRetry = 5
	defaultPushTimeout  = 30 * time.Second
)

var (
	ErrPushEnqueuerMissing    = errors.New("push enqueuer missing")
	ErrPushHandlerMissingDeps = errors.New("push handler dependencies missing")
)

// PushDeliveryService delivers one notification to one subscription
// (implemented by push.Service).
type PushDeliveryService interface {
	DeliverSubscription(ctx context.Context, subscriptionID, notificationID int64) error
}

// PushRegistrar registers the web push delivery handler.
type PushRegistrar struct {
	Service PushDeliveryService
}

// Register wires the push delivery handler into the worker manager.
func (r *PushRegistrar) Register(m *Manager) error {
	if r == nil || r.Service == nil {
		return ErrPushHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypePushDeliver, &PushDeliveryHandler{service: r.Service})
}

type pushDeliverPayload struct {
	SubscriptionID int64 `json:"subscription_id"`
	NotificationID int64 `json:"notification_id"`
}

// PushEnqueuer queues per-subscription push deliveries. It implements
// push.Deliverer.
type PushEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewPushEnqueuer creates an enqueuer with defaults.
func NewPushEnqueuer(client *asynq.Client) *PushEnqueuer {
	return &PushEnqueuer{
		client:   client,
		queue:    QueueDefault,
		maxRetry: defaultPushMaxRetry,
		timeout:  defaultPushTimeout,
	}
}

// DeliverPush enqueues a push delivery job.
func (e *PushEnqueuer) DeliverPush(ctx context.Context, subscriptionID, notificationID int64) error {
	if e == nil || e.client == nil {
		return ErrPushEnqueuerMissing
	}
	payload, err := json.Marshal(pushDeliverPayload{
		SubscriptionID: subscriptionID,
		NotificationID: notificationID,
	})
	if err != nil {
		return fmt.Errorf("marshal push payload: %w", err)
	}
	task := asynq.NewTask(TaskTypePushDeliver, payload)
	_, err = e.client.EnqueueContext(ctx, task,
		asynq.Queue(e.queue),
		asynq.MaxRetry(e.maxRetry),
		asynq.Timeout(e.timeout),
	)
	return err
}

// PushDeliveryHandler processes queued push deliveries.
type PushDeliveryHandler struct {
	service PushDeliveryService
}

// ProcessTask delivers one queued push notification.
func (h *PushDeliveryHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.service == nil {
		return ErrPushHandlerMissingDeps
	}
	if task == nil {
		return fmt.Errorf("missing task: %w", asynq.SkipRetry)
	}
	var payload pushDeliverPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode push payload: %w", asynq.SkipRetry)
	}
	if payload.SubscriptionID == 0 || payload.NotificationID == 0 {
		return fmt.Errorf("invalid push payload: %w", asynq.SkipRetry)
	}
	return h.service.DeliverSubscription(ctx, payload.SubscriptionID, payload.NotificationID)
}
