package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DeliveryLog is a persisted record of one delivery attempt.
type DeliveryLog struct {
	DeliveryID   string    `json:"delivery_id"`
	Subscription string    `json:"subscription"`
	DocType      string    `json:"doctype"`
	DocName      string    `json:"doc_name"`
	Event        string    `json:"event"`
	EndpointURL  string    `json:"endpoint_url"`
	AttemptNum   int       `json:"attempt_num"`
	StatusCode   int       `json:"status_code"`
	LatencyMs    int64     `json:"latency_ms"`
	Success      bool      `json:"success"`
	Error        string    `json:"error,omitempty"`
	ResponseBody string    `json:"response_body,omitempty"`
	AttemptedAt  time.Time `json:"attempted_at"`
}

const logListMaxLen int64 = 1000 // keep last 1000 delivery records per site

// Logger writes delivery results to Redis for fast recent-history lookups.
type Logger struct {
	rdb  *redis.Client
	site string
}

// NewLogger creates a Logger.
func NewLogger(rdb *redis.Client, site string) *Logger {
	return &Logger{rdb: rdb, site: site}
}

// Record writes a DeliveryLog entry to Redis.
// Entries are stored in a Redis List capped at logListMaxLen.
func (l *Logger) Record(ctx context.Context, task DeliveryTask, result DeliveryResult) {
	entry := DeliveryLog{
		DeliveryID:   result.DeliveryID,
		Subscription: task.Sub.Name,
		DocType:      task.Event.DocType,
		DocName:      task.Event.Name,
		Event:        task.Event.Event,
		EndpointURL:  task.Sub.EndpointURL,
		AttemptNum:   result.AttemptNum,
		StatusCode:   result.StatusCode,
		LatencyMs:    result.LatencyMs,
		Success:      result.Success,
		Error:        result.Error,
		ResponseBody: result.ResponseBody,
		AttemptedAt:  result.AttemptedAt,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	key := l.listKey()
	pipe := l.rdb.Pipeline()
	pipe.LPush(ctx, key, string(data))
	pipe.LTrim(ctx, key, 0, logListMaxLen-1)
	pipe.Expire(ctx, key, 7*24*time.Hour)
	pipe.Exec(ctx) //nolint:errcheck
}

// Recent returns the last n delivery log entries for this site.
func (l *Logger) Recent(ctx context.Context, n int64) []DeliveryLog {
	items, err := l.rdb.LRange(ctx, l.listKey(), 0, n-1).Result()
	if err != nil {
		return nil
	}
	logs := make([]DeliveryLog, 0, len(items))
	for _, raw := range items {
		var entry DeliveryLog
		if json.Unmarshal([]byte(raw), &entry) == nil {
			logs = append(logs, entry)
		}
	}
	return logs
}

// Get returns the delivery log entry for a specific delivery ID, or nil.
func (l *Logger) Get(ctx context.Context, deliveryID string) *DeliveryLog {
	all := l.Recent(ctx, logListMaxLen)
	for _, entry := range all {
		if entry.DeliveryID == deliveryID {
			return &entry
		}
	}
	return nil
}

func (l *Logger) listKey() string {
	return fmt.Sprintf("lightning:webhook:log:%s", l.site)
}
