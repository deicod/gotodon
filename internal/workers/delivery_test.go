package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
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

func TestDeliveryHandlerSkipsRetryForRefusedInbox(t *testing.T) {
	store := &stubDeliveryStore{
		account: db.Account{
			ID:         7,
			Uri:        "https://local.example/users/alice",
			PrivateKey: pgtype.Text{String: "key", Valid: true},
		},
	}
	tests := []struct {
		name      string
		err       error
		skipRetry bool
	}{
		{name: "forbidden address", err: fmt.Errorf("delivery failed: %w", safehttp.ErrForbiddenAddress), skipRetry: true},
		{name: "unsafe url", err: fmt.Errorf("delivery failed: %w", safehttp.ErrUnsafeURL), skipRetry: true},
		{name: "remote failure", err: errors.New("delivery returned status 503"), skipRetry: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &DeliveryHandler{
				store: store,
				deliver: func(context.Context, db.Account, string, *activitypub.Activity) error {
					return tt.err
				},
			}
			task := newDeliverTask(t, DeliverActivityPayload{
				LocalAccountID: 7,
				Inbox:          "https://remote.example/inbox",
				Activity:       mustMarshalActivity(t, &activitypub.Activity{Type: activitypub.TypeCreate}),
			})
			err := handler.ProcessTask(context.Background(), task)
			if !errors.Is(err, tt.err) {
				t.Fatalf("ProcessTask() error = %v, want %v", err, tt.err)
			}
			if got := errors.Is(err, asynq.SkipRetry); got != tt.skipRetry {
				t.Fatalf("ProcessTask() error = %v, SkipRetry %v, want %v", err, got, tt.skipRetry)
			}
		})
	}
}

func TestDeliveryHandlerRefusesPrivateInbox(t *testing.T) {
	privateKey, _, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	var hits atomic.Int64
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer internal.Close()

	handler := &DeliveryHandler{
		store: &stubDeliveryStore{account: db.Account{
			ID:         7,
			Uri:        "https://local.example/users/alice",
			PrivateKey: pgtype.Text{String: activitypub.EncodePrivateKey(privateKey), Valid: true},
		}},
		deliver: federation.NewDeliveryService().DeliverActivity,
	}
	task := newDeliverTask(t, DeliverActivityPayload{
		LocalAccountID: 7,
		Inbox:          internal.URL + "/inbox",
		Activity:       mustMarshalActivity(t, &activitypub.Activity{Type: activitypub.TypeCreate}),
	})

	err = handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, safehttp.ErrForbiddenAddress) || !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("ProcessTask() error = %v, want ErrForbiddenAddress and SkipRetry", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("private inbox received %d requests, want 0", got)
	}
}
