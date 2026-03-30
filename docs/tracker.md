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
- [ ] Implement dead letter queue for permanently failed events
- [x] Implement binlog position save/load (`canal/position.go`)
- [ ] Implement `cmd/backfill.go` — paginated full-sync with progress display
- [x] Implement `IndexHooks` — `BeforeIndex` and `TransformDoc` per DocType
- [ ] Test: 100k row backfill completes; binlog resumes after restart

## Phase 3: Search API Proxy & Auth
- [ ] Set up Fiber HTTP server with CORS, rate limiting, recovery middleware
- [ ] Implement `AuthMiddleware` — Frappe `sid` cookie → Redis session validation
- [ ] Implement `BuildPermissionFilters` RBAC — per-role Meilisearch filter injection
- [ ] Implement `GET /api/v1/search` handler — NLP + RBAC + Meilisearch + analytics log
- [ ] Implement `GET /api/v1/suggest` handler — multi-index quick search
- [ ] Implement `GET /api/v1/autocomplete` handler — faceted by DocType
- [ ] Implement `GET /api/v1/health` handler — Meilisearch + Redis status
- [ ] Implement `POST /api/v1/analytics/click` — click signal recording
- [ ] Test: invalid session → 401; valid session → results <10ms

## Phase 4: Frappe Frontend UI & Analytics
- [ ] Wire JS/CSS into Frappe via `hooks.py`
- [ ] Build `LightningSearch` JS class with full DOM construction
- [ ] Implement Cmd+K / Ctrl+K global keyboard listener
- [ ] Implement 80ms debounced search with `fetch` + `credentials: include`
- [ ] Implement grouped results display (by DocType with icons)
- [ ] Implement keyboard navigation (↑↓ Enter Esc)
- [ ] Implement inline latency display (`⚡ Xms`)
- [ ] Implement graceful degradation (fallback to Frappe native search)
- [ ] Create `Lightning Search Log` Frappe DocType
- [ ] Implement Python analytics API (`get_analytics_data`)
- [ ] Build Analytics Frappe Page: top searches, zero-results, latency trend, CTR

## Phase 5: NLP Rule Engine & Advanced Query Language
- [x] Create `nlp/models.go` — `Query{}` and `Filter{}` DSL structs
- [x] Create `nlp/tokenizer.go` — lowercase, strip punctuation, split
- [x] Create `nlp/intent.go` — DocType synonym dictionary + `DetectDocType`
- [ ] Create `nlp/rules.go` — `StatusRule`, `AmountRule`, `DateRule` (full coverage)
- [ ] Amount parser: handle `10k`, `5l`, `5 lakhs`, `2 crore`, `1m`, raw integers
- [ ] Date parser: `last month/week/year`, `this year/month`, `today`, `yesterday`, `last N days`
- [ ] Status parser: all standard Frappe statuses
- [x] Create `nlp/builder.go` — pluggable rules registry + `BuildQuery()`
- [ ] Create `nlp/advanced.go` — explicit DSL parser (`field:"value" AND ...`)
- [x] Create `nlp/meili.go` — `ToMeili()` translator
- [x] Create `nlp/builder_test.go` — table-driven tests; core test passing
- [ ] Wire NLP engine into `/search` handler (Phase 3 connector)
- [ ] Test: all NLP patterns produce correct Meilisearch filter output

## Phase 6: Smart Ranking, Hooks & Multi-Tenancy
- [ ] Define `config/ranking.yaml` — field weights per DocType
- [ ] Apply field weights to Meilisearch index settings on startup
- [ ] Implement role-aware index query ordering (`ranking/role_ranking.go`)
- [ ] Implement click signal recording in Redis (rolling 30-day window)
- [ ] Implement click score boost in search results
- [ ] Define `config/synonyms.yaml` — global + industry-specific synonym groups
- [ ] Apply synonyms to all indexes on startup
- [ ] Complete `IndexHooks` registry — `RegisterHooks`, `GetHooks` with thread safety
- [ ] Implement YAML-driven hook config (skip_docstatus, computed_fields)
- [ ] Multi-site config: each site gets its own canal listener + goroutine
- [ ] Site detection middleware in proxy — route to correct Meili/Redis client
- [ ] Browser IndexedDB cache (5-min TTL) in `LightningSearch` JS class
- [ ] Test: two sites run simultaneously with isolated indexes

## Phase 7: AI Search Layer (Hybrid)
- [ ] Implement `AI_MODE` feature flag in config
- [ ] Build Python embedding server (`ai/embedding_server.py`) with sentence-transformers
- [ ] Implement Go embedding client (`ai/embedder.go`)
- [ ] Implement `search/vector_indexer.go` — store vectors in Meilisearch on index
- [ ] Implement hybrid search in `/search` handler when `AI_MODE=local`
- [ ] Implement LLM client (`ai/llm.go`) for OpenAI / Ollama
- [ ] Implement `mergeQueries` — combine rule engine + LLM filter outputs
- [ ] Implement template-based result summarizer (`ai/summarizer.go`)
- [ ] Test: `AI_MODE=off` identical to Phase 5; `local` mode adds semantic results
- [ ] Test: LLM failure → graceful fallback to rule engine only

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
