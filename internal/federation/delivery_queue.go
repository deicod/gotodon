package federation

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	defaultDeliveryQueueSize       = 1000
	defaultDeliveryWorkerCount     = 4
	defaultDeliveryMaxRetries      = 5
	defaultDeliveryBaseBackoff     = 2 * time.Second
	defaultDeliveryMaxBackoff      = 1 * time.Minute
	defaultDeliveryQueueRetryDelay = 50 * time.Millisecond
)

var (
	ErrDeliveryQueueStopped    = errors.New("delivery queue stopped")
	ErrDeliveryQueueNotStarted = errors.New("delivery queue not started")
	ErrDeliveryQueueFull       = errors.New("delivery queue full")
	ErrDeliveryJobInvalid      = errors.New("delivery job invalid")
)

// DeliveryQueueConfig configures the delivery queue.
type DeliveryQueueConfig struct {
	QueueSize       int
	WorkerCount     int
	MaxRetries      int
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	QueueRetryDelay time.Duration
}

// DefaultDeliveryQueueConfig returns the baseline queue configuration.
func DefaultDeliveryQueueConfig() DeliveryQueueConfig {
	return DeliveryQueueConfig{
		QueueSize:       defaultDeliveryQueueSize,
		WorkerCount:     defaultDeliveryWorkerCount,
		MaxRetries:      defaultDeliveryMaxRetries,
		BaseBackoff:     defaultDeliveryBaseBackoff,
		MaxBackoff:      defaultDeliveryMaxBackoff,
		QueueRetryDelay: defaultDeliveryQueueRetryDelay,
	}
}

// DeliveryQueue handles asynchronous delivery with retries.
type DeliveryQueue struct {
	deliver ActivityDelivery

	queueSize       int
	workerCount     int
	maxRetries      int
	baseBackoff     time.Duration
	maxBackoff      time.Duration
	queueRetryDelay time.Duration

	enqueue chan *deliveryJob
	jobs    chan *deliveryJob
	results chan deliveryResult
	stop    chan struct{}

	started atomic.Bool
	stopped atomic.Bool
	wg      sync.WaitGroup
}

// NewDeliveryQueue creates a delivery queue with the provided delivery function.
func NewDeliveryQueue(deliver ActivityDelivery, cfg DeliveryQueueConfig) *DeliveryQueue {
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = defaultDeliveryQueueSize
	}
	workerCount := cfg.WorkerCount
	if workerCount <= 0 {
		workerCount = defaultDeliveryWorkerCount
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultDeliveryMaxRetries
	}
	baseBackoff := cfg.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = defaultDeliveryBaseBackoff
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultDeliveryMaxBackoff
	}
	queueRetryDelay := cfg.QueueRetryDelay
	if queueRetryDelay <= 0 {
		queueRetryDelay = defaultDeliveryQueueRetryDelay
	}

	return &DeliveryQueue{
		deliver:         deliver,
		queueSize:       queueSize,
		workerCount:     workerCount,
		maxRetries:      maxRetries,
		baseBackoff:     baseBackoff,
		maxBackoff:      maxBackoff,
		queueRetryDelay: queueRetryDelay,
		enqueue:         make(chan *deliveryJob, queueSize),
		jobs:            make(chan *deliveryJob, workerCount),
		results:         make(chan deliveryResult, workerCount),
		stop:            make(chan struct{}),
	}
}

// Start launches the scheduler and worker goroutines.
func (q *DeliveryQueue) Start() {
	if q.started.Swap(true) {
		return
	}
	q.wg.Add(1)
	go q.scheduler()
	for i := 0; i < q.workerCount; i++ {
		q.wg.Add(1)
		go q.worker()
	}
}

// Stop signals the queue to shut down and waits for completion or context cancellation.
func (q *DeliveryQueue) Stop(ctx context.Context) error {
	if !q.started.Load() {
		return nil
	}
	if q.stopped.Swap(true) {
		return nil
	}
	close(q.stop)

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Enqueue schedules a delivery job.
func (q *DeliveryQueue) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	if !q.started.Load() {
		return ErrDeliveryQueueNotStarted
	}
	if q.stopped.Load() {
		return ErrDeliveryQueueStopped
	}
	if inbox == "" || activity == nil || localActor.Uri == "" {
		return ErrDeliveryJobInvalid
	}

	job := &deliveryJob{
		localActor:  localActor,
		inbox:       inbox,
		activity:    activity,
		nextAttempt: time.Now().UTC(),
	}

	select {
	case q.enqueue <- job:
		return nil
	default:
		return ErrDeliveryQueueFull
	}
}

func (q *DeliveryQueue) scheduler() {
	defer q.wg.Done()

	pending := &deliveryJobHeap{}
	heap.Init(pending)

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		var timerC <-chan time.Time
		if pending.Len() > 0 {
			next := (*pending)[0].nextAttempt
			delay := time.Until(next)
			if delay < 0 {
				delay = 0
			}
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				timer.Reset(delay)
			}
			timerC = timer.C
		}

		select {
		case job := <-q.enqueue:
			if job != nil {
				heap.Push(pending, job)
			}
		case result := <-q.results:
			if result.job == nil {
				continue
			}
			if result.err == nil {
				continue
			}
			result.job.attempts++
			if result.job.attempts > q.maxRetries {
				continue
			}
			backoff := q.retryBackoff(result.job.attempts)
			result.job.nextAttempt = time.Now().UTC().Add(backoff)
			heap.Push(pending, result.job)
		case <-timerC:
			now := time.Now().UTC()
			for pending.Len() > 0 {
				next := (*pending)[0]
				if next.nextAttempt.After(now) {
					break
				}
				job := heap.Pop(pending).(*deliveryJob)
				if !q.tryDispatch(job) {
					job.nextAttempt = time.Now().UTC().Add(q.queueRetryDelay)
					heap.Push(pending, job)
					break
				}
			}
		case <-q.stop:
			close(q.jobs)
			return
		}
	}
}

func (q *DeliveryQueue) worker() {
	defer q.wg.Done()

	for job := range q.jobs {
		if job == nil {
			continue
		}
		err := q.deliver(context.Background(), job.localActor, job.inbox, job.activity)
		select {
		case q.results <- deliveryResult{job: job, err: err}:
		case <-q.stop:
			return
		}
	}
}

func (q *DeliveryQueue) retryBackoff(attempts int) time.Duration {
	if attempts <= 0 {
		return q.baseBackoff
	}
	delay := q.baseBackoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= q.maxBackoff {
			return q.maxBackoff
		}
	}
	if delay > q.maxBackoff {
		return q.maxBackoff
	}
	return delay
}

func (q *DeliveryQueue) tryDispatch(job *deliveryJob) bool {
	select {
	case q.jobs <- job:
		return true
	default:
		return false
	}
}

type deliveryJob struct {
	localActor  db.Account
	inbox       string
	activity    *activitypub.Activity
	attempts    int
	nextAttempt time.Time
}

type deliveryResult struct {
	job *deliveryJob
	err error
}

type deliveryJobHeap []*deliveryJob

func (h deliveryJobHeap) Len() int { return len(h) }

func (h deliveryJobHeap) Less(i, j int) bool {
	return h[i].nextAttempt.Before(h[j].nextAttempt)
}

func (h deliveryJobHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *deliveryJobHeap) Push(x any) {
	*h = append(*h, x.(*deliveryJob))
}

func (h *deliveryJobHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
