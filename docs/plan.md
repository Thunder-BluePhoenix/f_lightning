# Implementation Plan: Frappe Lightning

## The Goal

Build a **real-time sync bridge** between Frappe's MariaDB instance and Meilisearch, powered entirely by Go — giving Frappe a true sub-10ms "search-as-you-type" experience across millions of records.

Then evolve it into a full **Frappe Data Engine**: search, analytics, NLP, AI, and event streaming — all in one open-source system.

**One-line description:**
> Frappe Lightning — A real-time Go-powered search layer that syncs MariaDB changes to Meilisearch, delivering instant, sub-10ms "search-as-you-type" across millions of Frappe records.

---

## Why This Matters

Frappe's native **Global Search** polls the database dynamically, processes results through multiple Python layers, and becomes visibly sluggish on sites with millions of records. Users feel it. Productivity drops.

Lightning is an **infra-level upgrade disguised as a feature** — something Frappe itself should have shipped. The Go service listens to the MariaDB Binary Log directly: zero polling, zero Python overhead. Every save instantly propagates to a Meilisearch index that serves results in under 10ms.

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        FRAPPE DESK                          │
│          (Cmd+K Omnibox → Lightning JS Component)           │
└────────────────────────────┬────────────────────────────────┘
                             │  HTTP GET /search?q=...
                             ▼
┌─────────────────────────────────────────────────────────────┐
│              GO SEARCH PROXY (frappe_lightning)              │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐ │
│  │  NLP Engine  │  │  Auth / RBAC │  │  Meilisearch Fwd  │ │
│  │  (nlp/)      │  │  (Redis SID) │  │  (search/)        │ │
│  └──────────────┘  └──────────────┘  └───────────────────┘ │
│  ┌──────────────┐  ┌──────────────┐                        │
│  │ Smart Ranker │  │  Analytics   │                        │
│  │  (ranking/)  │  │  (analytics/)│                        │
│  └──────────────┘  └──────────────┘                        │
└────────────────────────────┬────────────────────────────────┘
                             │  Filtered, Ranked Query
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                      MEILISEARCH                            │
│    (per-site namespaced indexes, vector-ready)              │
└─────────────────────────────────────────────────────────────┘
                             ▲
              Instant Upsert │ on every change
                             │
┌─────────────────────────────────────────────────────────────┐
│              GO BINLOG LISTENER (canal/)                     │
│   Reads MariaDB ROW-format binlog events in real-time       │
│   Hooks: before_index / transform_doc (pluggable)           │
└────────────────────────────┬────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                  MARIADB (binlog_format=ROW)                 │
│    tabSales Invoice | tabCustomer | tabItem | ...           │
└─────────────────────────────────────────────────────────────┘
```

---

## Component Breakdown

| Component | Technology | Location |
|---|---|---|
| Binlog Listener | Go + `go-mysql/canal` | `frappe_lightning/canal/` |
| Sync Engine (INSERT/UPDATE/DELETE) | Go + Meilisearch Go SDK | `frappe_lightning/search/` |
| NLP Query Parser | Pure Go (zero external deps) | `frappe_lightning/nlp/` |
| Advanced DSL Parser | Pure Go | `frappe_lightning/nlp/advanced.go` |
| Search API Proxy | Go + Fiber | `frappe_lightning/api/` |
| Auth Middleware | Go + Redis session read | `frappe_lightning/api/middleware/` |
| RBAC Filter Engine | Go | `frappe_lightning/api/rbac.go` |
| Smart Ranking Engine | Go + YAML ranking config | `frappe_lightning/ranking/` |
| Analytics Collector | Go → Frappe DocType | `frappe_lightning/analytics/` |
| Pluggable Index Hooks | Go hook registry | `frappe_lightning/search/hooks.go` |
| Frappe UI Omnibox | Vanilla JS + CSS | `f_lightning/public/js/` |
| Analytics Dashboard | Frappe Page (Python+JS) | `f_lightning/page/` |
| Dev Tools CLI | Go cobra commands | `frappe_lightning/cmd/` |
| AI Layer (optional) | Go + vector embeddings | `frappe_lightning/ai/` |

---

## The Data Flow — End to End

### Write Path (Sync)
1. User saves a Sales Invoice in Frappe.
2. MariaDB writes a row-level event to the binlog (ROW format).
3. The Go `canal` listener catches the event immediately — no polling.
4. Go maps the SQL row → JSON document using the schema config.
5. `before_index` hook runs — can skip the document entirely (e.g., exclude cancelled docs).
6. `transform_doc` hook runs — can add computed fields, normalize values.
7. Batched upsert into Meilisearch index `{site}_sales_invoice`.
8. Binlog position saved to disk for safe restart.

### Read Path (Search)
1. User types `"unpaid invoices last month above 10k"` in the Cmd+K Omnibox.
2. Frontend (80ms debounce) hits `GET /api/v1/search?q=...` on the Go proxy.
3. Auth middleware reads Frappe `sid` cookie → validates against Redis session cache (no DB call).
4. NLP Engine tokenizes → detects intent → applies rule chain → builds structured `Query{}` DSL.
5. Smart Ranking Engine injects field-boost parameters and usage-signal sort orders.
6. RBAC Engine injects per-user permission filters (e.g., `company = 'Acme Corp'`).
7. Final Meilisearch params sent to correct namespaced index.
8. Results returned in **<10ms**. Analytics event logged asynchronously.

---

## All 12 Feature Areas

### ⚡ 1. Smart Ranking & Relevance Engine
- Field-level boosting (name > remarks, customer_name > notes)
- Usage-based ranking via Redis click signals
- Role-aware ranking (Sales → invoices first, Purchase → POs first)
- Typo tolerance (configurable edit distance)
- Multi-language synonym files

### 🔍 2. Advanced Query Language
- Explicit DSL: `customer:"Amazon" AND status:"Overdue"`
- Numeric ranges: `amount > 50000`
- Date shorthand: `date: last_30_days`
- Saved searches per user
- Query suggestions from search logs

### 🧠 3. AI Search Layer (Hybrid, No LLM Required to Ship)
- Rule-based NLP: `"unpaid invoices above 10k"` → structured Meilisearch filters
- Local embeddings for semantic search (fastText / sentence-transformers)
- Template-based result summarization: `"12 unpaid invoices totalling ₹2.3L"`
- Feature flag: `AI_MODE = off | local | cloud`
- Cloud LLM as optional upgrade (OpenAI / Ollama / Gemini)

### 🔄 4. Real-Time Sync Engine (Production Grade)
- Zero-latency binlog capture via go-mysql/canal
- Batching: 100 docs OR 500ms, whichever comes first
- Retry with exponential backoff (1s → 2s → 4s → 60s max)
- Dead letter queue for stuck events
- Backfill CLI for existing millions of rows
- GTID-based binlog position persistence

### 📊 5. Search Analytics Dashboard
- Top searched terms (bar chart)
- Zero-result queries (business gap detection — 🔥)
- Average query latency trending (line chart per day)
- Click-through rate (% of searches that opened a doc)
- Per-user and per-role search breakdowns
- Search volume heatmap

### 🧩 6. Universal Search API (Public Layer)
- `GET /api/v1/search?q=...&doctype=...`
- `GET /api/v1/suggest?q=...`
- `GET /api/v1/autocomplete?q=...`
- API token auth (separate from Frappe session)
- Rate-limited, versioned, CORS-configurable
- OpenAPI spec for documentation

### ⚙️ 7. Pluggable Indexing System
- `BeforeIndex` hook: skip documents by condition
- `TransformDoc` hook: add computed fields, normalize
- YAML-driven hook config for non-Go developers
- Custom synonym files per DocType
- Per-DocType field inclusion/exclusion lists

### 🖥️ 8. Lightning UI Components
- Cmd+K Command Palette (VS Code / Raycast style)
- Grouped results by DocType with icons
- Keyboard navigation (↑↓ Enter Esc)
- Inline preview panel
- 80ms debounced input
- Micro-animations and dark/light mode support

### 🔐 9. Permission-Aware Search
- Session validation via Redis (no DB round-trip)
- Per-user filter injection at query time
- Meilisearch tenant tokens for cryptographic isolation
- Role → DocType → field-level visibility mapping
- Zero data leakage guarantee

### 📦 10. Multi-Tenant / Multi-Site Support
- Namespaced indexes: `{site}_{doctype}`
- One Go binary, all sites, goroutine-isolated
- Per-site YAML config (DB, Meilisearch key, DocTypes, ranking)
- Per-site API key management

### ⚡ 11. Offline / Edge Search
- Browser IndexedDB cache for top-N frequent results
- Graceful degradation: fallback to Frappe native search
- Edge node deployment for distributed ERP
- Configurable cache TTL

### 🧪 12. Dev Tools & Debug Mode
- Live binlog viewer (stream to browser or terminal)
- Index diff checker (MariaDB vs Meilisearch state)
- Reindex CLI: `lightning reindex --doctype=... --site=...`
- Latency profiler: binlog → index → query → response
- NLP rule tester: `lightning parse "unpaid invoices above 10k"`
- Debug mode with full intermediate state logging

---

## Phase Breakdown

| Phase | Title | Focus |
|---|---|---|
| **1** | Infrastructure & Binlog Connection | MariaDB setup, Go project, canal listener |
| **2** | Indexing & Data Pipeline | Event handling, schema mapping, backfill, hooks |
| **3** | Search API Proxy & Auth | HTTP server, session auth, RBAC, endpoints |
| **4** | Frappe Frontend UI & Analytics | Cmd+K UI, analytics dashboard, search logs |
| **5** | NLP Rule Engine & Advanced Query | Tokenizer → Rules → DSL → Meilisearch translator |
| **6** | Smart Ranking, Hooks & Multi-Tenancy | Boosting, synonyms, usage signals, per-site isolation |
| **7** | AI Search Layer (Hybrid) | Local embeddings, semantic search, AI_MODE flag |
| **8** | Dev Tools, Hardening & Universal API | CLI tools, production ops, public REST API |

---

## Key Design Decisions

| Decision | Rationale |
|---|---|
| **Go over Python** | 10–100x lower latency, true concurrency, single binary deployment |
| **Meilisearch over Elasticsearch** | Simpler ops, faster cold start, built-in typo tolerance, vector-ready |
| **Binlog over hooks** | No Frappe code changes needed; works on any DocType automatically |
| **Redis session validation** | No DB round-trip per search request — keeps latency under 10ms |
| **Rule-based NLP first** | Deterministic, offline-safe, enterprise-safe; LLM is a toggle-on upgrade |
| **YAML-driven config** | Adding a DocType requires zero Go code |
| **Pluggable hooks** | Industry-specific customization without forking the core engine |

---

## Key Challenges & Strategies

| Challenge | Strategy |
|---|---|
| **Schema Discovery** | Dynamic field mapping from `config/schema.yaml` + MariaDB `DESCRIBE tabXXX` |
| **Permission Filtering** | Redis session → role lookup → Meilisearch filter injection at query time |
| **Initial Sync** | One-shot Go `SELECT *` batch sweep per DocType with progress indicator |
| **Binlog Resume** | Persist last GTID/file+pos to disk after every flush |
| **Multi-Tenancy** | Per-site namespaced index + per-site config YAML |
| **LLM Avoidance** | Rule-based NLP fast path; `AI_MODE=off` default |
| **Ranking Signals** | Track click events in Redis; incorporate into Meilisearch ranking rules |
| **Burst Handling** | Batcher (100 rows OR 500ms) reduces pressure during bulk imports |
| **Graceful Degradation** | If Go proxy is unreachable, frontend falls back to Frappe native search |

---

## Ultimate Vision — The "Frappe Data Engine"

Transform Lightning from a search proxy into a full real-time data intelligence layer:

```
Frappe  →  Go Data Engine  →  Meilisearch + Vector DB
                          →  Analytics DB (ClickHouse / TimescaleDB)
                          →  Event Stream (Kafka / Redis Streams)
                          →  AI Layer (Ollama / OpenAI / Gemini)
                          →  Universal REST API (external consumers)
```

| Stage | What It Is |
|---|---|
| v1 — Lightning Search | Sub-10ms search in Frappe |
| v2 — Lightning Analytics | Real-time query intelligence + business insights |
| v3 — Lightning Engine | Full data intelligence platform for the Frappe ecosystem |

---

## Verification Plan

### Automated Tests
- **Unit Tests**: Go `nlp` package — table-driven tests for every rule class (status, amount, date)
- **Integration Test**: Create a record in Frappe → assert it appears in Meilisearch within 500ms
- **Auth Test**: Send `/search` with invalid session → assert `401` response
- **Permission Test**: Restricted user → assert results only include permitted records
- **Backfill Test**: Run backfill on 100k rows → verify complete index, no duplicates
- **Ranking Test**: Click a result 10 times → verify it surfaces higher in subsequent queries
- **Hook Test**: Cancelled doc saved → verify it does NOT appear in search results

### Manual Verification
- Open Frappe Desk, press `Cmd+K`, type a query → results appear in <100ms visually
- Type `"unpaid invoices last month above 10k"` → verify correct DocType + 3 filters parsed
- Restart Go service mid-session → verify binlog resumes from saved position, no data loss
- Run `lightning reindex --doctype="Sales Invoice" --site=erp.local` → full reindex completes
- Log in as restricted user → verify only permitted company's records appear
- Open Analytics Dashboard → verify top queries and zero-result list are populated
