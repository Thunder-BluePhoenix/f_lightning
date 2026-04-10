package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RQJob is an RQ-compatible job payload stored in Redis as a JSON blob.
// Frappe enqueues jobs in this format without any changes on its side.
type RQJob struct {
	ID          string                 `json:"id"`
	Description string                 `json:"description"`
	CallString  string                 `json:"call_string"` // e.g. "frappe.email.queue.send_one"
	Args        []interface{}          `json:"args"`
	Kwargs      map[string]interface{} `json:"kwargs"`
	TTL         int                    `json:"ttl"`
	Timeout     int                    `json:"timeout"`
	EnqueuedAt  time.Time              `json:"enqueued_at"`

	// Internal tracking (not persisted in Redis)
	Retries   int    `json:"-"`
	QueueName string `json:"-"`
}

// Consumer pops jobs from RQ-format Redis queues using BLPOP.
type Consumer struct {
	rdb  *redis.Client
	site string
	log  *zap.Logger
}

// NewConsumer creates a Consumer for the given site's Redis client.
func NewConsumer(rdb *redis.Client, site string, log *zap.Logger) *Consumer {
	return &Consumer{rdb: rdb, site: site, log: log}
}

// Pop blocks until a job is available on one of the given queue names,
// fetches its payload from "rq:job:{id}", and returns it.
// Returns nil, nil on timeout or when ctx is cancelled.
func (c *Consumer) Pop(ctx context.Context, queues []string) (*RQJob, error) {
	rqKeys := make([]string, len(queues))
	for i, q := range queues {
		rqKeys[i] = fmt.Sprintf("rq:queue:%s", q)
	}

	result, err := c.rdb.BLPop(ctx, 2*time.Second, rqKeys...).Result()
	if err == redis.Nil || err == context.DeadlineExceeded || err == context.Canceled {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blpop error: %w", err)
	}
	if len(result) < 2 {
		return nil, nil
	}

	queueKey := result[0] // e.g. "rq:queue:default"
	jobID := result[1]

	raw, err := c.rdb.Get(ctx, "rq:job:"+jobID).Bytes()
	if err != nil {
		c.log.Warn("job payload missing", zap.String("id", jobID), zap.Error(err))
		return nil, nil
	}

	var job RQJob
	if err := json.Unmarshal(raw, &job); err != nil {
		c.log.Warn("failed to parse job", zap.String("id", jobID), zap.Error(err))
		return nil, nil
	}

	// Record which queue this job came from.
	for _, q := range queues {
		if queueKey == fmt.Sprintf("rq:queue:%s", q) {
			job.QueueName = q
			break
		}
	}

	return &job, nil
}

// SetStatus updates the job's status field in Redis.
func (c *Consumer) SetStatus(ctx context.Context, jobID, status string, extra ...string) {
	args := []interface{}{"status", status}
	for i := 0; i+1 < len(extra); i += 2 {
		args = append(args, extra[i], extra[i+1])
	}
	c.rdb.HSet(ctx, "rq:job:"+jobID, args...) //nolint:errcheck
}

// PushFailed moves a job to the RQ failed queue.
func (c *Consumer) PushFailed(ctx context.Context, jobID, excInfo string) {
	c.rdb.LPush(ctx, "rq:queue:failed", jobID)                                                                           //nolint:errcheck
	c.rdb.HSet(ctx, "rq:job:"+jobID, "status", "failed", "exc_info", excInfo, "ended_at", time.Now().Unix()) //nolint:errcheck
}

// PushBack re-enqueues a job onto the given queue (used for retries).
func (c *Consumer) PushBack(ctx context.Context, queue, jobID string) {
	c.rdb.LPush(ctx, fmt.Sprintf("rq:queue:%s", queue), jobID) //nolint:errcheck
}

// QueueDepth returns the number of jobs pending in a named queue.
func (c *Consumer) QueueDepth(ctx context.Context, queue string) int64 {
	n, _ := c.rdb.LLen(ctx, fmt.Sprintf("rq:queue:%s", queue)).Result()
	return n
}
