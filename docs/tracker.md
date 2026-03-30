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

## Phase 8: Dev Tools, Hardening & Universal API
- [ ] Build `lightning` CLI with cobra — parse, diff, watch, reindex, status, health commands
- [ ] Implement `lightning watch` — live binlog event streamer
- [ ] Implement `lightning diff` — MariaDB vs Meilisearch count comparison
- [ ] Implement `lightning profile` — per-segment latency breakdown
- [ ] Implement Redis Streams driver (feature-flagged replacement for in-memory batcher)
- [ ] Implement API token auth for public REST API consumers
- [ ] Expose `/api/v1/stats` and `/api/v1/reindex` (admin-only endpoints)
- [ ] Integrate `go.uber.org/zap` structured logging across all packages
- [ ] Add Prometheus metrics: `lightning_search_duration_ms`, `lightning_binlog_lag`, `lightning_documents_indexed_total`
- [ ] Expose `/metrics` endpoint for Prometheus scraping
- [ ] Write CI workflow (GitHub Actions) — `go test ./... -race` with Meilisearch service
- [ ] Write operational runbook section in README
- [ ] Test: P99 latency <10ms verified under load; all tests pass with `-race`
