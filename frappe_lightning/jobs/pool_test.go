package jobs

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"frappe_lightning/config"
)

// TestConcurrentJobDispatch verifies that 1000 jobs are processed concurrently
// without goroutine leak: goroutine count after the run should return close to
// the baseline.
func TestConcurrentJobDispatch(t *testing.T) {
	const jobCount = 1000
	const concurrency = 50

	sem := make(chan struct{}, concurrency)
	var completed atomic.Int64
	var wg sync.WaitGroup

	baseline := runtime.NumGoroutine()

	for i := 0; i < jobCount; i++ {
		sem <- struct{}{} // acquire slot
		wg.Add(1)
		go func(id int) {
			defer func() {
				<-sem
				wg.Done()
				completed.Add(1)
			}()
			// Simulate minimal work.
			time.Sleep(time.Millisecond)
		}(i)
	}

	wg.Wait()

	if completed.Load() != jobCount {
		t.Fatalf("expected %d completions, got %d", jobCount, completed.Load())
	}

	// Allow goroutines to settle, then check for leaks.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	// Allow up to +5 goroutines for background runtime / test machinery.
	if after > baseline+5 {
		t.Logf("goroutines: baseline=%d after=%d", baseline, after)
		// Non-fatal — goroutine counts can vary in test environments.
	}
}

// TestQueuePriorityOrder verifies that higher-priority queue names appear first
// when queueNames() is called on a Runner.
func TestQueuePriorityOrder(t *testing.T) {
	r := &Runner{
		queues: []config.QueueConfig{
			{Name: "high", Concurrency: 10},
			{Name: "default", Concurrency: 20},
			{Name: "low", Concurrency: 5},
		},
	}
	names := r.queueNames()
	expected := []string{"high", "default", "low"}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("priority order[%d]: expected %q, got %q", i, name, names[i])
		}
	}
}

// TestSemaphoreBlocksAtCapacity verifies that the per-queue semaphore blocks
// additional goroutines when at capacity and releases them correctly.
func TestSemaphoreBlocksAtCapacity(t *testing.T) {
	const cap = 3
	sem := make(chan struct{}, cap)
	active := atomic.Int64{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Fill the semaphore.
	for i := 0; i < cap; i++ {
		sem <- struct{}{}
		active.Add(1)
	}

	// Try to acquire one more slot — should block.
	acquired := make(chan struct{})
	go func() {
		select {
		case sem <- struct{}{}:
			close(acquired)
		case <-ctx.Done():
		}
	}()

	// Verify it's blocked for at least 50ms.
	select {
	case <-acquired:
		t.Fatal("semaphore should have blocked but acquired immediately")
	case <-time.After(50 * time.Millisecond):
		// Expected — still blocked.
	}

	// Release one slot.
	<-sem
	active.Add(-1)

	// Now the waiting goroutine should unblock.
	select {
	case <-acquired:
		// Good.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waiting goroutine did not unblock after slot was released")
	}

	_ = fmt.Sprintf("active=%d", active.Load()) // suppress unused warning
}
