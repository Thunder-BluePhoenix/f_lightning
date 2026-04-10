package gateway

import (
	"context"
	"testing"

	"frappe_lightning/config"
)

// TestResponseCache_RoundTrip verifies that Set followed by Get returns the original response.
func TestResponseCache_RoundTrip(t *testing.T) {
	// Use a no-op in-memory cache to avoid needing a real Redis instance.
	rules := []config.CacheRule{
		{PathPrefix: "/api/", TTLSeconds: 60},
	}

	rc := &inMemoryCache{rules: rules, store: map[string][]byte{}}

	ctx := context.Background()
	site := "erp.local"
	headers := map[string]string{"Content-Type": "application/json"}
	body := []byte(`{"ok":true}`)

	rc.set(ctx, site, "GET", "/api/resource/Customer", "", 200, headers, body)
	cached := rc.get(ctx, site, "GET", "/api/resource/Customer", "")

	if cached == nil {
		t.Fatal("expected cached response, got nil")
	}
	if cached.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", cached.StatusCode)
	}
	if string(cached.Body) != string(body) {
		t.Fatalf("expected body %s, got %s", body, cached.Body)
	}
	if cached.Headers["Content-Type"] != "application/json" {
		t.Fatalf("expected Content-Type header, got %v", cached.Headers)
	}
}

func TestResponseCache_NoopForNonGET(t *testing.T) {
	rules := []config.CacheRule{{PathPrefix: "/api/", TTLSeconds: 60}}
	rc := &inMemoryCache{rules: rules, store: map[string][]byte{}}
	ctx := context.Background()

	// POST should not be cached.
	rc.set(ctx, "erp.local", "POST", "/api/resource/Customer", "", 200,
		map[string]string{}, []byte(`{}`))
	cached := rc.get(ctx, "erp.local", "POST", "/api/resource/Customer", "")
	if cached != nil {
		t.Fatal("expected nil for POST, got a cached response")
	}
}

func TestResponseCache_NoopForNon2xx(t *testing.T) {
	rules := []config.CacheRule{{PathPrefix: "/api/", TTLSeconds: 60}}
	rc := &inMemoryCache{rules: rules, store: map[string][]byte{}}
	ctx := context.Background()

	rc.set(ctx, "erp.local", "GET", "/api/resource/Customer", "", 500,
		map[string]string{}, []byte(`error`))
	cached := rc.get(ctx, "erp.local", "GET", "/api/resource/Customer", "")
	if cached != nil {
		t.Fatal("expected nil for 500 response, got a cached response")
	}
}

func TestResponseCache_NoMatchingRuleReturnsNil(t *testing.T) {
	rules := []config.CacheRule{{PathPrefix: "/api/", TTLSeconds: 60}}
	rc := &inMemoryCache{rules: rules, store: map[string][]byte{}}
	ctx := context.Background()

	rc.set(ctx, "erp.local", "GET", "/assets/js/desk.js", "", 200,
		map[string]string{}, []byte(`/* js */`))
	cached := rc.get(ctx, "erp.local", "GET", "/assets/js/desk.js", "")
	if cached != nil {
		t.Fatal("expected nil for path with no matching cache rule")
	}
}

// TestHeaderEncodeRoundTrip verifies the binary header encoding is lossless.
func TestHeaderEncodeRoundTrip(t *testing.T) {
	original := map[string]string{
		"Content-Type":  "application/json",
		"Cache-Control": "no-store",
	}
	encoded := encodeHeaderBytes(original)
	decoded := parseHeaderBytes(encoded)

	for k, v := range original {
		if decoded[k] != v {
			t.Fatalf("header %q: expected %q, got %q", k, v, decoded[k])
		}
	}
}

// inMemoryCache is a test double for ResponseCache that stores responses in a map
// without needing Redis.
type inMemoryCache struct {
	rules []config.CacheRule
	store map[string][]byte
}

func (rc *inMemoryCache) set(_ context.Context, site, method, path, query string,
	statusCode int, headers map[string]string, body []byte) {
	if method != "GET" || statusCode < 200 || statusCode >= 300 {
		return
	}
	if rc.ttlFor(path) == 0 {
		return
	}
	real := &ResponseCache{rules: rc.rules}
	key := real.key(site, method, path, query)

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
	rc.store[key] = buf
}

func (rc *inMemoryCache) get(_ context.Context, site, method, path, query string) *CachedResponse {
	if method != "GET" {
		return nil
	}
	if rc.ttlFor(path) == 0 {
		return nil
	}
	real := &ResponseCache{rules: rc.rules}
	key := real.key(site, method, path, query)
	data, ok := rc.store[key]
	if !ok || len(data) < 8 {
		return nil
	}
	statusCode := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	headersLen := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
	if len(data) < 8+headersLen {
		return nil
	}
	return &CachedResponse{
		StatusCode: statusCode,
		Headers:    parseHeaderBytes(data[8 : 8+headersLen]),
		Body:       data[8+headersLen:],
	}
}

func (rc *inMemoryCache) ttlFor(path string) int {
	real := &ResponseCache{rules: rc.rules}
	return real.ttlFor(path)
}
