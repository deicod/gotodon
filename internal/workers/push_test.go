package workers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

type fakePushDeliveryService struct {
	calls [][2]int64
	err   error
}

func (f *fakePushDeliveryService) DeliverSubscription(_ context.Context, subscriptionID, notificationID int64) error {
	f.calls = append(f.calls, [2]int64{subscriptionID, notificationID})
	return f.err
}

func TestPushDeliveryHandlerProcessTask(t *testing.T) {
	service := &fakePushDeliveryService{}
	handler := &PushDeliveryHandler{service: service}

	payload, _ := json.Marshal(pushDeliverPayload{SubscriptionID: 7, NotificationID: 500})
	task := asynq.NewTask(TaskTypePushDeliver, payload)

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(service.calls) != 1 || service.calls[0] != [2]int64{7, 500} {
		t.Errorf("calls = %v", service.calls)
	}
}

func TestPushDeliveryHandlerRejectsBadPayload(t *testing.T) {
	handler := &PushDeliveryHandler{service: &fakePushDeliveryService{}}

	err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypePushDeliver, []byte("not json")))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("expected SkipRetry for invalid JSON, got %v", err)
	}

	empty, _ := json.Marshal(pushDeliverPayload{})
	err = handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypePushDeliver, empty))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("expected SkipRetry for zero IDs, got %v", err)
	}
}

func TestPushDeliveryHandlerPropagatesServiceError(t *testing.T) {
	service := &fakePushDeliveryService{err: errors.New("boom")}
	handler := &PushDeliveryHandler{service: service}

	payload, _ := json.Marshal(pushDeliverPayload{SubscriptionID: 7, NotificationID: 500})
	err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypePushDeliver, payload))
	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Errorf("expected retryable error, got %v", err)
	}
}

func TestPushRegistrarRequiresService(t *testing.T) {
	registrar := &PushRegistrar{}
	if err := registrar.Register(&Manager{mux: asynq.NewServeMux(), handlers: map[string]struct{}{}}); !errors.Is(err, ErrPushHandlerMissingDeps) {
		t.Errorf("expected ErrPushHandlerMissingDeps, got %v", err)
	}
}

func TestPushEnqueuerRequiresClient(t *testing.T) {
	var enqueuer *PushEnqueuer
	if err := enqueuer.DeliverPush(context.Background(), 1, 2); !errors.Is(err, ErrPushEnqueuerMissing) {
		t.Errorf("expected ErrPushEnqueuerMissing, got %v", err)
	}
}
