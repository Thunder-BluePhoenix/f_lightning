package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"frappe_lightning/config"

	"github.com/redis/go-redis/v9"
)

// RateLimiter enforces per-user per-second request limits using a Redis
// sliding-window counter. Each user+route key is incremented on every request
// and expires after one second.
type RateLimiter struct {
	rdb   *redis.Client
	rules []config.RateLimitRule
	// defaultRPS is used when no rule matches the request path.
	defaultRPS int
}

// NewRateLimiter creates a RateLimiter backed by the given Redis client.
// defaultRPS is used when no rule matches (0 = no default limit).
func NewRateLimiter(rdb *redis.Client, rules []config.RateLimitRule, defaultRPS int) *RateLimiter {
	return &RateLimiter{rdb: rdb, rules: rules, defaultRPS: defaultRPS}
}

// Allow returns true if the user is within their rate limit for this path.
// site is included in the Redis key so limits are isolated per tenant.
func (rl *RateLimiter) Allow(ctx context.Context, site, user, path string) bool {
	limit := rl.limitFor(path)
	if limit <= 0 {
		return true // no limit configured
	}

	key := fmt.Sprintf("gw:rl:%s:%s:%s", site, user, rl.routeKey(path))

	// Increment and set a 1-second expiry atomically using a pipeline.
	pipe := rl.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, time.Second)
	pipe.Exec(ctx) //nolint:errcheck

	return incr.Val() <= int64(limit)
}

// limitFor returns the per-second limit for the given path.
// Returns 0 if no rule matches and no default is set.
func (rl *RateLimiter) limitFor(path string) int {
	for _, r := range rl.rules {
		if strings.HasPrefix(path, r.PathPrefix) {
			return r.PerUserRPS
		}
	}
	return rl.defaultRPS
}

// routeKey maps a path to a stable bucket key so all paths under the same
// prefix share one counter. Uses the matched prefix or a hash of the path.
func (rl *RateLimiter) routeKey(path string) string {
	for _, r := range rl.rules {
		if strings.HasPrefix(path, r.PathPrefix) {
			// Sanitise prefix for use as a Redis key segment.
			safe := strings.NewReplacer("/", "_", ".", "_").Replace(strings.Trim(r.PathPrefix, "/"))
			return safe
		}
	}
	// No matching rule — use the first two path segments as a bucket.
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "_" + parts[1]
	}
	return "default"
}
