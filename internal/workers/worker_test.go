package workers

import (
	"context"
	"reflect"
	"testing"

	"github.com/hibiken/asynq"
)

func TestNormalizeQueuesDefaults(t *testing.T) {
	got := normalizeQueues(nil)
	want := defaultQueues()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeQueues(nil) = %#v, want %#v", got, want)
	}
}

func TestNormalizeQueuesFiltersInvalid(t *testing.T) {
	input := map[string]int{
		"":         3,
		"low":      -1,
		"default":  0,
		"critical": 4,
	}
	got := normalizeQueues(input)
	want := map[string]int{"critical": 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeQueues(%#v) = %#v, want %#v", input, got, want)
	}
}

func TestNormalizeQueuesFallbackWhenEmpty(t *testing.T) {
	input := map[string]int{
		"default": 0,
	}
	got := normalizeQueues(input)
	want := defaultQueues()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeQueues(%#v) = %#v, want %#v", input, got, want)
	}
}

func TestManagerHandleFuncValidations(t *testing.T) {
	manager := &Manager{
		mux:      asynq.NewServeMux(),
		handlers: make(map[string]struct{}),
	}

	if err := manager.HandleFunc("", func(context.Context, *asynq.Task) error { return nil }); err != ErrTaskTypeEmpty {
		t.Fatalf("HandleFunc empty type = %v, want %v", err, ErrTaskTypeEmpty)
	}

	if err := manager.HandleFunc("jobs:test", nil); err != ErrHandlerNil {
		t.Fatalf("HandleFunc nil handler = %v, want %v", err, ErrHandlerNil)
	}

	if err := manager.HandleFunc("jobs:test", func(context.Context, *asynq.Task) error { return nil }); err != nil {
		t.Fatalf("HandleFunc valid = %v, want nil", err)
	}

	if err := manager.HandleFunc("jobs:test", func(context.Context, *asynq.Task) error { return nil }); err != ErrTaskTypeRegistered {
		t.Fatalf("HandleFunc duplicate = %v, want %v", err, ErrTaskTypeRegistered)
	}

	manager.started = true
	if err := manager.HandleFunc("jobs:after_start", func(context.Context, *asynq.Task) error { return nil }); err != ErrWorkersStarted {
		t.Fatalf("HandleFunc after start = %v, want %v", err, ErrWorkersStarted)
	}
}

func TestManagerRegisterValidations(t *testing.T) {
	manager := &Manager{
		mux:      asynq.NewServeMux(),
		handlers: make(map[string]struct{}),
	}

	if err := manager.Register(nil); err != ErrRegistrarNil {
		t.Fatalf("Register nil registrar = %v, want %v", err, ErrRegistrarNil)
	}
}
