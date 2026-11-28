package services

import (
	"testing"
	"time"
)

func TestInProcessRetriesBackoff(t *testing.T) {
	var total time.Duration
	for attempt := 1; attempt < defaultInProcessLifecycleAttempts; attempt++ {
		total += inProcessRetries{}.backoff(attempt)
	}
	if total < 2*time.Hour || total > 4*time.Hour {
		t.Fatalf("default attempts span %v, want about three hours", total)
	}

	retries := inProcessRetries{baseBackoff: time.Second, maxBackoff: 10 * time.Second}
	for attempt, want := range map[int]time.Duration{
		1:   time.Second,
		2:   2 * time.Second,
		4:   8 * time.Second,
		5:   10 * time.Second,
		100: 10 * time.Second,
	} {
		if got := retries.backoff(attempt); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, want)
		}
	}
}

// TestInProcessLifecycleRetriesUseTheirFields checks that the in-process
// runners hand their settings to the retries.
func TestInProcessLifecycleRetriesUseTheirFields(t *testing.T) {
	suspension := &InProcessAccountSuspension{Timeout: time.Second, MaxAttempts: 3, BaseBackoff: 2 * time.Second, MaxBackoff: 4 * time.Second}
	if got, want := suspension.retries(), (inProcessRetries{name: "Account suspension", timeout: time.Second, maxAttempts: 3, baseBackoff: 2 * time.Second, maxBackoff: 4 * time.Second}); got != want {
		t.Fatalf("suspension retries = %+v, want %+v", got, want)
	}
	unsuspension := &InProcessAccountUnsuspension{Timeout: time.Second, MaxAttempts: 3, BaseBackoff: 2 * time.Second, MaxBackoff: 4 * time.Second}
	if got, want := unsuspension.retries(), (inProcessRetries{name: "Account unsuspension", timeout: time.Second, maxAttempts: 3, baseBackoff: 2 * time.Second, maxBackoff: 4 * time.Second}); got != want {
		t.Fatalf("unsuspension retries = %+v, want %+v", got, want)
	}
}
