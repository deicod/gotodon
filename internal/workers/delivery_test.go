package workers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type stubDeliveryStore struct {
	account db.Account
	err     error
	called  bool
}

func (s *stubDeliveryStore) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	s.called = true
	return s.account, s.err
}

func TestNewDeliverActivityTask(t *testing.T) {
	activity := &activitypub.Activity{Type: activitypub.TypeCreate}
	task, err := NewDeliverActivityTask(42, "https://remote.example/inbox", activity)
	if err != nil {
		t.Fatalf("NewDeliverActivityTask error = %v", err)
	}

	var payload DeliverActivityPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload unmarshal error = %v", err)
	}
	if payload.LocalAccountID != 42 {
		t.Fatalf("payload.LocalAccountID = %d, want 42", payload.LocalAccountID)
	}
	if payload.Inbox != "https://remote.example/inbox" {
		t.Fatalf("payload.Inbox = %q, want inbox", payload.Inbox)
	}
	var decoded activitypub.Activity
	if err := json.Unmarshal(payload.Activity, &decoded); err != nil {
		t.Fatalf("activity unmarshal error = %v", err)
	}
	if decoded.Type != activitypub.TypeCreate {
		t.Fatalf("activity.Type = %q, want %q", decoded.Type, activitypub.TypeCreate)
	}
}

func TestNewDeliverActivityTaskInvalid(t *testing.T) {
	activity := &activitypub.Activity{Type: activitypub.TypeCreate}
	if _, err := NewDeliverActivityTask(0, "https://remote.example/inbox", activity); !errors.Is(err, ErrDeliveryPayloadInvalid) {
		t.Fatalf("missing account id error = %v", err)
	}
	if _, err := NewDeliverActivityTask(1, "", activity); !errors.Is(err, ErrDeliveryPayloadInvalid) {
		t.Fatalf("missing inbox error = %v", err)
	}
	if _, err := NewDeliverActivityTask(1, "not-a-url", activity); !errors.Is(err, ErrDeliveryPayloadInvalid) {
		t.Fatalf("invalid inbox error = %v", err)
	}
	if _, err := NewDeliverActivityTask(1, "https://remote.example/inbox", nil); !errors.Is(err, ErrDeliveryPayloadInvalid) {
		t.Fatalf("missing activity error = %v", err)
	}
}

func TestDeliveryEnqueuerValidates(t *testing.T) {
	enqueuer := NewDeliveryEnqueuer(nil)
	if err := enqueuer.Enqueue(db.Account{ID: 1}, "https://remote.example/inbox", &activitypub.Activity{Type: activitypub.TypeCreate}); !errors.Is(err, ErrDeliveryEnqueuerMissing) {
		t.Fatalf("nil client error = %v", err)
	}

	enqueuer = &DeliveryEnqueuer{client: &asynq.Client{}}
	if err := enqueuer.Enqueue(db.Account{}, "https://remote.example/inbox", &activitypub.Activity{Type: activitypub.TypeCreate}); !errors.Is(err, ErrDeliveryPayloadInvalid) {
		t.Fatalf("missing actor id error = %v", err)
	}
}

func TestDeliveryHandlerSkipsInvalidPayload(t *testing.T) {
	handler := &DeliveryHandler{
		store:   &stubDeliveryStore{},
		deliver: func(context.Context, db.Account, string, *activitypub.Activity) error { return nil },
	}
	task := asynq.NewTask(TaskTypeDeliverActivity, []byte("not-json"))
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask invalid payload error = %v, want SkipRetry", err)
	}
}

func TestDeliveryHandlerSkipsMissingActor(t *testing.T) {
	store := &stubDeliveryStore{err: pgx.ErrNoRows}
	handler := &DeliveryHandler{
		store:   store,
		deliver: func(context.Context, db.Account, string, *activitypub.Activity) error { return nil },
	}
	task := newDeliverTask(t, DeliverActivityPayload{
		LocalAccountID: 99,
		Inbox:          "https://remote.example/inbox",
		Activity:       mustMarshalActivity(t, &activitypub.Activity{Type: activitypub.TypeCreate}),
	})
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask missing actor error = %v, want SkipRetry", err)
	}
	if !store.called {
		t.Fatalf("GetAccountByID was not called")
	}
}

func TestDeliveryHandlerSuccess(t *testing.T) {
	store := &stubDeliveryStore{
		account: db.Account{
			ID:         7,
			Uri:        "https://local.example/users/alice",
			PrivateKey: pgtype.Text{String: "key", Valid: true},
		},
	}
	called := false
	handler := &DeliveryHandler{
		store: store,
		deliver: func(_ context.Context, actor db.Account, inbox string, activity *activitypub.Activity) error {
			called = true
			if actor.ID != 7 {
				t.Fatalf("actor ID = %d, want 7", actor.ID)
			}
			if inbox != "https://remote.example/inbox" {
				t.Fatalf("inbox = %q, want remote inbox", inbox)
			}
			if activity.Type != activitypub.TypeCreate {
				t.Fatalf("activity.Type = %q, want Create", activity.Type)
			}
			return nil
		},
	}
	task := newDeliverTask(t, DeliverActivityPayload{
		LocalAccountID: 7,
		Inbox:          "https://remote.example/inbox",
		Activity:       mustMarshalActivity(t, &activitypub.Activity{Type: activitypub.TypeCreate}),
	})
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask error = %v", err)
	}
	if !called {
		t.Fatalf("deliver not called")
	}
}

func newDeliverTask(t *testing.T, payload DeliverActivityPayload) *asynq.Task {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("payload marshal error = %v", err)
	}
	return asynq.NewTask(TaskTypeDeliverActivity, body)
}

func mustMarshalActivity(t *testing.T, activity *activitypub.Activity) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("activity marshal error = %v", err)
	}
	return body
}
