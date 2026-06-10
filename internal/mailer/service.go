package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// InstanceProvider resolves the instance branding used in mail templates.
// It is called per send so runtime changes to the site title are picked up.
type InstanceProvider func(ctx context.Context) InstanceInfo

// Service renders mails and hands them to a Sender.
type Service struct {
	sender   Sender
	from     string
	instance InstanceProvider
	now      func() time.Time
}

// NewService creates a mail rendering/sending service.
func NewService(sender Sender, from string, instance InstanceProvider) *Service {
	return &Service{
		sender:   sender,
		from:     from,
		instance: instance,
		now:      time.Now,
	}
}

// RenderAndSend renders the mail for its kind/locale and transmits it.
func (s *Service) RenderAndSend(ctx context.Context, mail Mail) error {
	if s == nil || s.sender == nil {
		return ErrMailNotConfigured
	}
	if err := mail.Validate(); err != nil {
		return err
	}
	instance := InstanceInfo{Title: "Gotodon", Domain: "localhost"}
	if s.instance != nil {
		instance = s.instance(ctx)
	}
	subject, body, err := Render(mail, instance)
	if err != nil {
		return err
	}
	message, err := BuildMessage(s.from, mail.To, subject, body, s.now())
	if err != nil {
		return err
	}
	if err := s.sender.Send(ctx, mail.To, message); err != nil {
		return fmt.Errorf("sending %s mail: %w", mail.Kind, err)
	}
	return nil
}

// DirectDeliverer sends mail asynchronously in-process. It is the fallback
// when the worker queue is unavailable; errors are logged, not returned.
type DirectDeliverer struct {
	service *Service
	logger  *slog.Logger
	sem     chan struct{}
	timeout time.Duration
}

// NewDirectDeliverer creates an in-process deliverer with bounded concurrency.
func NewDirectDeliverer(service *Service, logger *slog.Logger) *DirectDeliverer {
	if logger == nil {
		logger = slog.Default()
	}
	return &DirectDeliverer{
		service: service,
		logger:  logger,
		sem:     make(chan struct{}, 4),
		timeout: defaultSendTimeout,
	}
}

// Deliver validates the mail synchronously and sends it in the background.
// The semaphore is acquired inside the goroutine so a saturated sender never
// blocks the calling request.
func (d *DirectDeliverer) Deliver(ctx context.Context, mail Mail) error {
	if d == nil || d.service == nil {
		return ErrMailNotConfigured
	}
	if err := mail.Validate(); err != nil {
		return err
	}
	go func() {
		d.sem <- struct{}{}
		defer func() { <-d.sem }()
		sendCtx, cancel := context.WithTimeout(context.Background(), d.timeout)
		defer cancel()
		if err := d.service.RenderAndSend(sendCtx, mail); err != nil {
			d.logger.Error("Failed to deliver mail", "kind", mail.Kind, "error", err)
		}
	}()
	return nil
}
