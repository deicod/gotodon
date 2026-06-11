package push

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type fakeDeliverer struct {
	delivered [][2]int64
}

func (f *fakeDeliverer) DeliverPush(_ context.Context, subscriptionID, notificationID int64) error {
	f.delivered = append(f.delivered, [2]int64{subscriptionID, notificationID})
	return nil
}

func TestPublishNotificationFansOutToMatchingSubscriptions(t *testing.T) {
	store := fixture()
	// A second subscription for the same user without the mention alert.
	store.subscriptions[8] = db.WebPushSubscription{
		ID:        8,
		Endpoint:  "https://push.example.org/send/other",
		KeyP256dh: pgtype.Text{String: "key", Valid: true},
		KeyAuth:   pgtype.Text{String: "auth", Valid: true},
		Data:      []byte(`{"alerts":{"mention":false}}`),
		UserID:    pgtype.Int8{Int64: 10, Valid: true},
	}
	// A subscription belonging to a different user.
	store.subscriptions[9] = db.WebPushSubscription{
		ID:        9,
		Endpoint:  "https://push.example.org/send/elsewhere",
		KeyP256dh: pgtype.Text{String: "key", Valid: true},
		KeyAuth:   pgtype.Text{String: "auth", Valid: true},
		Data:      []byte(`{"alerts":{"mention":true}}`),
		UserID:    pgtype.Int8{Int64: 99, Valid: true},
	}

	deliverer := &fakeDeliverer{}
	service := NewService(store, &fakeSender{status: 200}, testConfig(), nil)
	publisher := NewPublisher(service, deliverer, nil)

	publisher.PublishNotification(context.Background(), store.notifications[500])

	if len(deliverer.delivered) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliverer.delivered))
	}
	if deliverer.delivered[0] != [2]int64{7, 500} {
		t.Errorf("delivered = %v, want [7 500]", deliverer.delivered[0])
	}
}

func TestPublishNotificationSkipsRemoteRecipients(t *testing.T) {
	store := fixture()
	notification := store.notifications[500]
	notification.AccountID = 42 // no local user
	store.notifications[500] = notification

	deliverer := &fakeDeliverer{}
	service := NewService(store, &fakeSender{status: 200}, testConfig(), nil)
	publisher := NewPublisher(service, deliverer, nil)

	publisher.PublishNotification(context.Background(), notification)

	if len(deliverer.delivered) != 0 {
		t.Errorf("expected no deliveries for remote recipient, got %d", len(deliverer.delivered))
	}
}

func TestPublishNotificationIgnoresUntypedNotifications(t *testing.T) {
	store := fixture()
	deliverer := &fakeDeliverer{}
	service := NewService(store, &fakeSender{status: 200}, testConfig(), nil)
	publisher := NewPublisher(service, deliverer, nil)

	publisher.PublishNotification(context.Background(), db.Notification{ID: 1, AccountID: 1})

	if len(deliverer.delivered) != 0 {
		t.Errorf("expected no deliveries for untyped notification")
	}
}
