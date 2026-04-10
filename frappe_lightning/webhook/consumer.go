package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	streamGroup    = "lightning-webhooks"
	streamMaxLen   = 100_000
	streamReadCount = 50
	streamBlockTime = 2 * time.Second
)

// WebhookEvent is the payload pushed to the Redis Stream by the Python hook.
type WebhookEvent struct {
	Site    string                 `json:"site"`
	DocType string                 `json:"doctype"`
	Name    string                 `json:"name"`
	Event   string                 `json:"event"` // after_insert, on_update, etc.
	Data    map[string]interface{} `json:"data"`
}

// Consumer reads from a Redis Stream using XREADGROUP.
type Consumer struct {
	rdb      *redis.Client
	site     string
	workerID string
	log      *zap.Logger
}

// NewConsumer creates a Consumer for a site and ensures the consumer group exists.
func NewConsumer(rdb *redis.Client, site, workerID string, log *zap.Logger) (*Consumer, error) {
	c := &Consumer{rdb: rdb, site: site, workerID: workerID, log: log}
	if err := c.ensureGroup(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// streamKey returns the Redis Stream key for this site.
func (c *Consumer) streamKey() string {
	return fmt.Sprintf("lightning:webhooks:%s", c.site)
}

// dlqKey returns the Redis List key for the dead-letter queue.
func (c *Consumer) dlqKey() string {
	return fmt.Sprintf("lightning:webhooks:dlq:%s", c.site)
}

// ensureGroup creates the consumer group if it does not already exist.
func (c *Consumer) ensureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, c.streamKey(), streamGroup, "$").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

// ReadBatch blocks until up to streamReadCount events are available, then
// returns them. Returns nil, nil on timeout or context cancellation.
func (c *Consumer) ReadBatch(ctx context.Context) ([]redis.XMessage, error) {
	result, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    streamGroup,
		Consumer: c.workerID,
		Streams:  []string{c.streamKey(), ">"},
		Count:    streamReadCount,
		Block:    streamBlockTime,
	}).Result()

	if err == redis.Nil || err == context.Canceled || err == context.DeadlineExceeded {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(result) == 0 || len(result[0].Messages) == 0 {
		return nil, nil
	}
	return result[0].Messages, nil
}

// Ack acknowledges a message so it is removed from the pending-entry list.
func (c *Consumer) Ack(ctx context.Context, msgID string) {
	c.rdb.XAck(ctx, c.streamKey(), streamGroup, msgID) //nolint:errcheck
}

// ParseEvent deserialises the "payload" field of a stream message.
func ParseEvent(msg redis.XMessage) (*WebhookEvent, error) {
	raw, ok := msg.Values["payload"].(string)
	if !ok {
		return nil, fmt.Errorf("missing payload field in stream message %s", msg.ID)
	}
	var ev WebhookEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		return nil, fmt.Errorf("unmarshal event: %w", err)
	}
	return &ev, nil
}

// PushDLQ writes a failed delivery task to the dead-letter queue.
func (c *Consumer) PushDLQ(ctx context.Context, task DeliveryTask) {
	data, _ := json.Marshal(task)
	c.rdb.LPush(ctx, c.dlqKey(), string(data)) //nolint:errcheck
	c.log.Warn("delivery moved to DLQ",
		zap.String("site", c.site),
		zap.String("delivery_id", task.DeliveryID),
		zap.String("endpoint", task.Sub.EndpointURL),
	)
}

// DLQDepth returns the current dead-letter queue depth.
func (c *Consumer) DLQDepth(ctx context.Context) int64 {
	n, _ := c.rdb.LLen(ctx, c.dlqKey()).Result()
	return n
}

// DLQList returns up to n items from the DLQ without removing them.
func (c *Consumer) DLQList(ctx context.Context, n int64) []DeliveryTask {
	items, err := c.rdb.LRange(ctx, c.dlqKey(), 0, n-1).Result()
	if err != nil {
		return nil
	}
	tasks := make([]DeliveryTask, 0, len(items))
	for _, raw := range items {
		var t DeliveryTask
		if json.Unmarshal([]byte(raw), &t) == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// DLQFlush deletes the entire DLQ for this site.
func (c *Consumer) DLQFlush(ctx context.Context) int64 {
	n, _ := c.rdb.LLen(ctx, c.dlqKey()).Result()
	c.rdb.Del(ctx, c.dlqKey()) //nolint:errcheck
	return n
}
