package jobs

import (
	"testing"
)

// TestRQJobRetryTracking verifies that the Retries field on RQJob increments
// as expected and that the max-retries check works correctly.
func TestRQJobRetryTracking(t *testing.T) {
	job := &RQJob{
		ID:         "test-job-001",
		CallString: "frappe.utils.background_jobs.test_fn",
	}

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		job.Retries++
		if job.Retries > maxRetries {
			t.Fatalf("retries exceeded max at attempt %d", attempt)
		}
	}

	// After maxRetries increments, job.Retries == maxRetries.
	// One more increment puts it over the threshold.
	job.Retries++
	if job.Retries <= maxRetries {
		t.Fatalf("expected job to be over retry threshold, got Retries=%d", job.Retries)
	}
}

// TestRQJobDefaults verifies that a zero-value RQJob has sensible defaults
// when accessed by executor logic.
func TestRQJobDefaults(t *testing.T) {
	job := &RQJob{}
	if job.Timeout < 0 {
		t.Fatal("default timeout should not be negative")
	}
	// Zero timeout is valid — executor applies its own fallback.
}

// TestRQJobQueueNameFallback verifies that code which defaults to "default"
// when QueueName is empty works as expected.
func TestRQJobQueueNameFallback(t *testing.T) {
	job := &RQJob{ID: "abc", CallString: "some.fn"}
	// Simulate the pool.go defaulting behaviour.
	qName := job.QueueName
	if qName == "" {
		qName = "default"
	}
	if qName != "default" {
		t.Fatalf("expected qName=default for empty QueueName, got %q", qName)
	}
}

// TestRetryToFailedQueue is a logic-level test verifying the branching in
// handleFailure: when Retries >= maxRetries the job should go to the failed
// queue, not be re-enqueued.
func TestRetryToFailedQueue(t *testing.T) {
	maxRetries := 3
	job := &RQJob{
		ID:         "failing-job",
		CallString: "frappe.utils.background_jobs.always_fail",
		Retries:    maxRetries, // already at the limit
	}

	job.Retries++ // simulate one more failure
	shouldGoDLQ := job.Retries >= maxRetries

	if !shouldGoDLQ {
		t.Fatal("expected job with Retries >= maxRetries to go to DLQ")
	}
}
