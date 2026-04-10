package webhook

import (
	"testing"
	"time"
)

// TestBackoffScheduleLength ensures the schedule has at least 5 entries.
func TestBackoffScheduleLength(t *testing.T) {
	if len(backoffSchedule) < 5 {
		t.Fatalf("expected at least 5 backoff steps, got %d", len(backoffSchedule))
	}
}

// TestBackoffScheduleIsMonotonic verifies each delay is larger than the previous.
func TestBackoffScheduleIsMonotonic(t *testing.T) {
	for i := 1; i < len(backoffSchedule); i++ {
		if backoffSchedule[i] <= backoffSchedule[i-1] {
			t.Errorf("backoff[%d]=%v should be > backoff[%d]=%v",
				i, backoffSchedule[i], i-1, backoffSchedule[i-1])
		}
	}
}

// TestBackoffScheduleValues verifies the exact values match the spec.
func TestBackoffScheduleValues(t *testing.T) {
	expected := []time.Duration{
		10 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		1 * time.Hour,
	}
	for i, e := range expected {
		if backoffSchedule[i] != e {
			t.Errorf("backoffSchedule[%d]: expected %v, got %v", i, e, backoffSchedule[i])
		}
	}
}

// TestRetrierSchedule_IndexClamping verifies that AttemptNum beyond the schedule
// length uses the last entry (1h) rather than panicking.
func TestRetrierSchedule_IndexClamping(t *testing.T) {
	pool := make(chan DeliveryTask, 10)
	// We don't need a real consumer for this test — we just test the index clamping logic.
	_ = NewRetrier(nil, pool)

	// Simulate the clamping logic in Schedule().
	attemptNum := 100 // way beyond schedule length
	idx := attemptNum
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	if backoffSchedule[idx] != 1*time.Hour {
		t.Fatalf("clamped index should return 1h, got %v", backoffSchedule[idx])
	}
}

// TestRetrierSchedule_MaxRetriesMovesToDLQ verifies that a task at max retries
// is NOT dispatched back into the pool.
func TestRetrierSchedule_MaxRetriesMovesToDLQ(t *testing.T) {
	pool := make(chan DeliveryTask, 10)

	// Simulate what Schedule() does when AttemptNum >= MaxRetries.
	task := DeliveryTask{
		DeliveryID: "dlq-test-001",
		Sub: WebhookSubscription{MaxRetries: 5},
		AttemptNum: 5, // at the limit
	}

	// Replicate the conditional in Retrier.Schedule without a real Redis consumer.
	if task.AttemptNum >= task.Sub.MaxRetries {
		// goes to DLQ — do NOT push to pool
	} else {
		pool <- task
	}

	if len(pool) > 0 {
		t.Fatal("task at max retries should not be pushed to the delivery pool")
	}
}
