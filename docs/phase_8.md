# Phase 8: Dev Tools, Production Hardening & Universal API

**Goal:** Make Frappe Lightning production-ready for real companies and extensible as a platform. This phase covers CLI tools, production-grade event queues, a public REST API, observability, and the foundations for the "Frappe Data Engine" vision.

---

## 1. Lightning CLI

A single `lightning` binary for every operational need:

```bash
# Reindex
lightning reindex --doctype="Sales Invoice" --site=erp.local
lightning reindex --all --site=erp.local --batch=500

# Diagnostics
lightning diff --doctype="Sales Invoice" --site=erp.local
# → "MariaDB: 45,231 | Meilisearch: 45,198 | Missing: 33"

# NLP tester
lightning parse "unpaid invoices last month above 10k"
# → { doctype: "Sales Invoice", filters: [...], text: "" }

# Position management
lightning show-position --site=erp.local
lightning reset-position --site=erp.local  # ⚠ re-processes all events

# Status
lightning status --site=erp.local
lightning health
```

---

## 2. Live Binlog Viewer

Stream raw binlog events to terminal in real-time:

```bash
lightning watch --site=erp.local

# Output:
# [01:15:33] INSERT  tabCustomer         name=CUST-00123  customer_name="Acme Corp"
# [01:15:34] UPDATE  tabSales Invoice    name=ACC-SINV-2026-00124  status: Draft→Submitted
# [01:15:40] DELETE  tabItem             name=ITEM-OLD-001
```

Invaluable during development and when debugging sync lag issues.

---

## 3. Index Diff Checker

Compare MariaDB row counts vs Meilisearch document counts — identify missing or stale docs:

```go
// cmd/diff.go
func DiffCmd(db *sql.DB, meili meilisearch.ServiceManager, schema config.DocTypeSchema, site string) {
    var dbCount int
    db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", schema.Table)).Scan(&dbCount)

    stats, _ := meili.Index(fmt.Sprintf("%s_%s", site, schema.IndexSuffix)).GetStats()

    missing := dbCount - int(stats.NumberOfDocuments)
    fmt.Printf("MariaDB: %d | Meilisearch: %d | Missing: %d\n", dbCount, stats.NumberOfDocuments, missing)
    if missing > 0 {
        fmt.Printf("→ Run: lightning reindex --doctype='%s' --site=%s\n", schema.Name, site)
    }
}
```

---

## 4. Latency Profiler

Per-request timing breakdown for every search:

```go
type RequestTrace struct {
    SessionValidationMs int `json:"session_ms"`
    NLPParseMs          int `json:"nlp_ms"`
    RBACBuildMs         int `json:"rbac_ms"`
    MeilisearchMs       int `json:"meili_ms"`
    TotalMs             int `json:"total_ms"`
}
```

```bash
lightning profile --site=erp.local --query="unpaid invoices above 10k"
# Session validation:  0.8ms
# NLP parse:           0.2ms
# RBAC filter build:   0.1ms
# Meilisearch query:   5.1ms
# ─────────────────────────
# Total:               6.2ms
```

---

## 5. Production-Grade Event Queue (Redis Streams)

For high-traffic sites or mass bulk imports, replace the in-memory batcher with Redis Streams:

```yaml
# config.yaml
queue:
  driver: redis_streams  # memory | redis_streams | kafka
  redis_stream_key: lightning:events:{site}
  consumer_group: lightning-sync
  max_pending: 10000
```

**Benefits over in-memory batcher:**
- Events survive Go service restarts — no data loss
- Multiple consumer workers can process in parallel
- Consumer lag is monitorable via `XPENDING`
- Dead letter queue via manual `XACK` management

```go
// Binlog → Redis Stream
h.rdb.XAdd(ctx, &redis.XAddArgs{
    Stream: fmt.Sprintf("lightning:events:%s", h.site),
    Values: map[string]interface{}{"payload": payload},
    MaxLen: 50000,
})

// Redis Stream → Meilisearch (consumer)
msgs, _ := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
    Group: "lightning-sync", Consumer: "worker-1",
    Streams: []string{streamKey, ">"},
    Count: 100, Block: 500 * time.Millisecond,
}).Result()
```

---

## 6. Universal Search API (Public Layer)

Expose the search engine as a documented public REST API — accessible by mobile apps, external tools, and third-party integrations without requiring a Frappe session.

### Endpoints

```
GET  /api/v1/search?q=...&doctype=...        Full NLP search
GET  /api/v1/suggest?q=...                   Quick suggestions (top 5)
GET  /api/v1/autocomplete?q=...              Faceted autocomplete
GET  /api/v1/health                          Service health
GET  /api/v1/stats                           Index statistics (admin)
POST /api/v1/reindex                         Trigger reindex (admin)
```

### API Token Auth

```go
// Bearer token in Authorization header (no Frappe session needed)
func APITokenMiddleware(c *fiber.Ctx) error {
    auth := c.Get("Authorization")
    if strings.HasPrefix(auth, "Bearer ") {
        token := strings.TrimPrefix(auth, "Bearer ")
        site, ok := validateAPIToken(token)
        if !ok {
            return c.Status(401).JSON(fiber.Map{"error": "invalid API token"})
        }
        c.Locals("site", site)
        c.Locals("roles", []string{"Search API User"})
        return c.Next()
    }
    return AuthMiddleware(c) // Fall back to Frappe session auth
}
```

---

## 7. Observability (Prometheus + Structured Logging)

```go
// Structured logging with zap
log.Info("search request",
    zap.String("user", user),
    zap.String("query", rawQuery),
    zap.String("doctype", parsed.DocType),
    zap.Int("results", resultCount),
    zap.Int64("took_ms", tookMs),
)

// Prometheus metrics
searchLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
    Name:    "lightning_search_duration_ms",
    Buckets: []float64{1, 2, 5, 10, 25, 50, 100},
}, []string{"site", "doctype"})
```

Expose at `/metrics`. Build Grafana dashboards for:
- P50/P95/P99 search latency per site
- Binlog consumer lag trend
- Documents indexed per minute per DocType
- Error rate per endpoint

---

## 8. CI/CD

```yaml
# .github/workflows/ci.yml
jobs:
  test-go:
    runs-on: ubuntu-latest
    services:
      meilisearch:
        image: getmeili/meilisearch:latest
        ports: [7700:7700]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.21' }
      - run: cd frappe_lightning && go test ./... -v -race -coverprofile=coverage.out
```

---

## 9. Operational Runbook

```bash
# Start service
lightning start --config /etc/lightning/config.yaml

# After crash — auto-resumes from saved binlog position
lightning start --config config.yaml

# If position file corrupted
lightning reset-position --site=erp.local --from-now
lightning reindex --all --site=erp.local
```

---

## Validation Checklist

- [ ] `lightning parse` outputs correct structured query for any NLP input
- [ ] `lightning diff` shows accurate count discrepancy
- [ ] `lightning watch` streams live events in real-time
- [ ] Redis Streams: events survive service restart (no data loss)
- [ ] Consumer lag visible via `redis-cli XPENDING`
- [ ] Public API token auth works for external requests
- [ ] `/api/v1/stats` returns index counts per site
- [ ] Prometheus `/metrics` returns `lightning_search_duration_ms` histogram
- [ ] P99 search latency <10ms verified via metrics
- [ ] All Go tests pass with `-race` flag
- [ ] CI pipeline passes on every push to `develop`
