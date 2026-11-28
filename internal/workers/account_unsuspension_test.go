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

var _ AccountUnsuspender = (*services.AccountUnsuspensionService)(nil)

type recordingUnsuspender struct {
	calls []int64
	err   error
}

func (u *recordingUnsuspender) Unsuspend(_ context.Context, accountID int64) error {
	u.calls = append(u.calls, accountID)
	return u.err
}

func TestNewAccountUnsuspensionTask(t *testing.T) {
	task, err := NewAccountUnsuspensionTask(42)
	if err != nil {
		t.Fatalf("NewAccountUnsuspensionTask: %v", err)
	}
	if task.Type() != TaskTypeAccountUnsuspension {
		t.Fatalf("type = %q", task.Type())
	}
	var payload map[string]any
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload) != 1 || payload["account_id"] != float64(42) {
		t.Fatalf("payload = %v", payload)
	}

	if _, err := NewAccountUnsuspensionTask(0); !errors.Is(err, ErrAccountUnsuspensionPayloadInvalid) {
		t.Fatalf("account 0: err = %v, want ErrAccountUnsuspensionPayloadInvalid", err)
	}
}

func TestAccountUnsuspensionHandlerProcessTask(t *testing.T) {
	unsuspender := &recordingUnsuspender{}
	handler := &AccountUnsuspensionHandler{unsuspender: unsuspender}
	task, err := NewAccountUnsuspensionTask(7)
	if err != nil {
		t.Fatalf("NewAccountUnsuspensionTask: %v", err)
	}

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(unsuspender.calls) != 1 || unsuspender.calls[0] != 7 {
		t.Fatalf("calls = %v", unsuspender.calls)
	}

	// A failed unsuspension is retried, not dropped.
	unsuspender.err = errors.New("queue down")
	if err := handler.ProcessTask(context.Background(), task); !errors.Is(err, unsuspender.err) || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("failing unsuspension: err = %v, want the unsuspender's error, retried", err)
	}
}

func TestAccountUnsuspensionHandlerSkipsInvalidPayload(t *testing.T) {
	unsuspender := &recordingUnsuspender{}
	handler := &AccountUnsuspensionHandler{unsuspender: unsuspender}
	for name, payload := range map[string]string{
		"not JSON":      "{",
		"no account ID": `{}`,
		"negative ID":   `{"account_id":-1}`,
	} {
		if err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeAccountUnsuspension, []byte(payload))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: err = %v, want SkipRetry", name, err)
		}
	}
	if err := handler.ProcessTask(context.Background(), nil); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("nil task: err = %v, want SkipRetry", err)
	}
	if len(unsuspender.calls) != 0 {
		t.Fatalf("unsuspended %v for invalid payloads", unsuspender.calls)
	}
	if err := (&AccountUnsuspensionHandler{}).ProcessTask(context.Background(), asynq.NewTask(TaskTypeAccountUnsuspension, nil)); !errors.Is(err, ErrAccountUnsuspensionHandlerMissingDeps) {
		t.Fatalf("no unsuspender: err = %v", err)
	}
}

func TestAccountUnsuspensionRegistrarAndEnqueuerValidate(t *testing.T) {
	if err := (&AccountUnsuspensionRegistrar{}).Register(&Manager{}); !errors.Is(err, ErrAccountUnsuspensionHandlerMissingDeps) {
		t.Fatalf("Register without unsuspender: err = %v", err)
	}
	if err := (&AccountUnsuspensionRegistrar{Unsuspender: &recordingUnsuspender{}}).Register(nil); !errors.Is(err, ErrRegistrarNil) {
		t.Fatalf("Register without manager: err = %v", err)
	}
	var enqueuer *AccountUnsuspensionEnqueuer
	if err := enqueuer.ScheduleAccountUnsuspension(context.Background(), 1); !errors.Is(err, ErrAccountUnsuspensionEnqueuerMissing) {
		t.Fatalf("nil enqueuer: err = %v", err)
	}
	if err := NewAccountUnsuspensionEnqueuer(nil).ScheduleAccountUnsuspension(context.Background(), 1); !errors.Is(err, ErrAccountUnsuspensionEnqueuerMissing) {
		t.Fatalf("enqueuer without client: err = %v", err)
	}
}

// TestAccountUnsuspensionEnqueuerQueues queues the unsuspension of an
// account on Redis: the task waits in its queue with the retry budget and
// timeout of an unsuspension.
func TestAccountUnsuspensionEnqueuerQueues(t *testing.T) {
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
	queue := "test_account_unsuspension_" + rand.Text()
	t.Cleanup(func() {
		_ = inspector.DeleteQueue(queue, true)
		_ = inspector.Close()
	})

	enqueuer := NewAccountUnsuspensionEnqueuer(client)
	if enqueuer.queue != QueueDefault {
		t.Fatalf("queue = %q, want %q", enqueuer.queue, QueueDefault)
	}
	enqueuer.queue = queue
	if err := enqueuer.ScheduleAccountUnsuspension(context.Background(), 42); err != nil {
		t.Fatalf("ScheduleAccountUnsuspension: %v", err)
	}

	tasks, err := inspector.ListPendingTasks(queue)
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("%d pending tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Type != TaskTypeAccountUnsuspension || task.MaxRetry != defaultAccountUnsuspensionMaxRetry || task.Timeout != defaultAccountUnsuspensionTimeout {
		t.Fatalf("task = %s, max retry %d, timeout %v", task.Type, task.MaxRetry, task.Timeout)
	}
	var payload AccountUnsuspensionPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.AccountID != 42 {
		t.Fatalf("payload = %+v", payload)
	}
}
