package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/db"
)

const (
	TaskTypeProcessRemoteAccount = "federation:process_remote_account"

	defaultRemoteAccountMaxRetry = 10
	defaultRemoteAccountTimeout  = 30 * time.Second
)

var (
	ErrRemoteAccountPayloadInvalid     = errors.New("remote account payload invalid")
	ErrRemoteAccountEnqueuerMissing    = errors.New("remote account enqueuer missing")
	ErrRemoteAccountHandlerMissingDeps = errors.New("remote account handler dependencies missing")
)

// ProcessRemoteAccountPayload is the payload for processing remote accounts.
type ProcessRemoteAccountPayload struct {
	ActorURI string `json:"actor_uri"`
}

// RemoteAccountResolver resolves and stores remote accounts.
type RemoteAccountResolver interface {
	ResolveAccount(ctx context.Context, acct string) (db.Account, error)
}

// RemoteAccountRegistrar registers the remote account job handler.
type RemoteAccountRegistrar struct {
	Resolver RemoteAccountResolver
}

// Register wires the remote account job handler into the worker manager.
func (r *RemoteAccountRegistrar) Register(m *Manager) error {
	if r == nil || r.Resolver == nil {
		return ErrRemoteAccountHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	handler := &RemoteAccountHandler{resolver: r.Resolver}
	return m.Handle(TaskTypeProcessRemoteAccount, handler)
}

// NewProcessRemoteAccountTask builds a task for a remote account refresh.
func NewProcessRemoteAccountTask(actorURI string) (*asynq.Task, error) {
	actorURI = strings.TrimSpace(actorURI)
	if actorURI == "" {
		return nil, ErrRemoteAccountPayloadInvalid
	}
	parsed, err := url.ParseRequestURI(actorURI)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrRemoteAccountPayloadInvalid
	}
	payload := ProcessRemoteAccountPayload{ActorURI: actorURI}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeProcessRemoteAccount, body), nil
}

// RemoteAccountEnqueuer enqueues remote account refresh jobs.
type RemoteAccountEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewRemoteAccountEnqueuer creates an enqueuer with defaults.
func NewRemoteAccountEnqueuer(client *asynq.Client) *RemoteAccountEnqueuer {
	return &RemoteAccountEnqueuer{
		client:   client,
		queue:    QueueLow,
		maxRetry: defaultRemoteAccountMaxRetry,
		timeout:  defaultRemoteAccountTimeout,
	}
}

// EnqueueRemoteAccount schedules a remote account refresh job.
func (e *RemoteAccountEnqueuer) EnqueueRemoteAccount(ctx context.Context, actorURI string) error {
	if e == nil || e.client == nil {
		return ErrRemoteAccountEnqueuerMissing
	}
	task, err := NewProcessRemoteAccountTask(actorURI)
	if err != nil {
		return err
	}
	_, err = e.client.Enqueue(task,
		asynq.Queue(e.queue),
		asynq.MaxRetry(e.maxRetry),
		asynq.Timeout(e.timeout),
	)
	return err
}

// RemoteAccountHandler processes remote account refresh jobs.
type RemoteAccountHandler struct {
	resolver RemoteAccountResolver
}

// ProcessTask refreshes a remote account using the resolver.
func (h *RemoteAccountHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil || h == nil || h.resolver == nil {
		return ErrRemoteAccountHandlerMissingDeps
	}

	var payload ProcessRemoteAccountPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	actorURI := strings.TrimSpace(payload.ActorURI)
	if actorURI == "" {
		return fmt.Errorf("missing actor uri: %w", asynq.SkipRetry)
	}
	parsed, err := url.ParseRequestURI(actorURI)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid actor uri: %w", asynq.SkipRetry)
	}

	_, err = h.resolver.ResolveAccount(ctx, actorURI)
	if err != nil {
		return err
	}
	return nil
}
