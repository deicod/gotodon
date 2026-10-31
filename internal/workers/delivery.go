package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
)

const (
	TaskTypeDeliverActivity = "federation:deliver_activity"

	defaultDeliveryMaxRetry = 5
	defaultDeliveryTimeout  = 45 * time.Second
)

var (
	ErrDeliveryPayloadInvalid     = errors.New("delivery payload invalid")
	ErrDeliveryEnqueuerMissing    = errors.New("delivery enqueuer missing")
	ErrDeliveryHandlerMissingDeps = errors.New("delivery handler dependencies missing")
)

// DeliverActivityPayload is the payload for delivery jobs.
type DeliverActivityPayload struct {
	LocalAccountID int64           `json:"local_account_id"`
	Inbox          string          `json:"inbox"`
	Activity       json.RawMessage `json:"activity"`
}

// DeliveryStore loads local actors for delivery.
type DeliveryStore interface {
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
}

// DeliveryRegistrar registers the delivery job handler.
type DeliveryRegistrar struct {
	Store   DeliveryStore
	Deliver federation.ActivityDelivery
}

// Register wires the delivery job handler into the worker manager.
func (r *DeliveryRegistrar) Register(m *Manager) error {
	if r == nil || r.Store == nil || r.Deliver == nil {
		return ErrDeliveryHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	handler := &DeliveryHandler{
		store:   r.Store,
		deliver: r.Deliver,
	}
	return m.Handle(TaskTypeDeliverActivity, handler)
}

// NewDeliverActivityTask builds a delivery task with validation.
func NewDeliverActivityTask(localAccountID int64, inbox string, activity *activitypub.Activity) (*asynq.Task, error) {
	if localAccountID <= 0 {
		return nil, ErrDeliveryPayloadInvalid
	}
	if strings.TrimSpace(inbox) == "" || activity == nil {
		return nil, ErrDeliveryPayloadInvalid
	}
	if _, err := url.ParseRequestURI(inbox); err != nil {
		return nil, ErrDeliveryPayloadInvalid
	}
	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return nil, fmt.Errorf("marshal activity: %w", err)
	}
	payload := DeliverActivityPayload{
		LocalAccountID: localAccountID,
		Inbox:          inbox,
		Activity:       activityJSON,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeDeliverActivity, body), nil
}

// DeliveryEnqueuer enqueues delivery jobs for Asynq.
type DeliveryEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewDeliveryEnqueuer creates a delivery enqueuer using defaults.
func NewDeliveryEnqueuer(client *asynq.Client) *DeliveryEnqueuer {
	return &DeliveryEnqueuer{
		client:   client,
		queue:    QueueCritical,
		maxRetry: defaultDeliveryMaxRetry,
		timeout:  defaultDeliveryTimeout,
	}
}

// Enqueue schedules a delivery job.
func (e *DeliveryEnqueuer) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	if e == nil || e.client == nil {
		return ErrDeliveryEnqueuerMissing
	}
	if localActor.ID == 0 {
		return ErrDeliveryPayloadInvalid
	}
	task, err := NewDeliverActivityTask(localActor.ID, inbox, activity)
	if err != nil {
		return err
	}
	_, err = e.client.Enqueue(task,
		asynq.Queue(e.queue),
		asynq.MaxRetry(e.maxRetry),
		asynq.Timeout(e.timeout),
	)
	return err
}

// DeliveryHandler processes delivery jobs.
type DeliveryHandler struct {
	store   DeliveryStore
	deliver federation.ActivityDelivery
}

// ProcessTask loads the actor, validates payload, and delivers the activity.
func (h *DeliveryHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil || h == nil || h.store == nil || h.deliver == nil {
		return ErrDeliveryHandlerMissingDeps
	}

	var payload DeliverActivityPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.LocalAccountID <= 0 || strings.TrimSpace(payload.Inbox) == "" || len(payload.Activity) == 0 {
		return fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}
	if _, err := url.ParseRequestURI(payload.Inbox); err != nil {
		return fmt.Errorf("invalid inbox: %w", asynq.SkipRetry)
	}

	var activity activitypub.Activity
	if err := json.Unmarshal(payload.Activity, &activity); err != nil {
		return fmt.Errorf("decode activity: %w", asynq.SkipRetry)
	}

	localActor, err := h.store.GetAccountByID(ctx, payload.LocalAccountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("local actor missing: %w", asynq.SkipRetry)
		}
		return fmt.Errorf("load actor: %w", err)
	}
	if localActor.Uri == "" || !localActor.PrivateKey.Valid || localActor.PrivateKey.String == "" {
		return fmt.Errorf("local actor invalid: %w", asynq.SkipRetry)
	}

	if err := h.deliver(ctx, localActor, payload.Inbox, &activity); err != nil {
		// An inbox refused by the address policy stays refused; retrying
		// would only send more requests towards it.
		if safehttp.IsRefused(err) {
			return fmt.Errorf("inbox refused: %w: %w", err, asynq.SkipRetry)
		}
		return err
	}
	return nil
}
