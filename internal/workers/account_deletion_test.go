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

var _ AccountDeleter = (*services.AccountDeletionService)(nil)

type recordingDeleter struct {
	calls []AccountDeletionPayload
	err   error
}

func (d *recordingDeleter) Delete(ctx context.Context, accountID int64, opts services.AccountDeletionOptions) error {
	d.calls = append(d.calls, AccountDeletionPayload{AccountID: accountID, Options: opts})
	return d.err
}

func TestNewAccountDeletionTask(t *testing.T) {
	opts := services.AccountDeletionOptions{ReserveUsername: true, SkipActivityPub: true}
	task, err := NewAccountDeletionTask(42, opts)
	if err != nil {
		t.Fatalf("NewAccountDeletionTask: %v", err)
	}
	if task.Type() != TaskTypeAccountDeletion {
		t.Fatalf("type = %q", task.Type())
	}
	var payload map[string]any
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	options, _ := payload["options"].(map[string]any)
	if payload["account_id"] != float64(42) || options["reserve_username"] != true || options["reserve_email"] != false || options["skip_activitypub"] != true {
		t.Fatalf("payload = %v", payload)
	}

	if _, err := NewAccountDeletionTask(0, opts); !errors.Is(err, ErrAccountDeletionPayloadInvalid) {
		t.Fatalf("account 0: err = %v, want ErrAccountDeletionPayloadInvalid", err)
	}
}

func TestAccountDeletionHandlerProcessTask(t *testing.T) {
	deleter := &recordingDeleter{}
	handler := &AccountDeletionHandler{deleter: deleter}
	opts := services.AdminAccountDeletionOptions()
	task, err := NewAccountDeletionTask(7, opts)
	if err != nil {
		t.Fatalf("NewAccountDeletionTask: %v", err)
	}

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if len(deleter.calls) != 1 || deleter.calls[0].AccountID != 7 || deleter.calls[0].Options != opts {
		t.Fatalf("calls = %+v", deleter.calls)
	}

	// A failed deletion is retried, not dropped.
	deleter.err = errors.New("database down")
	if err := handler.ProcessTask(context.Background(), task); !errors.Is(err, deleter.err) || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("failing deletion: err = %v, want the deleter's error, retried", err)
	}
}

func TestAccountDeletionHandlerSkipsInvalidPayload(t *testing.T) {
	deleter := &recordingDeleter{}
	handler := &AccountDeletionHandler{deleter: deleter}
	for name, payload := range map[string]string{
		"not JSON":      "{",
		"no account ID": `{"options":{}}`,
		"negative ID":   `{"account_id":-1}`,
	} {
		if err := handler.ProcessTask(context.Background(), asynq.NewTask(TaskTypeAccountDeletion, []byte(payload))); !errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%s: err = %v, want SkipRetry", name, err)
		}
	}
	if len(deleter.calls) != 0 {
		t.Fatalf("deleted %+v for invalid payloads", deleter.calls)
	}
	if err := (&AccountDeletionHandler{}).ProcessTask(context.Background(), asynq.NewTask(TaskTypeAccountDeletion, nil)); !errors.Is(err, ErrAccountDeletionHandlerMissingDeps) {
		t.Fatalf("no deleter: err = %v", err)
	}
}

func TestAccountDeletionRegistrarAndEnqueuerValidate(t *testing.T) {
	if err := (&AccountDeletionRegistrar{}).Register(&Manager{}); !errors.Is(err, ErrAccountDeletionHandlerMissingDeps) {
		t.Fatalf("Register without deleter: err = %v", err)
	}
	if err := (&AccountDeletionRegistrar{Deleter: &recordingDeleter{}}).Register(nil); !errors.Is(err, ErrRegistrarNil) {
		t.Fatalf("Register without manager: err = %v", err)
	}
	var enqueuer *AccountDeletionEnqueuer
	if err := enqueuer.ScheduleAccountDeletion(context.Background(), 1, services.AdminAccountDeletionOptions()); !errors.Is(err, ErrAccountDeletionEnqueuerMissing) {
		t.Fatalf("nil enqueuer: err = %v", err)
	}
	if err := NewAccountDeletionEnqueuer(nil).ScheduleAccountDeletion(context.Background(), 1, services.AdminAccountDeletionOptions()); !errors.Is(err, ErrAccountDeletionEnqueuerMissing) {
		t.Fatalf("enqueuer without client: err = %v", err)
	}
}

// TestAccountDeletionEnqueuerQueuesOnce queues the deletion of an account
// on Redis twice: one task waits in its queue, with the retry budget and
// timeout of a deletion, and the second request stands on the first.
func TestAccountDeletionEnqueuerQueuesOnce(t *testing.T) {
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
	queue := "test_account_deletion_" + rand.Text()
	t.Cleanup(func() {
		_ = inspector.DeleteQueue(queue, true)
		_ = inspector.Close()
	})

	enqueuer := NewAccountDeletionEnqueuer(client)
	enqueuer.queue = queue
	opts := services.AdminAccountDeletionOptions()
	for i := 0; i < 2; i++ {
		if err := enqueuer.ScheduleAccountDeletion(context.Background(), 42, opts); err != nil {
			t.Fatalf("ScheduleAccountDeletion %d: %v", i+1, err)
		}
	}

	tasks, err := inspector.ListPendingTasks(queue)
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("%d pending tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.Type != TaskTypeAccountDeletion || task.MaxRetry != defaultAccountDeletionMaxRetry || task.Timeout != defaultAccountDeletionTimeout {
		t.Fatalf("task = %s, max retry %d, timeout %v", task.Type, task.MaxRetry, task.Timeout)
	}
	var payload AccountDeletionPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.AccountID != 42 || payload.Options != opts {
		t.Fatalf("payload = %+v", payload)
	}
}
