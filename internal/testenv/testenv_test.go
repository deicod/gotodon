package testenv

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// recordingTB stands in for the testing.TB that SkipService is given and
// records whether it was skipped or failed. Like the real one, it ends the
// calling goroutine on either.
type recordingTB struct {
	testing.TB
	skipped bool
	failed  bool
	message string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Skip(args ...any) {
	r.skipped = true
	r.message = fmt.Sprint(args...)
	runtime.Goexit()
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func runSkipService(t *testing.T, reason string) *recordingTB {
	t.Helper()
	rec := &recordingTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		SkipService(rec, reason)
	}()
	<-done
	return rec
}

func TestSkipService(t *testing.T) {
	const reason = "skipping database-backed test; set DATABASE_URL"
	tests := []struct {
		name        string
		value       string
		wantSkipped bool
		wantFailed  bool
		wantMessage string
	}{
		{name: "unset", value: "", wantSkipped: true, wantMessage: reason},
		{name: "false", value: "false", wantSkipped: true, wantMessage: reason},
		{name: "zero", value: "0", wantSkipped: true, wantMessage: reason},
		{name: "one", value: "1", wantFailed: true, wantMessage: RequireServicesVar + " requires"},
		{name: "true", value: "true", wantFailed: true, wantMessage: RequireServicesVar + " requires"},
		{name: "not a boolean", value: "yes please", wantFailed: true, wantMessage: "is not a boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(RequireServicesVar, tt.value)

			rec := runSkipService(t, reason)

			if rec.skipped != tt.wantSkipped || rec.failed != tt.wantFailed {
				t.Fatalf("skipped = %v, failed = %v; want skipped = %v, failed = %v",
					rec.skipped, rec.failed, tt.wantSkipped, tt.wantFailed)
			}
			if !strings.Contains(rec.message, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", rec.message, tt.wantMessage)
			}
			if !strings.Contains(rec.message, reason) {
				t.Errorf("message = %q, want it to keep the reason %q", rec.message, reason)
			}
		})
	}
}
