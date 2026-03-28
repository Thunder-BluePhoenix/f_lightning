# The "Lightning" Philosophy

> **"Speed is not a feature. Speed is the feature."**

---

## 🧠 The Origin Idea

While Frappe's native **Global Search** is functional, it becomes visibly sluggish on sites with millions of records. It polls the database dynamically and processes results through multiple Python layers — each adding latency before data reaches the user.

**The Solution:** Use Go to build a **real-time sync bridge** between MariaDB and a blazing-fast search engine like Meilisearch or Typesense.

**How it works:**
> A dedicated Go service listens directly to the MariaDB **Binary Log** (via `go-mysql/canal`). Every time a record — a Sales Invoice, Customer, Item — is saved in Frappe, Go **instantly** captures the change and indexes it. Zero polling. Zero Python overhead.

**The Result:**
> A custom Go-powered search bar injected into the Frappe UI. True **"search-as-you-type"** experience across millions of records. **Sub-10ms latency**. Every. Single. Time.

**What this really is:**
> An **infra-level upgrade disguised as a feature**. Something Frappe itself should have shipped — but didn't. That's the opportunity.

---

## 🚀 The 12 Pillars of Lightning

### ⚡ 1. Smart Ranking & Relevance Engine
Don't just return results — return the *right* results.
- **Field-level boosting**: Customer name ranked above remarks; `name` beats description
- **Usage-based ranking**: recently/frequently accessed docs surface first
- **Role-aware ranking**: Sales users see invoices first; Purchase users see POs first
- **Typo tolerance**: `cust` → Customer, `inv` → Invoice, `suppiler` → Supplier
- **Synonyms**: invoice ↔ bill, customer ↔ client, item ↔ product ↔ SKU

> 👉 Makes it feel like Google inside Frappe.

---

### 🔍 2. Advanced Query Language (Power Users)
Give developers and admins superpowers with an explicit DSL:
```
customer:"Amazon" AND status:"Overdue"
amount > 50000
date: last_30_days
grand_total between 10000 50000
posting_date >= 2026-01-01
```
- Full boolean logic: `AND`, `OR`, `NOT`
- Numeric and date ranges
- Quoted field values for exact matching
- Saved searches (stored per-user in Frappe)
- Query suggestions as you type (powered by search logs)

> 👉 Power users will never leave.

---

### 🧠 3. AI Search Layer — Hybrid Strategy

The strategy is **"AI-feel without AI dependency first"**. Layer intelligence progressively without ever blocking the ship:

#### Phase 1 — No LLM Needed (Ship Now ✅)

| Capability | Approach |
|---|---|
| Natural Language → Structured Query | Rule-Based NLP (Go) |
| Semantic matching | Local embeddings (`fastText`, `word2vec`, `sentence-transformers`) |
| Result summarization | Template aggregations (`12 unpaid invoices totalling ₹2.3L`) |
| Smart suggestions | Query autocomplete + recent searches + synonym expansion |
| Typo correction | `invocie` → `invoice`, `suppiler` → `supplier` |

**Example NLP flow (zero LLM):**
```
"unpaid invoices from last month above 10k"
→ { doctype: "Sales Invoice", status: "Overdue", date: "last_month", amount: { gt: 10000 } }
```
This is **deterministic**, **fast**, **offline-safe**, and **enterprise-safe**.

#### Phase 2 — With LLM (Toggle On Later 🔮)
- Semantic search via vector embeddings (OpenAI or local Ollama)
- Contextual conversation: *"now filter that by region"*
- Flexible messy language: *"show me stuff I forgot to collect money from"*
- Result summarization with natural language descriptions

```
AI_MODE = off | local | cloud   ← feature flag, never blocks shipping
```

> ❌ Do not block shipping on LLM. Build the rule engine first, add AI as a "wow upgrade" later.

---

### 🔄 4. Real-Time Sync Engine (Production Grade)
Make the Go service bulletproof — this is what separates a project from a product:
- **Zero-latency binlog capture** — Go-MySQL Canal, no polling
- **Retry queues** — Kafka or Redis Streams (optional, feature-flagged)
- **Backfill indexing** — full `SELECT *` sweep of millions of existing rows before binlog takes over
- **Partial reindex** — DocType-level rebuild without full wipe
- **Conflict resolution** — GTID-based position persistence for safe restarts
- **Dead letter queue** — failed events logged and retried on recovery
- **Pluggable Hooks**: `before_index(doc)`, `transform_doc(doc)` per DocType

> 👉 This is what separates a project from a product.

---

### 📊 5. Search Analytics Dashboard
Inside Frappe Desk, a dedicated page to surface actionable intelligence:
- **Top searched terms** — what users want most
- **Zero-result queries** (🔥 gold for product and business teams — these are gaps)
- **Slow queries** — performance profiling targets
- **Click-through tracking** — relevance validation; if users don't click, ranking is wrong
- **Search volume trends** — per day, per user, per DocType
- **User search heatmap** — know which roles are searching for what

> 👉 Helps companies understand what their users are actually looking for. This is data you can't get from Frappe today.

---

### 🧩 6. Universal Search API (Public Layer)
Expose search as a clean, documented REST API:
```
GET /api/v1/search?q=...&doctype=...&site=...
GET /api/v1/suggest?q=...
GET /api/v1/autocomplete?q=...
GET /api/v1/health
```
- API token auth (separate from Frappe session) for external consumers
- Other apps, mobile clients, CLI tools, and integrations can consume it
- Rate-limited and versioned
- OpenAPI spec auto-generated from Go handler signatures

> 👉 Makes Lightning a **platform**, not just a plugin.

---

### ⚙️ 7. Pluggable Indexing System
Let developers customize the indexing pipeline per DocType without touching core code:
```go
// Hooks called by the Go sync engine
type IndexHooks struct {
    BeforeIndex  func(doc map[string]interface{}) bool
    TransformDoc func(doc map[string]interface{}) map[string]interface{}
}

// Example: exclude draft and cancelled documents
hooks.BeforeIndex = func(doc map[string]interface{}) bool {
    return doc["docstatus"] == 1 // submitted only
}
```
- Custom schema per DocType (index only relevant fields)
- Config-driven rules (YAML instead of code for non-Go developers)
- Multi-language synonym files
- Computed fields (e.g., `grand_total_display: "₹1,25,000"`)

> 👉 Makes Lightning extensible across any industry vertical.

---

### 🖥️ 8. Lightning UI Components
Upgrade Frappe UX so it feels modern and premium:
- **Command Palette** (VS Code / Raycast style — `Cmd+K` / `Ctrl+K`)
- Instant typeahead dropdown with categorised results (grouped by DocType)
- Full keyboard navigation (↑ ↓ arrows, `Enter` to open, `Esc` to close)
- **Inline preview panel** — see document details without navigating away
- Micro-animations — every keystroke feels alive
- Dark mode + Frappe theme variables — no hardcoded colors
- Debounced input (80ms) — zero wasted API calls

> 👉 This alone can make the app go viral in the Frappe community.

---

### 🔐 9. Permission-Aware Search
Critical for ERP: users must **never** see records they are not permitted to access.
- Validate Frappe session via Redis cache — fast, no DB round-trip
- Inject per-user `filter` expressions into every Meilisearch query before execution
- Support Meilisearch **tenant tokens** for index-level cryptographic isolation
- Role → DocType → field-level visibility mapping (configurable per role)
- Guest users get zero results — no accidental data exposure

> 👉 Without this, the search is unusable in real companies.

---

### 📦 10. Multi-Tenant / Multi-Site Support
Since Frappe runs multiple sites on one server:
- Isolated Meilisearch index per site: `{site}_sales_invoice`
- Namespace-based routing in the Go proxy — one binary, all sites
- Per-site configurable YAML: different DocTypes, fields, ranking rules
- Per-site API keys for Meilisearch
- Single Go binary serving all sites concurrently with goroutine isolation

---

### ⚡ 11. Offline / Edge Search (Advanced)
- Cache top-N frequently used index entries locally (browser `IndexedDB` or OS-level)
- Edge node deployment for geographically distributed ERP users
- Graceful degradation: fallback to standard Frappe search if Go proxy is unreachable
- Configurable cache TTL and invalidation strategy

---

### 🧪 12. Dev Tools & Debug Mode
For developers building on top of Lightning — and for understanding what the system is doing:
- **Live Binlog Viewer**: stream raw binlog events to a browser tab or terminal
- **Index Diff Checker**: compare MariaDB current state vs Meilisearch — highlight stale/missing docs
- **Reindex CLI**: `lightning reindex --doctype="Sales Invoice" --site=erp.local`
- **Latency Profiler**: complete breakdown of binlog → index → query → response timing
- **Debug Mode**: every NLP parse step logged with intermediate state
- **Rule Tester**: test NLP queries in isolation with `lightning parse "unpaid invoices above 10k"`

---

## 🎯 Design Principles

### "AI-Feel Without AI Dependency"
80% of what users expect from "AI search" can be delivered with deterministic engineering:

| What Users Want | How We Deliver It (No LLM) |
|---|---|
| Natural language queries | Rule-based NLP parser |
| Smart filters | Synonym + keyword mapping |
| "Feels intelligent" | Usage-based ranking + typo tolerance |
| Summary of results | Aggregation templates |
| "It knew what I meant" | DocType intent detection |

**Reserve LLM for what only LLMs can do:** reasoning, contextual conversation, fuzzy ambiguous language.

### "Deterministic Over Magic"
Every search result must be explainable. No hallucinations. No probabilistic surprises. The NLP parser is a **deterministic query interpreter** — given the same input, it always produces the same output. This is enterprise-safe.

### "Config Over Code"
Adding a new DocType to Lightning should require zero Go code. A YAML entry is enough. Adding synonyms is YAML. Adding hooks for non-Go devs is YAML. Go code is only required for maximal performance custom rules.

### "Fail Gracefully, Recover Automatically"
- If Meilisearch is unreachable: buffer events, retry with exponential backoff
- If binlog position is lost: alert, then resume from current tail
- If Redis session check fails: reject with 401 (never assume authenticated)
- If Go proxy is down: Frappe falls back to its own native search automatically

---

## 🔥 Ultimate Vision — The "Frappe Data Engine"

Transform Lightning from a search proxy into a full **real-time data intelligence layer** for Frappe:

```
Frappe  →  Go Data Engine  →  Meilisearch + Vector DB
                          →  Analytics DB (ClickHouse / TimescaleDB)
                          →  Event Stream (Kafka / Redis Streams)
                          →  AI Layer (Ollama / OpenAI / Gemini)
                          →  Universal REST API (external consumers)
```

> Search. Analytics. AI Insights. Event Streaming. All in one clean, open-source system built on top of Frappe.

**The evolution path:**

| Stage | What It Is |
|---|---|
| v1 — Lightning Search | Sub-10ms search in Frappe |
| v2 — Lightning Analytics | Real-time query intelligence + business insights |
| v3 — Lightning Engine | Full data intelligence platform for Frappe |
