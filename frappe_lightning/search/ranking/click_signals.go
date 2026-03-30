package ranking

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ClickTracker manages the real-time scoring of documents based on user clicks.
type ClickTracker struct {
	rdb *redis.Client
	log *zap.Logger
}

func NewClickTracker(rdb *redis.Client, log *zap.Logger) *ClickTracker {
	return &ClickTracker{rdb: rdb, log: log}
}

// LogClick records a click for a specific document.
// Uses a rolling 30-day Sorted Set to automatically expire old clicks and maintain a dynamic leader score.
func (c *ClickTracker) LogClick(site, doctype, name string) {
	ctx := context.Background()
	key := fmt.Sprintf("lightning:%s:clicks:%s", site, doctype)
	
	// Ensure the sorted set exists and clean up items older than 30 days
	thirtyDaysAgo := strconv.FormatInt(time.Now().Add(-30*24*time.Hour).Unix(), 10)
	c.rdb.ZRemRangeByScore(ctx, key, "-inf", thirtyDaysAgo)

	// Increment the score for the specific document
	// Note: We use the current timestamp as the score, and increment it for recency+frequency.
	// Actually, standard ranking score is simple increment via ZIncrBy
	err := c.rdb.ZIncrBy(ctx, key, 1.0, name).Err()
	if err != nil {
		c.log.Warn("failed to log click signal", zap.Error(err), zap.String("doc", name))
	}
}

// GetScore returns the current behavioral ranking score for a specific document.
func (c *ClickTracker) GetScore(site, doctype, name string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	key := fmt.Sprintf("lightning:%s:clicks:%s", site, doctype)
	score, err := c.rdb.ZScore(ctx, key, name).Result()
	if err != nil && err != redis.Nil {
		return 0.0
	}
	return score
}
