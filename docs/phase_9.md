# Phase 9: Frappe API Gateway

**Goal:** Generalise the auth and rate-limiting infrastructure already built in Phase 3 into a full-featured Go reverse proxy that sits in front of Frappe's Gunicorn workers. Any Frappe HTTP request — not just search — benefits from edge-level auth, caching, rate limiting, circuit breaking, and request routing without touching Python.

---

## Why This Matters

Frappe's Python stack handles every request end-to-end: static assets, REST calls, form loads, file uploads, long-running reports. A Go gateway at the edge can:

- Validate sessions in Redis before Frappe even sees the request — eliminating one Python round-trip per call.
- Cache deterministic responses (GET `/api/resource/...`) in Redis with a configurable TTL — reducing Frappe worker load by 40–70% on read-heavy sites.
- Rate-limit per user or per role, not just per IP — preventing report-abuse from locking out legitimate users.
- Short-circuit failed Frappe workers with a circuit breaker, returning a clean error instead of a timeout.
- Load-balance across multiple `bench` Gunicorn worker groups.

---

## Architecture

```
Client
  │
  ▼
┌──────────────────────────────────────────────────────┐
│  Lightning API Gateway  (Go / Fiber)  :80 / :443     │
│                                                      │
│  ┌───────────────┐  ┌──────────────┐  ┌──────────┐  │
│  │  Auth Edge    │  │  Rate Limit  │  │  Cache   │  │
│  │  (Redis sid)  │  │  (per user)  │  │  (Redis) │  │
│  └───────┬───────┘  └──────┬───────┘  └────┬─────┘  │
│          └─────────────────┴───────────────┘         │
│                            │                         │
│               ┌────────────▼───────────┐             │
│               │   Reverse Proxy Core   │             │
│               │  (circuit breaker,     │             │
│               │   load balancer,       │             │
│               │   request rewriter)    │             │
│               └────────────┬───────────┘             │
└────────────────────────────┼─────────────────────────┘
                             │
              ┌──────────────▼──────────────┐
              │  Frappe Gunicorn Workers    │
              │  worker-1 :8000             │
              │  worker-2 :8001  (optional) │
              └─────────────────────────────┘
```

---

## Components

### 1. Edge Auth Middleware

Reuse and generalise `api/middleware/auth.go`. For any request hitting `/api/`:

- Read `sid` cookie → validate against Redis (`{site}|sessiondata|{sid}`).
- If valid: pass `X-Frappe-User` and `X-Frappe-Roles` headers downstream to Frappe worker.
- If invalid: return `401` immediately — Frappe never sees the request.
- API token (`Authorization: Bearer`) supported for programmatic access.

**Config:**

```yaml
gateway:
  auth:
    skip_paths:
      - /assets/
      - /files/
      - /api/method/login
      - /api/method/frappe.auth.get_logged_user
```

### 2. Per-User Rate Limiter

Extend the existing IP-based limiter to per-user and per-route limits:

```yaml
gateway:
  rate_limits:
    - path_prefix: /api/method/frappe.desk.reportview
      per_user_rps: 5          # slow reports: max 5 per second per user
    - path_prefix: /api/resource/
      per_user_rps: 100        # REST reads: generous limit
    - path_prefix: /api/v1/search
      per_user_rps: 30         # Lightning search: already fast
    - default_per_user_rps: 50
```

Limits tracked in Redis with a sliding window counter:

```go
key := fmt.Sprintf("rl:%s:%s", user, routeKey)
count, _ := rdb.Incr(ctx, key).Result()
rdb.Expire(ctx, key, time.Second)
if count > int64(limit) {
    return c.Status(429).JSON(fiber.Map{"error": "rate limit exceeded"})
}
```

### 3. Response Cache

Cache idempotent GET responses in Redis with configurable TTL per path pattern:

```yaml
gateway:
  cache:
    enabled: true
    rules:
      - path_prefix: /api/resource/DocType
        ttl: 300s          # DocType metadata — rarely changes
      - path_prefix: /api/resource/User
        ttl: 60s
      - path_prefix: /api/method/frappe.desk.search_link
        ttl: 30s
```

Cache key: `gw:cache:{site}:{method}:{path}:{query_hash}`. Invalidated on POST/PUT/DELETE to the same resource prefix.

### 4. Reverse Proxy Core

```go
// Upstream pool per site
type UpstreamPool struct {
    workers []*url.URL
    current uint64       // atomic round-robin counter
}

func (p *UpstreamPool) Next() *url.URL {
    idx := atomic.AddUint64(&p.current, 1) % uint64(len(p.workers))
    return p.workers[idx]
}
```

Proxy behaviour:
- Strips `X-Forwarded-For` spoofing, injects real client IP.
- Forwards original `Host` header.
- Streams response body — no buffering for file downloads.
- 30s proxy timeout with a clean error response on breach.

### 5. Circuit Breaker

Protect upstream Frappe workers from cascading failures:

```go
type CircuitBreaker struct {
    failures    int64
    threshold   int64         // open after N consecutive failures
    openUntil   time.Time
    halfOpenMax int64         // test requests allowed while half-open
}
```

States:
- **Closed** — normal traffic.
- **Open** — immediate `503` returned; no requests hit Frappe worker.
- **Half-Open** — one probe request allowed through every 10s.

```yaml
gateway:
  circuit_breaker:
    failure_threshold: 5
    open_duration: 30s
    half_open_probe_interval: 10s
```

### 6. Request / Response Rewriter

Optional middleware for site-specific transformations:

```yaml
gateway:
  rewrite_rules:
    - match: /api/method/custom.old_endpoint
      redirect: /api/method/custom.new_endpoint
      status: 301
    - match: /old-path/
      strip_prefix: /old-path
      add_prefix: /new-path
```

---

## Config Schema

```yaml
gateway:
  listen_port: 80
  tls_port: 443
  tls_cert: /etc/ssl/certs/frappe.crt
  tls_key:  /etc/ssl/private/frappe.key

  sites:
    - name: erp.local
      upstream_workers:
        - http://127.0.0.1:8000
        - http://127.0.0.1:8001   # optional second worker
      redis:
        host: localhost
        port: 11000

  auth:
    skip_paths:
      - /assets/
      - /files/
      - /api/method/login

  rate_limits:
    - path_prefix: /api/method/frappe.desk.reportview
      per_user_rps: 5
    - default_per_user_rps: 50

  cache:
    enabled: true
    rules:
      - path_prefix: /api/resource/DocType
        ttl: 300s

  circuit_breaker:
    failure_threshold: 5
    open_duration: 30s
```

---

## CLI Commands

```bash
lightning gateway start  --config config.yaml
lightning gateway status --config config.yaml
lightning gateway cache flush --site erp.local
lightning gateway cache stats --site erp.local
lightning gateway ratelimit reset --user admin@example.com --site erp.local
```

---

## Task Checklist

- [ ] Define `gateway` config block and load into `Config` struct
- [ ] Implement `gateway/proxy.go` — reverse proxy core with `UpstreamPool`
- [ ] Implement `gateway/auth.go` — generalised edge auth (reuse from Phase 3)
- [ ] Implement `gateway/ratelimit.go` — per-user Redis sliding window limiter
- [ ] Implement `gateway/cache.go` — Redis response cache with TTL rules
- [ ] Implement `gateway/circuit.go` — circuit breaker with closed/open/half-open states
- [ ] Implement `gateway/rewrite.go` — path redirect and prefix rewrite rules
- [ ] Wire all middleware into a `gateway/server.go` Fiber app
- [ ] Add `lightning gateway` subcommands to the CLI (`start`, `status`, `cache flush`)
- [ ] Add Prometheus metrics: `gateway_requests_total`, `gateway_cache_hit_ratio`, `gateway_upstream_latency_seconds`
- [ ] Write unit tests for rate limiter and circuit breaker state machine
- [ ] Test: cached response returns in <1ms; circuit opens after 5 upstream failures
- [ ] Test: rate limit blocks user after threshold; resets after 1s window
- [ ] Test: auth bypass paths reach upstream without Redis validation

---

## Validation Checklist

- [ ] Frappe Desk loads fully when proxied through the gateway
- [ ] `sid`-invalid requests return `401` before hitting Gunicorn
- [ ] Report endpoint rate-limited at configured RPS per user
- [ ] Cache hit ratio >50% on a standard Frappe Desk session
- [ ] Circuit opens when Gunicorn is killed; closes automatically after recovery
- [ ] TLS terminates at gateway; upstream communication is plain HTTP
- [ ] `lightning gateway cache stats` shows hit/miss counts per route
