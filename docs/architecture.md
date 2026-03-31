# Frappe Lightning: Architecture Reference

This document describes the internal architecture of Frappe Lightning — how the components fit together, how data flows through the system, and why each design decision was made.

---

## Overview

Lightning is a standalone Go sidecar that sits alongside a Frappe installation. It has two independent responsibilities:

1. **Sync** — listen to MariaDB's binary log and keep Meilisearch indexes up to date in real time.
2. **Serve** — accept HTTP search requests from the Frappe frontend, parse natural language, inject permissions, and proxy queries to Meilisearch.

These two paths share no request cycle. The sync path is entirely asynchronous.

---

## System Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                        FRAPPE DESK                          │
│          (Cmd+K Omnibox → Lightning JS Component)           │
└────────────────────────────┬────────────────────────────────┘
                             │  HTTP GET /api/v1/search?q=...
                             │  Header: X-Frappe-Site-Name
                             ▼
┌─────────────────────────────────────────────────────────────┐
│              LIGHTNING GO BINARY (frappe_lightning)          │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  HTTP Server (Fiber)  :8765                         │   │
│  │  - CORS, Rate Limiter (150 req/10s), Panic Recovery │   │
│  │  - SiteResolver middleware (multi-tenant dispatch)  │   │
│  │  - AuthRequired middleware (Redis session / Token)  │   │
│  │  - /api/v1/search  → Search handler                 │   │
│  │  - /api/v1/suggest → Suggest handler                │   │
│  │  - /api/v1/health  → HealthCheck (no auth)          │   │
│  │  - /metrics        → Prometheus handler (no auth)   │   │
│  │  - POST /api/v1/analytics/click → LogClick          │   │
│  └────────────────┬────────────────────────────────────┘   │
│                   │                                         │
│          ┌────────▼────────┐   ┌─────────────────────┐     │
│          │   NLP Engine    │   │     RBAC Engine      │     │
│          │   nlp/          │   │  api/rbac/rbac.go    │     │
│          │ BuildQuery(q)   │   │ BuildPermissionFilter│     │
│          │ ToMeili(parsed) │   └────────────┬─────────┘     │
│          └────────┬────────┘               │               │
│                   └──────────┬─────────────┘               │
│                              │ merged filter                │
│                              ▼                              │
│                  ┌─────────────────────┐                   │
│                  │  Meilisearch Client │                   │
│                  │  (per-site tenant)  │                   │
│                  └─────────────────────┘                   │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Sync Pipeline (goroutine per site)                 │   │
│  │                                                     │   │
│  │  canal.Start() ──events chan──► search.Engine       │   │
│  │   (go-mysql binlog)            (batcher → meili)    │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
          ▲ binlog stream                  ▲ index upsert/delete
          │                               │
┌─────────┴───────────┐         ┌─────────┴────────────────┐
│   MariaDB           │         │   Meilisearch             │
│   (ROW binlog)      │         │   per-site namespaced     │
│   tabSales Invoice  │         │   indexes                 │
│   tabCustomer ...   │         │   erp_local_sales_invoice │
└─────────────────────┘         └──────────────────────────┘
          ▲                               ▲
          │ session read                  │ click score write
          └────────────┬──────────────────┘
                       │
               ┌───────┴───────┐
               │     Redis     │
               │  (sessions +  │
               │  click scores)│
               └───────────────┘
```

---

## Components

### 1. `main.go` — Bootstrap & Multi-Tenant Setup

The entry point iterates over every configured site and starts:
- A **Meilisearch client** per site.
- An **index initialization** pass that applies schema settings (searchable/filterable/sortable attributes, synonyms, ranking rules) to Meilisearch before any events arrive.
- A **channel** (`chan *canal.RowEvent`, buffer 1024) shared between the binlog listener and the sync engine for that site.
- A **`search.Engine` goroutine** that consumes events and batches writes to Meilisearch.
- A **`canal.Start` goroutine** that connects to MariaDB and produces row events.
- A single shared **`api.Server`** that serves all sites from one port.

Graceful shutdown listens for `SIGINT`/`SIGTERM` and calls `server.Stop()`.

---

### 2. `canal/` — Binlog Listener

**File:** `canal/listener.go`, `canal/position.go`

Uses the `go-mysql/canal` library to connect to MariaDB as a replication slave. Only tables listed in the schema config are included via table regex filters.

On each row event:
- Column names are extracted from the table metadata provided by the canal library.
- The event is wrapped into a `RowEvent` struct with the site name, table name, action (`insert`/`update`/`delete`), column list, raw rows, and the MariaDB timestamp.
- The event is sent to the `eventsCh` channel non-blockingly (the channel has a 1024-item buffer).

**Binlog position persistence:** On startup, the listener attempts to resume from a saved `{site}.pos` file written by `canal/position.go`. If no file exists it starts from the current tail of the binlog, meaning only future changes are captured (use the backfill CLI for existing data).

---

### 3. `search/` — Sync Engine

**File:** `search/engine.go`, `search/batcher.go`, `search/hooks.go`, `search/dlq.go`

#### Engine

`Engine.Start()` reads `RowEvent`s from the channel. For each event:
1. Looks up the schema for the table.
2. Converts the raw row slice into a `map[string]interface{}` containing only the whitelisted fields from the schema.
3. For `insert`/`update`: runs the `BeforeIndex` hook (can reject the document), then `TransformDoc` hook (can mutate it), optionally generates a vector embedding, and adds the doc to the batcher.
4. For `delete`: immediately calls `meili.Index().DeleteDocument()` — no batching.
5. Updates the Prometheus `BinlogLag` gauge using the binlog event timestamp vs. current wall clock.

#### Batcher

`Batcher` accumulates documents per index name and flushes when either:
- 100 documents have accumulated, or
- 500ms have elapsed since the last add.

Flushed batches call `meili.Index().AddDocuments()` with primary key `"name"`. Failed flushes are retried with exponential backoff (1s → 2s → 4s up to 60s); after max retries the batch goes to the Dead Letter Queue.

#### Hooks

`HookRegistry` holds two hook types per DocType:
- `BeforeIndex(doctype, doc) bool` — return `false` to drop the document (e.g., skip cancelled records).
- `TransformDoc(doctype, doc) doc` — mutate or enrich the document before indexing.

Hooks are registered programmatically in Go code.

---

### 4. `api/` — HTTP Server

**File:** `api/server.go`

Built on [Fiber](https://gofiber.io/). Middleware stack (applied globally):
1. **Recover** — catches panics, returns 500.
2. **CORS** — `AllowOrigins: "*"` globally; tighten per-site via Nginx in production.
3. **Limiter** — 150 requests per 10 seconds per IP.

Routes:
| Method | Path | Auth | Handler |
|--------|------|------|---------|
| GET | `/metrics` | None | Prometheus |
| GET | `/api/v1/health` | None | HealthCheck |
| GET | `/api/v1/search` | Required | Search |
| GET | `/api/v1/suggest` | Required | Suggest |
| GET | `/api/v1/autocomplete` | Required | Autocomplete |
| POST | `/api/v1/analytics/click` | Required | LogClick |

All protected routes first pass through **SiteResolver** and then **AuthRequired**.

---

### 5. `api/middleware/` — Tenant Dispatch & Auth

#### SiteResolver (`middleware/site.go`)

Reads the `X-Frappe-Site-Name` request header. Looks up the matching `Tenant` struct (which holds the per-site Meilisearch client, Redis client, config, AI mode, and API token). Injects these into the Fiber context locals.

If no matching tenant is found, returns `404`.

#### AuthRequired (`middleware/auth.go`)

Two authentication paths:

**1. Bearer Token** (external / mobile apps):
```
Authorization: Bearer <api_token from config.yaml>
```
If the token matches the configured `api_token`, the user is set to `"api_user"` with roles `["System Manager", "Search API User"]`.

**2. Frappe Session Cookie** (`sid`):
The middleware reads the `sid` cookie and looks up the session in Redis using the key pattern `{site}|sessiondata|{sid}` (Frappe v15/v16 default). Falls back to `{sid}` as a bare key. A 200ms timeout is applied to the Redis call. If the session is valid and not `"Guest"`, the user and roles are extracted and injected into context locals.

> **Note:** The current session parser (`extractUserFromFrappeSession`) is a placeholder. In production, Frappe's Python `pickle`-encoded sessions require a compatible parser or a short Python sidecar bridge.

---

### 6. `nlp/` — Natural Language Query Engine

**Files:** `nlp/builder.go`, `nlp/rules.go`, `nlp/models.go`, `nlp/meili.go`, `nlp/advanced.go`, `nlp/intent.go`, `nlp/tokenizer.go`

The NLP pipeline is **purely rule-based** — no external dependencies, works fully offline.

#### Pipeline

```
raw query string
       │
       ▼
  Tokenizer        → lowercase, split on whitespace/punctuation
       │
       ▼
  Intent Detection → identifies DocType keywords (invoice, customer, PO, ...)
       │
       ▼
  Rule Chain       → runs each rule function against the token stream:
                     - DetectStatusFilter   (unpaid → status="Overdue")
                     - DetectAmountFilter   (above 10k → grand_total > 10000)
                     - ParseDateRange       (last month → posting_date between ...)
       │
       ▼
  Query{}          → structured: {DocType, Text, Filters[], Limit}
       │
       ▼
  ToMeili()        → translates Query{} to Meilisearch filter DSL strings
```

#### Amount Parsing (`ParseAmount`)

Supports: raw integers, `k` (×1,000), `l`/`lakh`/`lakhs` (×100,000), `m`/`million` (×1,000,000), `cr`/`crore`/`crores` (×10,000,000). Comma separators are stripped before parsing.

#### Date Range Parsing (`ParseDateRange`)

Recognized shorthand: `today`, `yesterday`, `this week`, `this month`, `this year`, `last week`, `last month`, `last year`. Dates are resolved to absolute `time.Time` ranges at query time and stored as UNIX timestamps in Meilisearch filters.

#### Status Detection (`DetectStatusFilter`)

Maps adjectives to Frappe status strings:

| Token | Meilisearch filter |
|-------|--------------------|
| `unpaid`, `overdue` | `status = "Overdue"` |
| `paid` | `status = "Paid"` |
| `draft` | `status = "Draft"` |
| `pending` | `status = "Pending"` |
| `submitted` | `status = "Submitted"` |
| `cancelled` | `status = "Cancelled"` |
| `completed` | `status = "Completed"` |
| `open` | `status = "Open"` |
| `closed` | `status = "Closed"` |
| `active` | `status = "Active"` |

---

### 7. `api/rbac/` — Permission Filter Engine

**File:** `api/rbac/rbac.go`

After the NLP parse, `BuildPermissionFilter(user, roles, doctype)` generates an additional Meilisearch filter string that is ANDed on top of the NLP filters. This prevents users from seeing records they are not permitted to see, regardless of what they search for.

The RBAC engine runs **per query** with no DB round-trip — all permission data is derived from the session roles already present in the request context.

---

### 8. `search/ranking/` — Click Signal Tracker

**File:** `search/ranking/click_signals.go`

Every time a user opens a document from search results (`POST /api/v1/analytics/click`), the click is recorded in Redis as an incrementing counter keyed by `{site}:{doctype}:{name}`. On the next sync event for that document, the score is read back from Redis and stored as `default_click_score` in the Meilisearch document.

`config/ranking.yaml` adds `custom_click_score:desc` to the Meilisearch ranking rules list so frequently-clicked documents naturally rise in results.

---

### 9. `ai/` — Hybrid Vector Search (Optional)

**File:** `ai/embedder.go`

When `ai_mode: local` is set, the `Embedder` sends text to an HTTP embedding server (default `http://localhost:5000/embed`) and receives a float32 vector. Vectors are stored in Meilisearch documents under the `_vectors.default` key.

At query time, the search handler embeds the query text and passes `req.Vector` plus `req.Hybrid.SemanticRatio: 0.5` to Meilisearch, blending keyword and semantic scoring 50/50.

The embedding server is separate from the Lightning binary — any HTTP server that accepts `POST /embed` with `{"text": "..."}` and returns `{"vector": [...]}` works. The reference implementation uses `sentence-transformers/all-MiniLM-L6-v2` (384 dimensions).

---

### 10. `search/metrics/` — Prometheus Metrics

**File:** `search/metrics/metrics.go`

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `lightning_search_duration_seconds` | Histogram | `site` | Search request latency |
| `lightning_binlog_lag_seconds` | Gauge | `site` | Replication lag (MariaDB event timestamp vs now) |
| `lightning_indexing_total` | Counter | `site`, `doctype`, `action` | Documents indexed (insert/update) |
| `lightning_indexing_errors_total` | Counter | `site`, `doctype` | Indexing failures |

Exposed at `GET /metrics` (no auth required). Scrape with Prometheus and visualize in Grafana.

---

## Multi-Tenancy Model

Each Frappe site gets its own isolated pipeline:

- **Separate Meilisearch indexes** — namespaced as `{slugified_site}_{doctype_suffix}`. E.g., site `erp.local` → index `erp_local_sales_invoice`.
- **Separate Redis client** — each site points to its own Frappe Redis cache instance.
- **Separate goroutines** — binlog listener and sync engine are goroutine-isolated per site.
- **Shared HTTP server** — one Fiber instance dispatches to the correct site tenant via the `X-Frappe-Site-Name` header.
- **Shared binary** — one `go run main.go` process handles all sites.

---

## Index Naming Convention

```
{site_name_slugified}_{doctype_suffix}
```

Slugification replaces `.`, `-`, `/`, `:` with `_` and lowercases. Examples:

| Site name | DocType | Index name |
|-----------|---------|------------|
| `erp.local` | Sales Invoice | `erp_local_sales_invoice` |
| `demo.frappe.cloud` | Customer | `demo_frappe_cloud_customer` |
| `site1` | Purchase Order | `site1_purchase_order` |

---

## Failure Handling

| Failure | Behavior |
|---------|----------|
| Meilisearch unreachable at startup | Warning logged; index init skipped; sync continues when Meilisearch recovers |
| Batch flush fails | Exponential backoff retry (1s → 2s → 4s → … 60s max) |
| Permanently stuck batch | Moved to Dead Letter Queue; logged for manual inspection |
| Redis timeout during auth | 200ms timeout; returns `503` to the client |
| Canal connection drops | Canal library reconnects automatically; resumes from last saved position |
| Lightning binary crash and restart | Resumes binlog from `{site}.pos` file; no data loss for events after the last flush |
| Embedding server unreachable | Warning logged; query falls back to keyword-only search |
