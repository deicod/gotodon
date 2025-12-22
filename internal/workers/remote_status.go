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
	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/db"
)

const (
	TaskTypeFetchRemoteStatus = "federation:fetch_remote_status"

	defaultRemoteStatusMaxRetry = 8
	defaultRemoteStatusTimeout  = 45 * time.Second
)

var (
	ErrRemoteStatusPayloadInvalid     = errors.New("remote status payload invalid")
	ErrRemoteStatusEnqueuerMissing    = errors.New("remote status enqueuer missing")
	ErrRemoteStatusHandlerMissingDeps = errors.New("remote status handler dependencies missing")
)

// FetchRemoteStatusPayload is the payload for remote status jobs.
type FetchRemoteStatusPayload struct {
	StatusID    int64  `json:"status_id,omitempty"`
	StatusURI   string `json:"status_uri,omitempty"`
	FetchThread bool   `json:"fetch_thread,omitempty"`
}

// RemoteStatusFetcher resolves remote statuses or threads.
type RemoteStatusFetcher interface {
	FetchStatus(ctx context.Context, uri string) (db.Status, error)
	FetchThread(ctx context.Context, status db.Status) error
}

// RemoteStatusLookup loads status records.
type RemoteStatusLookup interface {
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
}

// RemoteStatusRegistrar registers the remote status job handler.
type RemoteStatusRegistrar struct {
	Fetcher RemoteStatusFetcher
	Lookup  RemoteStatusLookup
}

// Register wires the remote status job handler into the worker manager.
func (r *RemoteStatusRegistrar) Register(m *Manager) error {
	if r == nil || r.Fetcher == nil || r.Lookup == nil {
		return ErrRemoteStatusHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	handler := &RemoteStatusHandler{
		fetcher: r.Fetcher,
		lookup:  r.Lookup,
	}
	return m.Handle(TaskTypeFetchRemoteStatus, handler)
}

// NewFetchRemoteStatusTask builds a task to fetch a remote status by URI.
func NewFetchRemoteStatusTask(statusURI string) (*asynq.Task, error) {
	statusURI = strings.TrimSpace(statusURI)
	if statusURI == "" {
		return nil, ErrRemoteStatusPayloadInvalid
	}
	parsed, err := url.ParseRequestURI(statusURI)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrRemoteStatusPayloadInvalid
	}
	payload := FetchRemoteStatusPayload{StatusURI: statusURI}
	return newFetchRemoteStatusTask(payload)
}

// NewFetchRemoteThreadTask builds a task to refresh a remote thread by status ID.
func NewFetchRemoteThreadTask(statusID int64) (*asynq.Task, error) {
	if statusID <= 0 {
		return nil, ErrRemoteStatusPayloadInvalid
	}
	payload := FetchRemoteStatusPayload{StatusID: statusID, FetchThread: true}
	return newFetchRemoteStatusTask(payload)
}

func newFetchRemoteStatusTask(payload FetchRemoteStatusPayload) (*asynq.Task, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeFetchRemoteStatus, body), nil
}

// RemoteStatusEnqueuer enqueues remote status fetch jobs.
type RemoteStatusEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewRemoteStatusEnqueuer creates an enqueuer with defaults.
func NewRemoteStatusEnqueuer(client *asynq.Client) *RemoteStatusEnqueuer {
	return &RemoteStatusEnqueuer{
		client:   client,
		queue:    QueueDefault,
		maxRetry: defaultRemoteStatusMaxRetry,
		timeout:  defaultRemoteStatusTimeout,
	}
}

// EnqueueFetchStatus schedules a remote status fetch by URI.
func (e *RemoteStatusEnqueuer) EnqueueFetchStatus(ctx context.Context, statusURI string) error {
	if e == nil || e.client == nil {
		return ErrRemoteStatusEnqueuerMissing
	}
	task, err := NewFetchRemoteStatusTask(statusURI)
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

// EnqueueFetchThread schedules a remote thread refresh by status ID.
func (e *RemoteStatusEnqueuer) EnqueueFetchThread(ctx context.Context, statusID int64) error {
	if e == nil || e.client == nil {
		return ErrRemoteStatusEnqueuerMissing
	}
	task, err := NewFetchRemoteThreadTask(statusID)
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

// FetchThread implements federation.ThreadFetcher by enqueuing a thread refresh.
func (e *RemoteStatusEnqueuer) FetchThread(ctx context.Context, status db.Status) error {
	if status.ID == 0 {
		return ErrRemoteStatusPayloadInvalid
	}
	if status.Local.Valid && status.Local.Bool {
		return nil
	}
	return e.EnqueueFetchThread(ctx, status.ID)
}

// RemoteStatusHandler processes remote status jobs.
type RemoteStatusHandler struct {
	fetcher RemoteStatusFetcher
	lookup  RemoteStatusLookup
}

// ProcessTask fetches a remote status or thread.
func (h *RemoteStatusHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil || h == nil || h.fetcher == nil || h.lookup == nil {
		return ErrRemoteStatusHandlerMissingDeps
	}

	var payload FetchRemoteStatusPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}

	if payload.FetchThread {
		if payload.StatusID <= 0 {
			return fmt.Errorf("missing status id: %w", asynq.SkipRetry)
		}
		status, err := h.lookup.GetStatusByID(ctx, payload.StatusID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("status missing: %w", asynq.SkipRetry)
			}
			return err
		}
		if status.Local.Valid && status.Local.Bool {
			return nil
		}
		if !status.Uri.Valid || strings.TrimSpace(status.Uri.String) == "" {
			return fmt.Errorf("status uri missing: %w", asynq.SkipRetry)
		}
		return h.fetcher.FetchThread(ctx, status)
	}

	statusURI := strings.TrimSpace(payload.StatusURI)
	if statusURI == "" {
		return fmt.Errorf("status uri missing: %w", asynq.SkipRetry)
	}
	parsed, err := url.ParseRequestURI(statusURI)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid status uri: %w", asynq.SkipRetry)
	}
	status, err := h.fetcher.FetchStatus(ctx, statusURI)
	if err != nil {
		return err
	}
	if payload.FetchThread && status.ID != 0 {
		if err := h.fetcher.FetchThread(ctx, status); err != nil {
			return err
		}
	}
	return nil
}
