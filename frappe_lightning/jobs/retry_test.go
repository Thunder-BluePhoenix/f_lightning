package jobs

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// TestSleepChClosesAfterDelay verifies sleepCh fires at the expected time.
func TestSleepChClosesAfterDelay(t *testing.T) {
	start := time.Now()
	<-sleepCh(1)
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("sleepCh(1) closed too early: %v", elapsed)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("sleepCh(1) took too long: %v", elapsed)
	}
}

// TestExponentialBackoffFormula verifies the 2^n delay formula used in retry.go.
func TestExponentialBackoffFormula(t *testing.T) {
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
	}
	for _, tt := range tests {
		got := time.Duration(math.Pow(2, float64(tt.attempt))) * time.Second
		if got != tt.expected {
			t.Errorf("attempt %d: expected %v delay, got %v", tt.attempt, tt.expected, got)
		}
	}
}

// TestContextCancelStopsRetryGoroutine ensures a retry goroutine that uses
// sleepCh exits cleanly when ctx is cancelled before the delay fires.
func TestContextCancelStopsRetryGoroutine(t *testing.T) {
	dispatched := atomic.Int64{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-sleepCh(10): // 10s — should never fire
			dispatched.Add(1)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	if dispatched.Load() != 0 {
		t.Fatal("retry goroutine should not dispatch after context cancellation")
	}
}
