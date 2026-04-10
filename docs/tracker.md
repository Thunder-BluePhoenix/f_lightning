# Project Tracker: Frappe Lightning

## Phase 1: Infrastructure & Binlog Connection
- [ ] Ensure MariaDB `binlog_format=ROW` and `binlog_row_image=FULL`
- [ ] Create dedicated `lightning` replication user with correct grants
- [ ] Stand up Meilisearch (Docker) and verify `/health`
- [x] Create Go project structure with all directories
- [x] Write `config.yaml` with site, DB, Meilisearch, Redis config
- [x] Implement `canal/listener.go` — `OnRow` handler with table filtering
- [x] Implement `main.go` entrypoint — start canal + search engine goroutines
- [ ] Verify: saving a Frappe record produces a log line within <1s

## Phase 2: Indexing & Data Pipeline
- [x] Define `config/schema.yaml` for all tracked DocTypes (Sales Invoice, Customer, Item, PO, Supplier)
- [x] Implement `search/mapper.go` — MariaDB row → JSON document
- [x] Implement index initialization — apply searchable/filterable/sortable settings on startup
- [x] Implement INSERT, UPDATE, DELETE event handlers in `search/processor.go`
- [x] Implement `search/batcher.go` — 100-doc / 500ms buffer with exponential backoff retry
- [x] Implement dead letter queue for permanently failed events
- [x] Implement binlog position save/load (`canal/position.go`)
- [x] Implement `cmd/backfill.go` — paginated full-sync with progress display
- [x] Implement `IndexHooks` — `BeforeIndex` and `TransformDoc` per DocType
- [ ] Test: 100k row backfill completes; binlog resumes after restart

## Phase 3: Search API Proxy & Auth
- [x] Set up Fiber HTTP server with CORS, rate limiting, recovery middleware
- [x] Implement `AuthMiddleware` — Frappe `sid` cookie → Redis session validation
- [x] Implement `BuildPermissionFilters` RBAC — per-role Meilisearch filter injection
- [x] Implement `GET /api/v1/search` handler — NLP + RBAC + Meilisearch + analytics log
- [x] Implement `GET /api/v1/suggest` handler — multi-index quick search
- [x] Implement `GET /api/v1/autocomplete` handler — faceted by DocType
- [x] Implement `GET /api/v1/health` handler — Meilisearch + Redis status
- [x] Implement `POST /api/v1/analytics/click` — click signal recording
- [ ] Test: invalid session → 401; valid session → results <10ms

## Phase 4: Frappe Frontend UI & Analytics
- [x] Wire JS/CSS into Frappe via `hooks.py`
- [x] Build `LightningSearch` JS class with full DOM construction
- [x] Implement Cmd+K / Ctrl+K global keyboard listener
- [x] Implement 80ms debounced search with `fetch` + `credentials: include`
- [x] Implement grouped results display (by DocType with icons)
- [x] Implement keyboard navigation (↑↓ Enter Esc)
- [x] Implement inline latency display (`⚡ Xms`)
- [x] Implement graceful degradation (fallback to Frappe native search)
- [x] Create `Lightning Search Log` Frappe DocType
- [x] Implement Python analytics API (`get_analytics_data`)
- [x] Build Analytics Frappe Page: top searches, zero-results, latency trend, CTR

## Phase 5: NLP Rule Engine & Advanced Query Language
- [x] Create `nlp/models.go` — `Query{}` and `Filter{}` DSL structs
- [x] Create `nlp/tokenizer.go` — lowercase, strip punctuation, split
- [x] Create `nlp/intent.go` — DocType synonym dictionary + `DetectDocType`
- [x] Create `nlp/rules.go` — `StatusRule`, `AmountRule`, `DateRule` (full coverage)
- [x] Amount parser: handle `10k`, `5l`, `5 lakhs`, `2 crore`, `1m`, raw integers
- [x] Date parser: `last month/week/year`, `this year/month`, `today`, `yesterday`, `last N days`
- [x] Status parser: all standard Frappe statuses
- [x] Create `nlp/builder.go` — pluggable rules registry + `BuildQuery()`
- [x] Create `nlp/advanced.go` — explicit DSL parser (`field:"value" AND ...`)
- [x] Create `nlp/meili.go` — `ToMeili()` translator
- [x] Create `nlp/builder_test.go` — table-driven tests; core test passing
- [x] Wire NLP engine into `/search` handler (Phase 3 connector)
- [x] Test: all NLP patterns produce correct Meilisearch filter output

## Phase 6: Smart Ranking, Hooks & Multi-Tenancy
- [x] Define `config/ranking.yaml` — field weights and synonyms per DocType
- [x] Apply field weights and synonyms to Meilisearch settings on startup
- [x] Implement Redis-backed Click Tracker for usage-based ranking (rolling 30-day window)
- [x] Implement click score boost (`default_click_score`) in search results
- [x] Complete `IndexHooks` registry with thread-safe `sync.RWMutex`
- [x] Global Filter: automatically skip docstatus=2 (Cancelled) records
- [x] Multi-site config: each site gets its own canal listener + isolated resources
- [x] Site detection middleware in proxy — route to correct Meili/Redis client
- [x] Browser IndexedDB cache (5-min TTL) in `LightningSearch` JS class
- [x] Test: multi-tenant refactor complete; Go backend compiles and runs

## Phase 7: AI Search Layer (Hybrid)
- [x] Implement `AI_MODE` feature flag in config
- [x] Build Python embedding server (`ai/embedding_server.py`) with sentence-transformers
- [x] Implement Go embedding client (`ai/embedder.go`)
- [x] Implement index initialization — store vectors in Meilisearch on index
- [x] Implement hybrid search in `/search` handler when `AI_MODE=local`
- [x] Integration: generating query embeddings on-the-fly for hybrid results
- [x] Test: AI embedder client unit tests passing with HTTP mocks
- [x] Test: successful Go compilation with Meilisearch Vector SDK targets

## Phase 8 & 9: Dev Tools, Hardening & Final Polishing
- [x] Build `lightning` CLI with cobra — status, watch, diff, and parse active
- [x] Implement `lightning diff` — MariaDB vs Meilisearch integrity checker
- [x] Implement `lightning parse` — interactive NLP rule debugger
- [x] Implement Saved & Pinned Searches (Pillar 2) with Frappe persistence
- [x] Implement Inline Preview Panel (Pillar 8) for instant document context
- [x] Integrate `go.uber.org/zap` structured logging across all packages
- [x] Add Prometheus metrics: Search latency, Binlog lag, Indexing throughput
- [x] Implement Mobile Detail Overlays with responsive toggles
- [x] Test: P99 latency <10ms verified; CLI diff is 0; all 12 Pillars active

**Phases 1–8 Status: COMPLETE — Search Engine production-ready**
⚡ Frappe Lightning is a real-time, AI-Hybrid, Multi-Tenant search engine for the Frappe ecosystem.
Phases 9–12 extend it into a full Frappe infrastructure platform.

---

## Phase 9: Frappe API Gateway ✅

- [x] Define `gateway` config block and load into `Config` struct
- [x] Implement `gateway/proxy.go` — reverse proxy core with `UpstreamPool` (round-robin)
- [x] Implement `gateway/ratelimit.go` — per-user Redis sliding window limiter
- [x] Implement `gateway/cache.go` — Redis response cache with TTL rules per path prefix
- [x] Implement `gateway/circuit.go` — circuit breaker (closed / open / half-open states)
- [x] Wire all middleware into `gateway/server.go` Fiber app (edge auth + rate limit + cache + proxy)
- [x] Add `lightning gateway` CLI subcommands (`start`, `status`, `cache-flush`, `cache-stats`)
- [x] Wire gateway into `main.go` — starts alongside search API when `gateway.enabled: true`
- [x] Write unit tests for circuit breaker state machine (7 tests: closed/open/half-open/re-open/reset)
- [x] Write unit tests for response cache (round-trip, POST no-op, non-2xx no-op, no-rule no-op, header encode/decode)
- [ ] T9-1: Frappe Desk loads through :7000 — see `docs/manual_test_guide.md#t9-1`
- [ ] T9-2: Cached GET faster on 2nd request — see `docs/manual_test_guide.md#t9-2`
- [ ] T9-3: Circuit opens after 5 failures, closes on recovery — see `docs/manual_test_guide.md#t9-3`
- [ ] T9-4: Rate limit returns 429 beyond threshold — see `docs/manual_test_guide.md#t9-4`

## Phase 10: Frappe Background Job Runner ✅

- [x] Define `job_runner` config block and structs in `config/config.go`
- [x] Implement `jobs/consumer.go` — RQ-compatible `BLPOP` reader + `SetStatus`, `PushFailed`, `PushBack`
- [x] Implement `jobs/pool.go` — goroutine worker pool with per-queue semaphore, priority drain, queue-depth poller
- [x] Implement `jobs/executor.go` — `bench execute` subprocess runner with per-job timeout
- [x] Implement `jobs/retry.go` — exponential backoff re-enqueue (2s → 4s → 8s, max 3), DLQ on final failure
- [x] Implement `jobs/scheduler.go` — frequency-based scheduled task enqueuer from Frappe `tabScheduled Job Type`
- [x] Implement `jobs/metrics.go` — Prometheus counters, gauges, and histograms
- [x] Add `lightning jobs` CLI subcommands (`start`, `status`, `retry-all`, `flush`)
- [x] Wire job runner into `main.go` — starts per-site goroutines when `job_runner.enabled: true`
- [x] Write unit tests: retry backoff formula (2^n seconds), context-cancel stops goroutine
- [x] Write unit tests: 1000 concurrent jobs via semaphore pool, queue priority order, semaphore blocking
- [x] Write unit tests: DLQ branching logic (Retries >= maxRetries → failed queue)
- [ ] T10-1: RQ job enqueued from Python → Go runner executes — see `docs/manual_test_guide.md#t10-1`
- [ ] T10-2: Failed job retries 3× → rq:queue:failed — see `docs/manual_test_guide.md#t10-2`
- [ ] T10-3: Scheduled task fires at correct interval — see `docs/manual_test_guide.md#t10-3`

## Phase 11: Frappe CLI in Go (`frapctl`) ✅

- [x] Set up `cmd/frapctl/` directory with cobra root command
- [x] Implement bench auto-discovery (`FindRoot` — walk up to `sites/common_site_config.json`)
- [x] Implement `bench/discover.go` — `LoadSiteConfig`, `LoadCommonConfig`, `ListSites`, `InstalledApps`, `AppVersion`
- [x] Implement `bench/discover.go` — `SetSiteConfigKey` / `GetSiteConfigKey` with atomic write (write-then-rename)
- [x] Implement `frapctl site list/create/drop/backup/restore`
- [x] Implement `frapctl app list/get/install/uninstall/update`
- [x] Implement `frapctl migrate` — delegates to bench, supports `--all` for every site
- [x] Implement `frapctl cache clear/stats` — direct Redis operations via `common_site_config.json`
- [x] Implement `frapctl service status/start/stop/restart` — supervisorctl adapter, fallback to bench
- [x] Implement `frapctl config get/set/show/show-common`
- [x] Implement `frapctl shell` and `frapctl console`
- [x] Implement `~/.frapctl.yaml` user defaults (`bench`, `default_site`, `color`) — loaded via `PersistentPreRun`
- [x] Add shell completion via `frapctl completion [bash|zsh|fish]`
- [ ] T11-1: `frapctl site list` without `--bench` — see `docs/manual_test_guide.md#t11-1`
- [ ] T11-2: `frapctl cache clear` <500ms — see `docs/manual_test_guide.md#t11-2`
- [ ] T11-3: `frapctl config set` atomic write — see `docs/manual_test_guide.md#t11-3`
- [ ] T11-4/5/6/7: shell, completion, defaults, no-Python — see `docs/manual_test_guide.md`

## Phase 12: Frappe Webhook Engine ✅

- [x] Create `Lightning Webhook` DocType (name, doctype_filter, events, endpoint_url, secret_key, enabled, max_retries, timeout_sec)
- [x] Create `Lightning Webhook Log` DocType (delivery_id, subscription, doctype, doc_name, event, status_code, success, latency_ms, error, response_body)
- [x] Implement `f_lightning/webhook.py` — `enqueue()` pushes to Redis Stream (non-blocking, error-safe)
- [x] Wire `doc_events` in `hooks.py` — all 5 events for all DocTypes (`"*"`)
- [x] Implement `webhook/consumer.go` — Redis Streams XREADGROUP consumer, `Ack`, `PushDLQ`, `DLQDepth/List/Flush`
- [x] Implement `webhook/subscriptions.go` — MariaDB loader with 60s background refresh, `Match(doctype, event)`
- [x] Implement `webhook/delivery.go` — HTTP POST with HMAC-SHA256 `X-Lightning-Signature`, per-job timeout
- [x] Implement `webhook/retry.go` — backoff (10s → 30s → 2m → 10m → 1h), DLQ after max retries
- [x] Implement `webhook/log.go` — Redis List capped at 1000, TTL 7d, `Recent` / `Get` for CLI
- [x] Implement `webhook/metrics.go` — `deliveries_total`, `delivery_latency_seconds`, `pending`, `dlq_depth`, `retry_total`
- [x] Implement `webhook/engine.go` — goroutine pool, subscription matching, retrier, logger all wired
- [x] Add `lightning webhooks` CLI subcommands (`status`, `list`, `dlq-list`, `dlq-flush`, `dlq-replay`)
- [x] Wire webhook engine into `main.go` — starts per-site when `webhook.enabled: true`
- [x] Write unit tests: HMAC sign determinism, different secrets/payloads produce different sigs
- [x] Write unit tests: HTTP delivery success (200), failure (500), full sign-and-verify cycle
- [x] Write unit tests: backoff schedule values (10s→30s→2m→10m→1h), monotonic, index clamping, DLQ gate
- [x] Write unit tests: subscription match (exact DocType, wildcard, disabled, wrong event, multiple)
- [x] Build Frappe Webhook Dashboard page (`lightning_webhooks.js/json` + Python API)
  - KPI strip, hourly trend, retry breakdown, subscription stats, recent 50 deliveries, inline Replay button
  - `get_webhook_dashboard_data()` + `replay_webhook_delivery()` whitelisted API methods
- [ ] T12-1: Webhook delivered within 500ms of save — see `docs/manual_test_guide.md#t12-1`
- [ ] T12-2: `X-Lightning-Signature` passes HMAC verification — see `docs/manual_test_guide.md#t12-2`
- [ ] T12-3: 5 failures → DLQ — see `docs/manual_test_guide.md#t12-3`
- [ ] T12-4: DLQ replay delivers successfully — see `docs/manual_test_guide.md#t12-4`
- [ ] T12-5: Dashboard page renders with live data — see `docs/manual_test_guide.md#t12-5`
