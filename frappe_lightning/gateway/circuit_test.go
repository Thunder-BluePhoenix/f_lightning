package gateway

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestCB(threshold int, openDur, probeInterval time.Duration) *CircuitBreaker {
	return NewCircuitBreaker(threshold, openDur, probeInterval, zap.NewNop())
}

func TestCircuitBreaker_InitiallyClosed(t *testing.T) {
	cb := newTestCB(5, 30*time.Second, 10*time.Second)
	if !cb.Allow() {
		t.Fatal("expected circuit to be closed initially")
	}
	if cb.State() != "closed" {
		t.Fatalf("expected state=closed, got %s", cb.State())
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := newTestCB(5, 30*time.Second, 10*time.Second)

	for i := 0; i < 5; i++ {
		cb.RecordFailure()
	}

	if cb.State() != "open" {
		t.Fatalf("expected state=open after %d failures, got %s", 5, cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected circuit to block requests when open")
	}
}

func TestCircuitBreaker_DoesNotOpenBeforeThreshold(t *testing.T) {
	cb := newTestCB(5, 30*time.Second, 10*time.Second)

	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}

	if cb.State() != "closed" {
		t.Fatalf("expected state=closed with only 4 failures, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected circuit to allow requests below threshold")
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	// Use a very short open duration so we can test the transition quickly.
	cb := newTestCB(3, 10*time.Millisecond, 1*time.Millisecond)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != "open" {
		t.Fatalf("expected open, got %s", cb.State())
	}

	time.Sleep(20 * time.Millisecond) // let the open duration expire

	// Allow() should trigger the closed→half-open transition.
	allowed := cb.Allow()
	state := cb.State()

	// Either Allow returned true (probe allowed) from half-open,
	// or state is half-open and the next call may return false.
	if state != "half-open" && state != "closed" {
		t.Fatalf("expected half-open or closed after open duration, got %s", state)
	}
	_ = allowed
}

func TestCircuitBreaker_ClosesAfterSuccessfulProbe(t *testing.T) {
	cb := newTestCB(3, 10*time.Millisecond, 1*time.Millisecond)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	time.Sleep(20 * time.Millisecond)
	cb.Allow() // trigger half-open transition

	cb.RecordSuccess()

	if cb.State() != "closed" {
		t.Fatalf("expected closed after success in half-open, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected circuit to allow traffic after closing")
	}
}

func TestCircuitBreaker_ReOpensOnHalfOpenFailure(t *testing.T) {
	cb := newTestCB(3, 10*time.Millisecond, 1*time.Millisecond)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	time.Sleep(20 * time.Millisecond)
	cb.Allow() // trigger half-open

	cb.RecordFailure() // probe fails

	if cb.State() != "open" {
		t.Fatalf("expected re-open after half-open failure, got %s", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := newTestCB(5, 30*time.Second, 10*time.Second)

	// Record 4 failures then a success — should not open.
	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}
	cb.RecordSuccess()

	// Now record 4 more failures — still should not open (count reset).
	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}

	if cb.State() != "closed" {
		t.Fatalf("expected closed after success reset, got %s", cb.State())
	}
}
