package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/services"
)

const (
	TaskTypeLinkCrawler = "links:crawl"

	defaultLinkCrawlerMaxRetry = 4
	defaultLinkCrawlerTimeout  = 60 * time.Second
)

var (
	ErrLinkCrawlerPayloadInvalid     = errors.New("link crawler payload invalid")
	ErrLinkCrawlerEnqueuerMissing    = errors.New("link crawler enqueuer missing")
	ErrLinkCrawlerHandlerMissingDeps = errors.New("link crawler handler dependencies missing")
)

// LinkCrawlerPayload is the payload for link crawling jobs.
type LinkCrawlerPayload struct {
	StatusID int64  `json:"status_id"`
	URL      string `json:"url"`
}

// LinkCrawlerRegistrar registers the link crawler handler.
type LinkCrawlerRegistrar struct {
	Crawler *services.LinkCrawler
}

// Register wires the link crawler handler into the worker manager.
func (r *LinkCrawlerRegistrar) Register(m *Manager) error {
	if r == nil || r.Crawler == nil {
		return ErrLinkCrawlerHandlerMissingDeps
	}
	if m == nil {
		return ErrRegistrarNil
	}
	handler := &LinkCrawlerHandler{crawler: r.Crawler}
	return m.Handle(TaskTypeLinkCrawler, handler)
}

// NewLinkCrawlerTask builds a link crawler task.
func NewLinkCrawlerTask(statusID int64, url string) (*asynq.Task, error) {
	if statusID <= 0 || url == "" {
		return nil, ErrLinkCrawlerPayloadInvalid
	}
	payload := LinkCrawlerPayload{StatusID: statusID, URL: url}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return asynq.NewTask(TaskTypeLinkCrawler, body), nil
}

// LinkCrawlerEnqueuer enqueues link crawler jobs.
type LinkCrawlerEnqueuer struct {
	client   *asynq.Client
	queue    string
	maxRetry int
	timeout  time.Duration
}

// NewLinkCrawlerEnqueuer creates a link crawler enqueuer with defaults.
func NewLinkCrawlerEnqueuer(client *asynq.Client) *LinkCrawlerEnqueuer {
	return &LinkCrawlerEnqueuer{
		client:   client,
		queue:    QueueLow,
		maxRetry: defaultLinkCrawlerMaxRetry,
		timeout:  defaultLinkCrawlerTimeout,
	}
}

// Enqueue schedules a link crawler job.
func (e *LinkCrawlerEnqueuer) Enqueue(ctx context.Context, statusID int64, url string) error {
	if e == nil || e.client == nil {
		return ErrLinkCrawlerEnqueuerMissing
	}
	task, err := NewLinkCrawlerTask(statusID, url)
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

// LinkCrawlerHandler processes link crawler tasks.
type LinkCrawlerHandler struct {
	crawler *services.LinkCrawler
}

// ProcessTask invokes the link crawler.
func (h *LinkCrawlerHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil || h == nil || h.crawler == nil {
		return ErrLinkCrawlerHandlerMissingDeps
	}

	var payload LinkCrawlerPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode payload: %w", asynq.SkipRetry)
	}
	if payload.StatusID <= 0 || payload.URL == "" {
		return fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}

	if err := h.crawler.Crawl(ctx, payload.StatusID, payload.URL); err != nil {
		if errors.Is(err, services.ErrLinkCrawlerSkipRetry) {
			return fmt.Errorf("crawl: %w", asynq.SkipRetry)
		}
		return err
	}
	return nil
}
