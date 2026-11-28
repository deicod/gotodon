package workers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/internal/testenv"
)

var _ AccountSuspender = (*services.AccountSuspensionService)(nil)

type recordingSuspender struct {
	calls []int64
	err   error
}

func (u *recordingSuspender) Suspend(_ context.Context, accountID int64) error {
	u.calls = append(u.calls, accountID)
	return u.err
}

func TestNewAccountSuspensionTask(t *testing.T) {
	task, err := NewAccountSuspensionTask(42)
	if err != nil {
		t.Fatalf("NewAccountSuspensionTask: %v", err)
	}
	if task.Type() != TaskTypeAccountSuspension {
		t.Fatalf("type = %q", task.Type())
	}
	var payload map[string]any
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload) != 1 || payload["account_id"] != float64(42) {
		t.Fatalf("payload = %v", payload)
	}

	if _, err := NewAccountSuspensionTask(0); !errors.Is(err, ErrAccountSuspensionPayloadInvalid) {
		t.Fatalf("account 0: err = %v, want ErrAccountSuspensionPayloadInvalid", err)
	}
}

func TestAccountSuspensionHandlerProcessTask(t *testing.T) {
	suspender := &recordingSuspender{}
	handler := &AccountSuspensionHandler{suspender: suspender}
	task, err := NewAccountSuspensionTask(7)
	if err != nil {
		t.Fatalf("NewAccountSuspensionTask: %v", err)
	}

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(suspender.calls) != 1 || suspender.calls[0] != 7 {
		t.Fatalf("calls = %v", suspender.calls)
	}

	// A failed suspension is retried, not dropped.
	suspender.err = errors.New("queue down")
	if err := handler.ProcessTask(context.Background(), task); !errors.Is(err, suspender.err) || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("failing suspension: err = %v, want the suspender's error, retried", err)
	}
}

func TestAccountSuspensionHandlerSkipsInvalidPayload(t *testing.T) {
	suspender := &recordingSuspender{}
	handler := &AccountSuspensionHandler{suspender: suspender}
	for name, payload := range map[string]string{
		"not JSON":      "{",
		"no account ID": `{}`,
		"negative ID":   `{"account_id":-1}`,
	} {
		if err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeAccountSuspension, []byte(payload))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: err = %v, want SkipRetry", name, err)
		}
	}
	if err := handler.ProcessTask(context.Background(), nil); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("nil task: err = %v, want SkipRetry", err)
	}
	if len(suspender.calls) != 0 {
		t.Fatalf("suspended %v for invalid payloads", suspender.calls)
	}
	if err := (&AccountSuspensionHandler{}).ProcessTask(context.Background(), asynq.NewTask(TaskTypeAccountSuspension, nil)); !errors.Is(err, ErrAccountSuspensionHandlerMissingDeps) {
		t.Fatalf("no suspender: err = %v", err)
	}
}

func TestAccountSuspensionRegistrarAndEnqueuerValidate(t *testing.T) {
	if err := (&AccountSuspensionRegistrar{}).Register(&Manager{}); !errors.Is(err, ErrAccountSuspensionHandlerMissingDeps) {
		t.Fatalf("Register without suspender: err = %v", err)
	}
	if err := (&AccountSuspensionRegistrar{Suspender: &recordingSuspender{}}).Register(nil); !errors.Is(err, ErrRegistrarNil) {
		t.Fatalf("Register without manager: err = %v", err)
	}
	var enqueuer *AccountSuspensionEnqueuer
	if err := enqueuer.ScheduleAccountSuspension(context.Background(), 1); !errors.Is(err, ErrAccountSuspensionEnqueuerMissing) {
		t.Fatalf("nil enqueuer: err = %v", err)
	}
	if err := NewAccountSuspensionEnqueuer(nil).ScheduleAccountSuspension(context.Background(), 1); !errors.Is(err, ErrAccountSuspensionEnqueuerMissing) {
		t.Fatalf("enqueuer without client: err = %v", err)
	}
}

// TestAccountSuspensionEnqueuerQueues queues the suspension of an
// account on Redis: the task waits in its queue with the retry budget and
// timeout of an suspension.
func TestAccountSuspensionEnqueuerQueues(t *testing.T) {
	if testing.Short() {
		testenv.SkipService(t, "skipping Redis-backed test in -short mode")
	}
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		testenv.SkipService(t, "REDIS_HOST not set; skipping Redis-backed test")
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	redisOpt := asynq.RedisClientOpt{Addr: net.JoinHostPort(host, port), Password: os.Getenv("REDIS_PASSWORD")}
	client := asynq.NewClient(redisOpt)
	t.Cleanup(func() { _ = client.Close() })
	inspector := asynq.NewInspector(redisOpt)
	queue := "test_account_suspension_" + rand.Text()
	t.Cleanup(func() {
		_ = inspector.DeleteQueue(queue, true)
		_ = inspector.Close()
	})

	enqueuer := NewAccountSuspensionEnqueuer(client)
	if enqueuer.queue != QueueDefault {
		t.Fatalf("queue = %q, want %q", enqueuer.queue, QueueDefault)
	}
	enqueuer.queue = queue
	if err := enqueuer.ScheduleAccountSuspension(context.Background(), 42); err != nil {
		t.Fatalf("ScheduleAccountSuspension: %v", err)
	}

	tasks, err := inspector.ListPendingTasks(queue)
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("%d pending tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Type != TaskTypeAccountSuspension || task.MaxRetry != defaultAccountSuspensionMaxRetry || task.Timeout != defaultAccountSuspensionTimeout {
		t.Fatalf("task = %s, max retry %d, timeout %v", task.Type, task.MaxRetry, task.Timeout)
	}
	var payload AccountSuspensionPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.AccountID != 42 {
		t.Fatalf("payload = %+v", payload)
	}
}
