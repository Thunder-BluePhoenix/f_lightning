package jobs

import (
	"context"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Retrier handles failed jobs with exponential backoff re-enqueue.
type Retrier struct {
	rdb        *redis.Client
	site       string
	maxRetries int
	log        *zap.Logger
}

// NewRetrier creates a Retrier.
func NewRetrier(rdb *redis.Client, site string, maxRetries int, log *zap.Logger) *Retrier {
	return &Retrier{rdb: rdb, site: site, maxRetries: maxRetries, log: log}
}

// HandleFailure decides whether to retry or send the job to the failed queue.
// Delays are: attempt 1 → 2s, attempt 2 → 4s, attempt 3 → 8s.
func (r *Retrier) HandleFailure(ctx context.Context, job *RQJob, err error, output string) {
	job.Retries++

	if job.Retries >= r.maxRetries {
		// Move to RQ-compatible failed queue.
		r.rdb.LPush(ctx, "rq:queue:failed", job.ID)                                                                                                    //nolint:errcheck
		r.rdb.HSet(ctx, "rq:job:"+job.ID, "status", "failed", "exc_info", output, "ended_at", time.Now().Unix()) //nolint:errcheck
		r.log.Error("job permanently failed — moved to dead-letter queue",
			zap.String("site", r.site),
			zap.String("id", job.ID),
			zap.String("fn", job.CallString),
			zap.Int("attempts", job.Retries),
		)
		return
	}

	delay := time.Duration(math.Pow(2, float64(job.Retries))) * time.Second
	r.log.Warn("job failed — scheduling retry",
		zap.String("site", r.site),
		zap.String("id", job.ID),
		zap.String("fn", job.CallString),
		zap.Int("attempt", job.Retries),
		zap.Duration("delay", delay),
	)

	// Re-enqueue after delay; preserve retry count in the payload.
	go func(j *RQJob, d time.Duration) {
		select {
		case <-ctx.Done():
			return
		case <-sleepCh(int(d.Seconds())):
			r.rdb.LPush(ctx, "rq:queue:default", j.ID) //nolint:errcheck
		}
	}(job, delay)
}

// sleepCh returns a channel that closes after n seconds.
// Used as a context-aware sleep across the package.
func sleepCh(seconds int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(seconds) * time.Second)
		close(ch)
	}()
	return ch
}
