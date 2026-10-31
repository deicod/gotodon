package federation

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
)

func TestDeliveryQueue_RetriesThenSucceeds(t *testing.T) {
	var attempts int32
	done := make(chan struct{})

	deliver := func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return errors.New("retry")
		}
		select {
		case <-done:
		default:
			close(done)
		}
		return nil
	}

	queue := NewDeliveryQueue(deliver, DeliveryQueueConfig{
		WorkerCount: 1,
		MaxRetries:  5,
		BaseBackoff: 5 * time.Millisecond,
		MaxBackoff:  20 * time.Millisecond,
		QueueSize:   10,
	})
	queue.Start()
	defer func() {
		_ = queue.Stop(context.Background())
	}()

	local := db.Account{
		Uri:        "https://local.test/users/alice",
		PrivateKey: pgtype.Text{String: "key", Valid: true},
	}
	activity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          local.Uri,
		ActivityObject: "https://remote.example/users/bob",
	}

	if err := queue.Enqueue(local, "https://remote.example/inbox", activity); err != nil {
		t.Fatalf("Enqueue error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for success")
	}

	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestDeliveryQueue_DropsAfterMaxRetries(t *testing.T) {
	var attempts int32

	deliver := func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("fail")
	}

	queue := NewDeliveryQueue(deliver, DeliveryQueueConfig{
		WorkerCount: 1,
		MaxRetries:  1,
		BaseBackoff: 5 * time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
		QueueSize:   10,
	})
	queue.Start()
	defer func() {
		_ = queue.Stop(context.Background())
	}()

	local := db.Account{
		Uri:        "https://local.test/users/alice",
		PrivateKey: pgtype.Text{String: "key", Valid: true},
	}
	activity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          local.Uri,
		ActivityObject: "https://remote.example/users/bob",
	}

	if err := queue.Enqueue(local, "https://remote.example/inbox", activity); err != nil {
		t.Fatalf("Enqueue error: %v", err)
	}

	timeout := time.After(500 * time.Millisecond)
	for atomic.LoadInt32(&attempts) < 2 {
		select {
		case <-timeout:
			t.Fatalf("timeout waiting for retries, attempts=%d", atomic.LoadInt32(&attempts))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestDeliveryQueue_DoesNotRetryRefusedInbox(t *testing.T) {
	for _, refusal := range []error{safehttp.ErrForbiddenAddress, safehttp.ErrUnsafeURL} {
		t.Run(refusal.Error(), func(t *testing.T) {
			var attempts int32
			deliver := func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
				atomic.AddInt32(&attempts, 1)
				return fmt.Errorf("delivery failed: %w", refusal)
			}

			queue := NewDeliveryQueue(deliver, DeliveryQueueConfig{
				WorkerCount: 1,
				MaxRetries:  5,
				BaseBackoff: 5 * time.Millisecond,
				MaxBackoff:  5 * time.Millisecond,
				QueueSize:   10,
			})
			queue.Start()
			defer func() {
				_ = queue.Stop(context.Background())
			}()

			local := db.Account{
				Uri:        "https://local.test/users/alice",
				PrivateKey: pgtype.Text{String: "key", Valid: true},
			}
			activity := &activitypub.Activity{
				Type:           activitypub.TypeFollow,
				Actor:          local.Uri,
				ActivityObject: "https://remote.example/users/bob",
			}
			if err := queue.Enqueue(local, "http://10.0.0.1/inbox", activity); err != nil {
				t.Fatalf("Enqueue error: %v", err)
			}

			timeout := time.After(500 * time.Millisecond)
			for atomic.LoadInt32(&attempts) < 1 {
				select {
				case <-timeout:
					t.Fatal("timeout waiting for the delivery attempt")
				default:
					time.Sleep(5 * time.Millisecond)
				}
			}

			// Several backoff periods pass without another attempt.
			time.Sleep(50 * time.Millisecond)
			if got := atomic.LoadInt32(&attempts); got != 1 {
				t.Fatalf("attempts = %d, want 1", got)
			}
		})
	}
}

func TestDeliveryQueue_EnqueueValidates(t *testing.T) {
	queue := NewDeliveryQueue(func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
		return nil
	}, DeliveryQueueConfig{})

	if err := queue.Enqueue(db.Account{}, "https://remote.example/inbox", &activitypub.Activity{}); err != ErrDeliveryQueueNotStarted {
		t.Fatalf("expected ErrDeliveryQueueNotStarted, got %v", err)
	}

	queue.Start()
	defer func() {
		_ = queue.Stop(context.Background())
	}()

	if err := queue.Enqueue(db.Account{}, "https://remote.example/inbox", &activitypub.Activity{}); err != ErrDeliveryJobInvalid {
		t.Fatalf("expected ErrDeliveryJobInvalid, got %v", err)
	}
	if err := queue.Enqueue(db.Account{Uri: "https://local.test/users/alice"}, "", &activitypub.Activity{}); err != ErrDeliveryJobInvalid {
		t.Fatalf("expected ErrDeliveryJobInvalid for missing inbox, got %v", err)
	}
	if err := queue.Enqueue(db.Account{Uri: "https://local.test/users/alice"}, "https://remote.example/inbox", nil); err != ErrDeliveryJobInvalid {
		t.Fatalf("expected ErrDeliveryJobInvalid for missing activity, got %v", err)
	}
}
