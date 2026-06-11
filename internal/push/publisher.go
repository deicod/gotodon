package push

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

// Publisher fans a freshly created notification out to the recipient's
// eligible Web Push subscriptions. It is called from the notification
// pipeline and is best-effort: failures are logged, never surfaced to the
// request that created the notification.
type Publisher struct {
	service   *Service
	deliverer Deliverer
	logger    *slog.Logger
}

// NewPublisher creates a notification fan-out publisher.
func NewPublisher(service *Service, deliverer Deliverer, logger *slog.Logger) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{service: service, deliverer: deliverer, logger: logger}
}

// PublishNotification enqueues a push delivery for each of the recipient's
// subscriptions whose alert and policy preferences match the notification.
func (p *Publisher) PublishNotification(ctx context.Context, notification db.Notification) {
	if p == nil || p.service == nil || p.deliverer == nil {
		return
	}
	if !notification.Type.Valid || notification.Type.String == "" {
		return
	}

	user, err := p.service.store.GetUserByAccountID(ctx, notification.AccountID)
	if err != nil {
		// Remote recipients have no local user and no subscriptions.
		if !errors.Is(err, pgx.ErrNoRows) {
			p.logger.Error("Failed to resolve user for push delivery", "account_id", notification.AccountID, "error", err)
		}
		return
	}

	subscriptions, err := p.service.store.ListPushSubscriptionsByUserID(ctx, pgtype.Int8{Int64: user.ID, Valid: true})
	if err != nil {
		p.logger.Error("Failed to list push subscriptions", "user_id", user.ID, "error", err)
		return
	}

	for _, sub := range subscriptions {
		pushable, err := p.service.pushable(ctx, sub, notification)
		if err != nil {
			p.logger.Error("Failed to check push eligibility", "subscription_id", sub.ID, "error", err)
			continue
		}
		if !pushable {
			continue
		}
		if err := p.deliverer.DeliverPush(ctx, sub.ID, notification.ID); err != nil {
			p.logger.Error("Failed to enqueue push delivery", "subscription_id", sub.ID, "notification_id", notification.ID, "error", err)
		}
	}
}

// DirectDeliverer performs push deliveries in-process. It is the fallback
// when the worker queue is unavailable; errors are logged, not returned, and
// deliveries are dropped when the pending queue is saturated.
type DirectDeliverer struct {
	service *Service
	logger  *slog.Logger
	sem     chan struct{}
	pending chan struct{}
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
		pending: make(chan struct{}, 256),
		timeout: defaultHTTPTimeout,
	}
}

// DeliverPush sends the push in the background. The concurrency semaphore is
// acquired inside the goroutine so a slow push service never blocks the
// caller; the pending channel caps total in-flight deliveries so a burst
// cannot pile up goroutines without bound.
func (d *DirectDeliverer) DeliverPush(_ context.Context, subscriptionID, notificationID int64) error {
	if d == nil || d.service == nil {
		return errors.New("push deliverer not configured")
	}
	select {
	case d.pending <- struct{}{}:
	default:
		return errors.New("push delivery queue full")
	}
	go func() {
		defer func() { <-d.pending }()
		d.sem <- struct{}{}
		defer func() { <-d.sem }()
		sendCtx, cancel := context.WithTimeout(context.Background(), d.timeout)
		defer cancel()
		if err := d.service.DeliverSubscription(sendCtx, subscriptionID, notificationID); err != nil {
			d.logger.Error("Failed to deliver push notification", "subscription_id", subscriptionID, "notification_id", notificationID, "error", err)
		}
	}()
	return nil
}
