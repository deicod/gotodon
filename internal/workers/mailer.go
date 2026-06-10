package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/mailer"
)

const (
	TaskTypeMailDeliver = "mailer:deliver"

	defaultMailMaxRetry = 5
	defaultMailTimeout  = 60 * time.Second
)

var (
	ErrMailEnqueuerMissing    = errors.New("mail enqueuer missing")
	ErrMailHandlerMissingDeps = errors.New("mail handler dependencies missing")
)

// MailService renders and sends a mail (implemented by mailer.Service).
type MailService interface {
	RenderAndSend(ctx context.Context, mail mailer.Mail) error
}

// MailRegistrar registers the mail delivery handler.
type MailRegistrar struct {
	Service MailService
}

// Register wires the mail delivery handler into the worker manager.
func (r *MailRegistrar) Register(m *Manager) error {
	if r == nil || r.Service == nil {
		return ErrMailHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	return m.Handle(TaskTypeMailDeliver, &MailDeliveryHandler{service: r.Service})
}

// MailEnqueuer queues mail delivery jobs. It implements mailer.Deliverer.
type MailEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewMailEnqueuer creates an enqueuer with defaults.
func NewMailEnqueuer(client *asynq.Client) *MailEnqueuer {
	return &MailEnqueuer{
		client:   client,
		queue:    QueueDefault,
		maxRetry: defaultMailMaxRetry,
		timeout:  defaultMailTimeout,
	}
}

// Deliver enqueues a mail delivery job.
func (e *MailEnqueuer) Deliver(ctx context.Context, mail mailer.Mail) error {
	if e == nil || e.client == nil {
		return ErrMailEnqueuerMissing
	}
	if err := mail.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(mail)
	if err != nil {
		return fmt.Errorf("marshal mail payload: %w", err)
	}
	task := asynq.NewTask(TaskTypeMailDeliver, payload)
	_, err = e.client.EnqueueContext(ctx, task,
		asynq.Queue(e.queue),
		asynq.MaxRetry(e.maxRetry),
		asynq.Timeout(e.timeout),
	)
	return err
}

// MailDeliveryHandler renders and sends queued mails.
type MailDeliveryHandler struct {
	service MailService
}

// ProcessTask renders and sends one mail.
func (h *MailDeliveryHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if h == nil || h.service == nil {
		return ErrMailHandlerMissingDeps
	}
	if task == nil {
		return fmt.Errorf("missing task: %w", asynq.SkipRetry)
	}
	var mail mailer.Mail
	if err := json.Unmarshal(task.Payload(), &mail); err != nil {
		return fmt.Errorf("decode mail payload: %w", asynq.SkipRetry)
	}
	if err := mail.Validate(); err != nil {
		return fmt.Errorf("invalid mail payload: %v: %w", err, asynq.SkipRetry)
	}
	return h.service.RenderAndSend(ctx, mail)
}
