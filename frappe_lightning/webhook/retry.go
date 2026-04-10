package webhook

import (
	"context"
	"time"
)

// backoffSchedule defines the delays between retry attempts.
// Attempt 0→1: 10s, 1→2: 30s, 2→3: 2m, 3→4: 10m, 4→5: 1h
var backoffSchedule = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
}

// Retrier schedules retry attempts for failed deliveries.
type Retrier struct {
	consumer *Consumer
	pool     chan<- DeliveryTask
}

// NewRetrier creates a Retrier that re-enqueues failed tasks onto pool.
func NewRetrier(consumer *Consumer, pool chan<- DeliveryTask) *Retrier {
	return &Retrier{consumer: consumer, pool: pool}
}

// Schedule either re-enqueues the task after the appropriate backoff delay,
// or moves it to the dead-letter queue when max retries are exhausted.
func (r *Retrier) Schedule(ctx context.Context, task DeliveryTask) {
	if task.AttemptNum >= task.Sub.MaxRetries {
		r.consumer.PushDLQ(ctx, task)
		return
	}

	idx := task.AttemptNum
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	delay := backoffSchedule[idx]

	go func(t DeliveryTask, d time.Duration) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
			t.AttemptNum++
			select {
			case r.pool <- t:
			case <-ctx.Done():
			}
		}
	}(task, delay)
}
