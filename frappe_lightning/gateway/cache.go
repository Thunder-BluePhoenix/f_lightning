package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"frappe_lightning/config"

	"github.com/redis/go-redis/v9"
)

// CachedResponse holds the serialised upstream response stored in Redis.
type CachedResponse struct {
	StatusCode  int               `json:"status"`
	Headers     map[string]string `json:"headers"`
	Body        []byte            `json:"body"`
}

// ResponseCache caches upstream GET responses in Redis.
// Only responses with a 2xx status code are cached.
// POST / PUT / DELETE requests automatically skip the cache.
type ResponseCache struct {
	rdb   *redis.Client
	rules []config.CacheRule
}

// NewResponseCache creates a ResponseCache.
func NewResponseCache(rdb *redis.Client, rules []config.CacheRule) *ResponseCache {
	return &ResponseCache{rdb: rdb, rules: rules}
}

// Get attempts to retrieve a cached response.
// Returns nil, nil if the key is not found or the cache is disabled for this path.
func (rc *ResponseCache) Get(ctx context.Context, site, method, path, query string) (*CachedResponse, error) {
	if method != "GET" {
		return nil, nil
	}
	ttl := rc.ttlFor(path)
	if ttl == 0 {
		return nil, nil
	}

	key := rc.key(site, method, path, query)
	data, err := rc.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Decode: status(4 bytes) | headers_len(4 bytes) | headers_json | body
	if len(data) < 8 {
		return nil, nil
	}
	statusCode := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	headersLen := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
	if len(data) < 8+headersLen {
		return nil, nil
	}
	headers := parseHeaderBytes(data[8 : 8+headersLen])
	body := data[8+headersLen:]

	return &CachedResponse{
		StatusCode: statusCode,
		Headers:    headers,
		Body:       body,
	}, nil
}

// Set stores a response in Redis under the computed cache key.
// Silently skips non-2xx responses or paths with no matching TTL rule.
func (rc *ResponseCache) Set(ctx context.Context, site, method, path, query string, statusCode int, headers map[string]string, body []byte) {
	if method != "GET" || statusCode < 200 || statusCode >= 300 {
		return
	}
	ttl := rc.ttlFor(path)
	if ttl == 0 {
		return
	}

	headerBytes := encodeHeaderBytes(headers)
	buf := make([]byte, 8+len(headerBytes)+len(body))

	buf[0] = byte(statusCode >> 24)
	buf[1] = byte(statusCode >> 16)
	buf[2] = byte(statusCode >> 8)
	buf[3] = byte(statusCode)
	buf[4] = byte(len(headerBytes) >> 24)
	buf[5] = byte(len(headerBytes) >> 16)
	buf[6] = byte(len(headerBytes) >> 8)
	buf[7] = byte(len(headerBytes))
	copy(buf[8:], headerBytes)
	copy(buf[8+len(headerBytes):], body)

	key := rc.key(site, method, path, query)
	rc.rdb.Set(ctx, key, buf, time.Duration(ttl)*time.Second) //nolint:errcheck
}

// Invalidate removes all cached entries whose keys share a path prefix.
// Called on POST / PUT / DELETE requests.
func (rc *ResponseCache) Invalidate(ctx context.Context, site, path string) {
	// Derive the invalidation prefix from the matching cache rule.
	for _, r := range rc.rules {
		if strings.HasPrefix(path, r.PathPrefix) {
			pattern := fmt.Sprintf("gw:cache:%s:GET:%s*", site, r.PathPrefix)
			keys, err := rc.rdb.Keys(ctx, pattern).Result()
			if err != nil || len(keys) == 0 {
				return
			}
			rc.rdb.Del(ctx, keys...) //nolint:errcheck
			return
		}
	}
}

// Stats returns cache hit/miss counts for the site (stored as Redis counters).
func (rc *ResponseCache) Stats(ctx context.Context, site string) (hits, misses int64) {
	h, _ := rc.rdb.Get(ctx, "gw:cache:stats:"+site+":hits").Int64()
	m, _ := rc.rdb.Get(ctx, "gw:cache:stats:"+site+":misses").Int64()
	return h, m
}

// RecordHit increments the hit counter asynchronously.
func (rc *ResponseCache) RecordHit(ctx context.Context, site string) {
	rc.rdb.Incr(ctx, "gw:cache:stats:"+site+":hits") //nolint:errcheck
}

// RecordMiss increments the miss counter asynchronously.
func (rc *ResponseCache) RecordMiss(ctx context.Context, site string) {
	rc.rdb.Incr(ctx, "gw:cache:stats:"+site+":misses") //nolint:errcheck
}

// FlushSite deletes all cache entries for a site.
func (rc *ResponseCache) FlushSite(ctx context.Context, site string) (int64, error) {
	pattern := fmt.Sprintf("gw:cache:%s:*", site)
	keys, err := rc.rdb.Keys(ctx, pattern).Result()
	if err != nil || len(keys) == 0 {
		return 0, err
	}
	return rc.rdb.Del(ctx, keys...).Result()
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (rc *ResponseCache) key(site, method, path, query string) string {
	h := sha256.Sum256([]byte(path + "?" + query))
	return fmt.Sprintf("gw:cache:%s:%s:%x", site, method, h[:8])
}

func (rc *ResponseCache) ttlFor(path string) int {
	for _, r := range rc.rules {
		if strings.HasPrefix(path, r.PathPrefix) {
			return r.TTLSeconds
		}
	}
	return 0
}

func encodeHeaderBytes(headers map[string]string) []byte {
	var sb strings.Builder
	for k, v := range headers {
		sb.WriteString(k)
		sb.WriteByte(':')
		sb.WriteString(v)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

func parseHeaderBytes(data []byte) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		m[line[:idx]] = line[idx+1:]
	}
	return m
}
