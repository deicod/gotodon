package workers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/db"
)

type stubScheduledStore struct {
	due       []db.ScheduledStatus
	delete    []db.DeleteScheduledStatusParams
	listErr   error
	deleteErr error
}

func (s *stubScheduledStore) ListDueScheduledStatuses(ctx context.Context) ([]db.ScheduledStatus, error) {
	return s.due, s.listErr
}

func (s *stubScheduledStore) DeleteScheduledStatus(ctx context.Context, arg db.DeleteScheduledStatusParams) error {
	s.delete = append(s.delete, arg)
	return s.deleteErr
}

type stubScheduledPublisher struct {
	calls     int
	last      db.ScheduledStatus
	returnErr error
}

func (s *stubScheduledPublisher) PublishScheduledStatus(ctx context.Context, scheduled db.ScheduledStatus) error {
	s.calls++
	s.last = scheduled
	return s.returnErr
}

type stubScheduledEnqueuer struct {
	calls int
	last  db.ScheduledStatus
	err   error
}

func (s *stubScheduledEnqueuer) EnqueuePublish(ctx context.Context, scheduled db.ScheduledStatus) error {
	s.calls++
	s.last = scheduled
	return s.err
}

func TestNewPublishScheduledStatusTask(t *testing.T) {
	task, err := NewPublishScheduledStatusTask(db.ScheduledStatus{ID: 10, AccountID: 5, Params: []byte(`{"status":"hello"}`)})
	if err != nil {
		t.Fatalf("NewPublishScheduledStatusTask error = %v", err)
	}
	var payload PublishScheduledStatusPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.ID != 10 || payload.AccountID != 5 {
		t.Fatalf("payload = %#v, want id 10 account 5", payload)
	}
}

func TestNewPublishScheduledStatusTaskInvalid(t *testing.T) {
	if _, err := NewPublishScheduledStatusTask(db.ScheduledStatus{ID: 0, AccountID: 1}); !errors.Is(err, ErrScheduledStatusPayloadInvalid) {
		t.Fatalf("missing id error = %v", err)
	}
	if _, err := NewPublishScheduledStatusTask(db.ScheduledStatus{ID: 1, AccountID: 0}); !errors.Is(err, ErrScheduledStatusPayloadInvalid) {
		t.Fatalf("missing account id error = %v", err)
	}
}

func TestScheduledStatusEnqueuerValidates(t *testing.T) {
	enqueuer := NewScheduledStatusEnqueuer(nil)
	if err := enqueuer.EnqueuePoller(context.Background()); !errors.Is(err, ErrScheduledStatusEnqueuerMissing) {
		t.Fatalf("nil client error = %v", err)
	}
}

func TestScheduledStatusPollerEnqueues(t *testing.T) {
	store := &stubScheduledStore{
		due: []db.ScheduledStatus{{ID: 1, AccountID: 2}, {ID: 3, AccountID: 4}},
	}
	enqueuer := &stubScheduledEnqueuer{}
	poller := &ScheduledStatusPoller{store: store, enqueuer: enqueuer}

	if err := poller.ProcessTask(context.Background(), asynq.NewTask(TaskTypeScheduledStatusPoller, nil)); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if enqueuer.calls != 2 {
		t.Fatalf("enqueuer.calls = %d, want 2", enqueuer.calls)
	}
	if enqueuer.last.ID != 3 {
		t.Fatalf("enqueuer.last.ID = %d, want 3", enqueuer.last.ID)
	}
}

func TestScheduledStatusPublisherHandler(t *testing.T) {
	publisher := &stubScheduledPublisher{}
	handler := &ScheduledStatusPublisherHandler{publisher: publisher}

	task, err := NewPublishScheduledStatusTask(db.ScheduledStatus{
		ID:        7,
		AccountID: 9,
		Params:    []byte(`{"status":"hello"}`),
	})
	if err != nil {
		t.Fatalf("NewPublishScheduledStatusTask error = %v", err)
	}
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher.calls = %d, want 1", publisher.calls)
	}
	if publisher.last.ID != 7 || publisher.last.AccountID != 9 {
		t.Fatalf("publisher.last = %#v, want id 7 account 9", publisher.last)
	}
}

func TestScheduledStatusPublisherHandlerSkipsInvalidPayload(t *testing.T) {
	handler := &ScheduledStatusPublisherHandler{
		publisher: &stubScheduledPublisher{},
	}
	task := asynq.NewTask(TaskTypePublishScheduledStatus, []byte("bad"))
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask invalid payload error = %v, want SkipRetry", err)
	}
}

type statusCodeError struct {
	code int
}

func (e statusCodeError) Error() string {
	return "status error"
}

func (e statusCodeError) StatusCode() int {
	return e.code
}

func TestScheduledStatusPublisherHandlerSkipsClientErrors(t *testing.T) {
	publisher := &stubScheduledPublisher{returnErr: statusCodeError{code: http.StatusUnprocessableEntity}}
	handler := &ScheduledStatusPublisherHandler{publisher: publisher}
	task, err := NewPublishScheduledStatusTask(db.ScheduledStatus{
		ID:        7,
		AccountID: 9,
		Params:    []byte(`{"status":"hello"}`),
	})
	if err != nil {
		t.Fatalf("NewPublishScheduledStatusTask error = %v", err)
	}
	err = handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask error = %v, want SkipRetry", err)
	}
}
