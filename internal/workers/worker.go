// Package workers provides background job infrastructure.
package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/hibiken/asynq"

	"github.com/deicod/gotodon/internal/config"
)

const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
)

var (
	ErrTaskTypeEmpty       = errors.New("task type is required")
	ErrHandlerNil          = errors.New("task handler is required")
	ErrTaskTypeRegistered  = errors.New("task type already registered")
	ErrWorkersStarted      = errors.New("workers already started")
	ErrRegistrarNil        = errors.New("job registrar is nil")
	ErrWorkerConfigMissing = errors.New("worker configuration is required")
)

// Registrar registers job handlers with a Manager.
type Registrar interface {
	Register(*Manager) error
}

// Manager owns the Asynq server, client, and handler registry.
type Manager struct {
	server *asynq.Server
	client *asynq.Client
	mux    *asynq.ServeMux
	logger *slog.Logger

	mu       sync.Mutex
	started  bool
	handlers map[string]struct{}
}

// NewManager creates a new worker manager.
func NewManager(cfg *config.Config, logger *slog.Logger) (*Manager, error) {
	if cfg == nil {
		return nil, ErrWorkerConfigMissing
	}
	if logger == nil {
		logger = slog.Default()
	}

	redisOpt := asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.Database,
	}
	queueWeights := normalizeQueues(cfg.Workers.Queues)

	server := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.Workers.Concurrency,
		Queues:      queueWeights,
		Logger:      slogAsynqLogger{logger: logger},
	})
	client := asynq.NewClient(redisOpt)

	return &Manager{
		server:   server,
		client:   client,
		mux:      asynq.NewServeMux(),
		logger:   logger,
		handlers: make(map[string]struct{}),
	}, nil
}

// Register invokes job registrars against this manager.
func (m *Manager) Register(registrars ...Registrar) error {
	for _, registrar := range registrars {
		if registrar == nil {
			return ErrRegistrarNil
		}
		if err := registrar.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// Handle registers a handler for a task type.
func (m *Manager) Handle(taskType string, handler asynq.Handler) error {
	if strings.TrimSpace(taskType) == "" {
		return ErrTaskTypeEmpty
	}
	if handler == nil {
		return ErrHandlerNil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return ErrWorkersStarted
	}
	if _, exists := m.handlers[taskType]; exists {
		return ErrTaskTypeRegistered
	}
	m.handlers[taskType] = struct{}{}
	m.mux.Handle(taskType, handler)
	return nil
}

// HandleFunc registers a handler function for a task type.
func (m *Manager) HandleFunc(taskType string, handler func(context.Context, *asynq.Task) error) error {
	if handler == nil {
		return ErrHandlerNil
	}
	return m.Handle(taskType, asynq.HandlerFunc(handler))
}

// Use registers middleware for all tasks.
func (m *Manager) Use(middlewares ...asynq.MiddlewareFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return ErrWorkersStarted
	}
	m.mux.Use(middlewares...)
	return nil
}

// Start begins processing tasks.
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return ErrWorkersStarted
	}
	m.started = true
	m.mu.Unlock()

	if err := m.server.Start(m.mux); err != nil {
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	return nil
}

// Shutdown stops worker processing and closes the client.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		m.server.Shutdown()
		close(done)
	}()

	select {
	case <-ctx.Done():
		m.server.Stop()
		<-done
		return ctx.Err()
	case <-done:
	}

	if m.client != nil {
		if err := m.client.Close(); err != nil {
			return fmt.Errorf("closing worker client: %w", err)
		}
	}
	return nil
}

// Client returns the Asynq client for enqueuing tasks.
func (m *Manager) Client() *asynq.Client {
	if m == nil {
		return nil
	}
	return m.client
}

func normalizeQueues(queues map[string]int) map[string]int {
	normalized := make(map[string]int)
	for name, weight := range queues {
		if strings.TrimSpace(name) == "" || weight <= 0 {
			continue
		}
		normalized[name] = weight
	}
	if len(normalized) == 0 {
		return defaultQueues()
	}
	return normalized
}

func defaultQueues() map[string]int {
	return map[string]int{
		QueueCritical: 6,
		QueueDefault:  3,
		QueueLow:      1,
	}
}

type slogAsynqLogger struct {
	logger *slog.Logger
}

func (l slogAsynqLogger) Debug(args ...interface{}) {
	l.log(slog.LevelDebug, args...)
}

func (l slogAsynqLogger) Info(args ...interface{}) {
	l.log(slog.LevelInfo, args...)
}

func (l slogAsynqLogger) Warn(args ...interface{}) {
	l.log(slog.LevelWarn, args...)
}

func (l slogAsynqLogger) Error(args ...interface{}) {
	l.log(slog.LevelError, args...)
}

func (l slogAsynqLogger) Fatal(args ...interface{}) {
	l.log(slog.LevelError, args...)
	os.Exit(1)
}

func (l slogAsynqLogger) log(level slog.Level, args ...interface{}) {
	if l.logger == nil {
		return
	}
	msg := fmt.Sprint(args...)
	l.logger.Log(context.Background(), level, msg)
}
