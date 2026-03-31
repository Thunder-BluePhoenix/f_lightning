# Frappe Lightning: Developer Guide

This guide explains how to extend Lightning without forking the core engine. It covers adding custom DocTypes, writing indexing hooks, extending the NLP rule engine, adding DocType intent keywords, testing, and running the local AI embedding server.

---

## Table of Contents

1. [Adding a Custom DocType](#1-adding-a-custom-doctype)
2. [Indexing Hooks — BeforeIndex & TransformDoc](#2-indexing-hooks--beforeindex--transformdoc)
3. [Extending the NLP Rule Engine](#3-extending-the-nlp-rule-engine)
4. [Adding DocType Intent Keywords](#4-adding-doctype-intent-keywords)
5. [Advanced DSL Syntax](#5-advanced-dsl-syntax)
6. [Backfill API](#6-backfill-api)
7. [Running the Local AI Embedding Server](#7-running-the-local-ai-embedding-server)
8. [Testing](#8-testing)
9. [Project Layout Reference](#9-project-layout-reference)

---

## 1. Adding a Custom DocType

### Step 1 — Register the schema

Open `frappe_lightning/config/schema.go` and add an entry to `DefaultSchemas()`:

```go
{
    Name:        "Work Order",
    Table:       "tabWork Order",
    IndexSuffix: "work_order",
    Fields: []string{
        "name", "production_item", "company", "status",
        "qty", "produced_qty", "planned_start_date", "docstatus",
    },
    Searchable:  []string{"name", "production_item"},
    Filterable:  []string{"status", "company", "planned_start_date", "doctype"},
    Sortable:    []string{"planned_start_date", "qty"},
},
```

**Fields reference:**

| Field | Purpose |
|-------|---------|
| `Fields` | Columns extracted from the binlog row. Only listed fields are written to Meilisearch — omit large text/blob columns you don't need to search. |
| `Searchable` | Fields Meilisearch uses for full-text search. Order matters — earlier fields rank higher. |
| `Filterable` | Fields that can appear in filter expressions (`status = "Draft"`). Required for NLP-generated and RBAC filters to work. |
| `Sortable` | Fields available for explicit `sort` parameters. |

### Step 2 — Add the DocType to `config.yaml`

```yaml
doctypes:
  - name: Work Order
    table: "tabWork Order"
```

### Step 3 — Restart Lightning and run backfill

```bash
sudo systemctl restart frappe-lightning

lightning backfill \
  --config /etc/frappe-lightning/config.yaml \
  --site erp.local \
  --doctype "Work Order"
```

That's all. The canal listener will automatically include the new table in its regex filter, and the sync engine will begin processing its events.

---

## 2. Indexing Hooks — `BeforeIndex` & `TransformDoc`

Hooks let you customise what gets indexed and how, without changing the sync engine. They are registered in Go and run synchronously in the event processing goroutine.

Hooks live in `frappe_lightning/search/hooks.go`.

### Default Behaviour

Two DocTypes have built-in hooks registered by `registerDefaults()`:
- **Sales Invoice** — only indexes submitted documents (`docstatus = 1`). Drafts and cancelled invoices are never indexed.
- **Purchase Order** — same rule.

### Writing a `BeforeIndex` Hook

`BeforeIndex` receives the document map and returns `true` to allow indexing or `false` to drop it.

**Example: Skip disabled Items**

```go
registry.Register("Item", &search.IndexHooks{
    BeforeIndex: func(doc map[string]interface{}) bool {
        if disabled, ok := doc["disabled"]; ok {
            switch v := disabled.(type) {
            case int64:
                return v == 0
            case float64:
                return v == 0
            }
        }
        return true
    },
})
```

**Example: Only index submitted Work Orders**

```go
registry.Register("Work Order", &search.IndexHooks{
    BeforeIndex: func(doc map[string]interface{}) bool {
        ds, ok := doc["docstatus"]
        if !ok { return true }
        if v, ok := ds.(int64); ok { return v == 1 }
        if v, ok := ds.(float64); ok { return int(v) == 1 }
        return true
    },
})
```

### Writing a `TransformDoc` Hook

`TransformDoc` receives the document map and must return the (optionally mutated) document. Use this to add computed fields, normalise values, or join data from another source.

**Example: Add a display label field**

```go
registry.Register("Item", &search.IndexHooks{
    TransformDoc: func(doc map[string]interface{}) map[string]interface{} {
        code, _ := doc["item_code"].(string)
        name, _ := doc["item_name"].(string)
        if code != "" && name != "" {
            doc["display_label"] = code + " — " + name
        }
        return doc
    },
})
```

**Example: Normalise currency to uppercase**

```go
registry.Register("Sales Invoice", &search.IndexHooks{
    TransformDoc: func(doc map[string]interface{}) map[string]interface{} {
        if cur, ok := doc["currency"].(string); ok {
            doc["currency"] = strings.ToUpper(cur)
        }
        return doc
    },
})
```

### Combining Both Hooks

```go
registry.Register("Delivery Note", &search.IndexHooks{
    BeforeIndex: func(doc map[string]interface{}) bool {
        // Only index submitted delivery notes
        if ds, ok := doc["docstatus"].(int64); ok {
            return ds == 1
        }
        return true
    },
    TransformDoc: func(doc map[string]interface{}) map[string]interface{} {
        // Add a searchable full-address field
        city, _ := doc["shipping_address_city"].(string)
        country, _ := doc["shipping_address_country"].(string)
        doc["shipping_location"] = city + ", " + country
        return doc
    },
})
```

### Where to Register Hooks

Hooks must be registered **before** `engine.Start()` is called. The appropriate place is in `main.go`, after the engine is created:

```go
engine := search.NewEngine(site, schemas, meiliClient, rdb, cfg.AIMode, embedder, log)

// Register custom hooks
engine.Hooks().Register("Item", &search.IndexHooks{ ... })
engine.Hooks().Register("Work Order", &search.IndexHooks{ ... })

go engine.Start(eventsCh)
```

> `engine.Hooks()` is a getter you add to the `Engine` struct to expose the `HookRegistry`. If it doesn't exist yet, add: `func (e *Engine) Hooks() *HookRegistry { return e.hooks }`.

---

## 3. Extending the NLP Rule Engine

The NLP pipeline applies a slice of `Rule` functions to the token stream. Adding a new rule means writing a function and appending it to the `Rules` slice in `nlp/builder.go`.

### Rule Function Signature

```go
// A Rule receives the lowercased token slice and returns a Filter or nil.
type Rule func(tokens []string) *Filter
```

### Filter Struct (`nlp/models.go`)

```go
type Filter struct {
    Field string      // Meilisearch field name
    Op    string      // =  >  <  >=  <=  between
    Value interface{} // scalar or []time.Time for between
}
```

### Example: Detect a Territory Filter

Add to `nlp/rules.go`:

```go
var TerritoryKeywords = map[string]string{
    "mumbai":    "Mumbai",
    "delhi":     "Delhi",
    "bangalore": "Bangalore",
    "hyderabad": "Hyderabad",
    "chennai":   "Chennai",
}

// DetectTerritoryFilter looks for "in <city>" patterns.
func DetectTerritoryFilter(tokens []string) *Filter {
    for i, t := range tokens {
        if t == "in" && i+1 < len(tokens) {
            if city, ok := TerritoryKeywords[tokens[i+1]]; ok {
                return &Filter{
                    Field: "territory",
                    Op:    "=",
                    Value: city,
                }
            }
        }
    }
    return nil
}
```

Register it in `nlp/builder.go`:

```go
var Rules = []Rule{
    DetectAmountFilter,
    ParseDateRange,
    DetectStatusFilter,
    DetectTerritoryFilter,   // ← add your rule here
}
```

Now queries like `"customers in Mumbai"` or `"leads in Delhi"` automatically inject `territory = "Mumbai"` as a Meilisearch filter.

### Testing Your Rule

Use the CLI immediately:

```bash
lightning parse "customers in Mumbai" --config config.yaml
```

Expected output:

```json
{
  "text": "customers in Mumbai",
  "doctype": "Customer",
  "filters": [
    {"field": "territory", "op": "=", "value": "Mumbai"}
  ],
  "limit": 20
}
```

---

## 4. Adding DocType Intent Keywords

The NLP engine identifies the target DocType from the token stream using `DocTypeMap` in `nlp/intent.go`. To make a new DocType discoverable from natural language, add aliases:

```go
var DocTypeMap = map[string]string{
    // ... existing entries ...
    "work order":   "Work Order",
    "wo":           "Work Order",
    "production":   "Work Order",
    "delivery":     "Delivery Note",
    "delivery note":"Delivery Note",
    "dn":           "Delivery Note",
    "journal":      "Journal Entry",
    "jv":           "Journal Entry",
}
```

After this change, queries like `"draft WOs this month"` will correctly identify `Work Order` as the DocType and route to the `{site}_work_order` Meilisearch index.

---

## 5. Advanced DSL Syntax

Power users can bypass the NLP engine's heuristics with explicit field:operator:value expressions. The Advanced DSL parser (`nlp/advanced.go`) handles this before the rule chain runs.

**Pattern:** `field:[operator]value` or `field:"quoted value"`

```
status:"Overdue"
grand_total:>50000
posting_date:>=2026-01-01
customer:"Acme Corp"
amount:>10k
```

Multiple DSL tokens can be combined with free-text:

```
customer:"Acme Corp" status:"Overdue" last month
```

This produces:
- Filter: `customer = "Acme Corp"`
- Filter: `status = "Overdue"`
- NLP date filter: `posting_date between <last_month_start> <last_month_end>`
- Remaining keyword text: _(empty after DSL extraction)_

The regex pattern is:

```
(\w+):(?:([<>=!]+)?)([^\s"]+|"[^"]+")
```

Supported operators: `=` (default), `>`, `<`, `>=`, `<=`, `!=`.

---

## 6. Backfill API

The backfill function (`cmd/backfill.go`) is also callable programmatically from your own Go code:

```go
import (
    "frappe_lightning/cmd"
    "frappe_lightning/config"
)

schemas := config.DefaultSchemas()
schema, _ := config.SchemaByTable(schemas, "tabWork Order")

err := cmd.Backfill(siteConfig, *schema, meiliClient, log)
```

**Backfill behaviour:**
- Connects directly to MariaDB via SQL (not the binlog).
- Selects only the fields listed in `schema.Fields`.
- Uploads in batches of 5,000 documents.
- Sleeps 100ms between batches to avoid overloading MariaDB.
- Uses `name` as the Meilisearch primary key — re-running the backfill is idempotent (upserts).
- Safe to run while the main service is running.

---

## 7. Running the Local AI Embedding Server

When `ai_mode: local` is set, Lightning expects an HTTP server at `embedding_server_url` that converts text to a float32 vector.

### Reference Python Server

```python
# embed_server.py
from flask import Flask, request, jsonify
from sentence_transformers import SentenceTransformer

app = Flask(__name__)
model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")

@app.route("/embed", methods=["POST"])
def embed():
    text = request.json.get("text", "")
    embedding = model.encode(text).tolist()
    return jsonify({"embedding": embedding})

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000)
```

Install and run:

```bash
pip install flask sentence-transformers
python embed_server.py
```

### Config alignment

The model `all-MiniLM-L6-v2` outputs 384-dimensional vectors. Make sure your `config.yaml` matches:

```yaml
ai_mode: local
embedding_server_url: "http://localhost:5000/embed"
vector_dimensions: 384
```

### Protocol

Lightning sends:
```json
POST /embed
{"text": "unpaid invoices above 10k"}
```

Your server must return:
```json
{"embedding": [0.012, -0.034, 0.091, ...]}   // length must equal vector_dimensions
```

The field name is `embedding` — matching `EmbedResponse.Embedding` in `ai/embedder.go`.

### Using a Different Model

To use a 768-dimension model (e.g., `all-mpnet-base-v2`):

1. Change `vector_dimensions: 768` in `config.yaml`.
2. Update the Python server to load the new model.
3. **Delete and recreate all Meilisearch indexes** — dimension changes are not backward compatible.

---

## 8. Testing

### Unit Tests — NLP

The NLP package has table-driven tests in `nlp/builder_test.go`. Run them with:

```bash
cd frappe_lightning
go test ./nlp/... -v
```

To add a test case for a new rule, extend the table in `builder_test.go`:

```go
{
    name:  "territory filter",
    input: "customers in Mumbai",
    wantDocType: "Customer",
    wantFilters: []nlp.Filter{
        {Field: "territory", Op: "=", Value: "Mumbai"},
    },
},
```

### Unit Tests — Embedder

```bash
go test ./ai/... -v
```

The `ai/embedder_test.go` file tests the HTTP client logic with a mock server.

### Integration Testing the NLP CLI

The fastest way to validate NLP changes without spinning up the full stack:

```bash
lightning parse "draft work orders this week" --config config.yaml
lightning parse 'customer:"Acme Corp" status:"Overdue"' --config config.yaml
lightning parse "items below 500" --config config.yaml
```

### Integration Testing the Full Stack

1. Start Meilisearch, Redis, and a test Frappe bench.
2. Run backfill for one DocType.
3. Create a record in Frappe and wait up to 500ms.
4. Query the Lightning API and verify the record appears.

Minimal curl integration test:

```bash
# Create a record in Frappe (via Frappe REST API), then:
sleep 1
curl "http://localhost:8765/api/v1/search?q=new+customer+name" \
  -H "Authorization: Bearer lightning-secret-dev" \
  -H "X-Frappe-Site-Name: erp.local" \
  | jq '.hits[0].customer_name'
```

### Checking Sync Integrity

```bash
# Are MariaDB and Meilisearch in sync?
lightning diff erp.local "Sales Invoice" --config config.yaml

# Watch live events hitting the binlog listener
lightning watch erp.local --config config.yaml
```

---

## 9. Project Layout Reference

```
frappe_lightning/
├── main.go                    # Entry point — bootstrap, multi-tenant setup, graceful shutdown
├── config.yaml.example        # Example configuration
│
├── config/
│   ├── config.go              # Config struct + Load() function
│   ├── schema.go              # IndexSchema definitions + DefaultSchemas()
│   ├── ranking.go             # RankingConfig struct + LoadRanking()
│   └── ranking.yaml           # Per-DocType ranking rules and synonyms
│
├── canal/
│   ├── listener.go            # go-mysql/canal handler + Start() function
│   └── position.go            # Binlog position persistence (save/load .pos files)
│
├── search/
│   ├── engine.go              # Engine — event processing, document mapping, index writes
│   ├── batcher.go             # Batching layer (100 docs OR 500ms flush)
│   ├── hooks.go               # HookRegistry, IndexHooks, default hooks
│   ├── dlq.go                 # Dead letter queue for permanently failed batches
│   └── metrics/
│       └── metrics.go         # Prometheus metric registrations
│
├── search/ranking/
│   └── click_signals.go       # Redis-backed click score tracker
│
├── nlp/
│   ├── models.go              # Query and Filter structs
│   ├── tokenizer.go           # Tokenize() — lowercase + split
│   ├── intent.go              # DocTypeMap + DetectDocType()
│   ├── rules.go               # DetectAmountFilter, ParseDateRange, DetectStatusFilter
│   ├── advanced.go            # Advanced DSL parser (field:op:value syntax)
│   ├── builder.go             # BuildQuery() — main pipeline; Rules slice
│   ├── meili.go               # ToMeili() — Query{} → Meilisearch params
│   └── builder_test.go        # Table-driven NLP unit tests
│
├── api/
│   ├── server.go              # Fiber app setup, middleware, route registration
│   ├── middleware/
│   │   ├── site.go            # SiteResolver — tenant dispatch via X-Frappe-Site-Name
│   │   └── auth.go            # AuthRequired — sid cookie + Bearer token validation
│   ├── handlers/
│   │   ├── search.go          # Search handler — NLP + RBAC + Meilisearch + metrics
│   │   └── handlers.go        # HealthCheck, Suggest, Autocomplete, LogClick
│   └── rbac/
│       └── rbac.go            # BuildPermissionFilter() — role → Meilisearch filter
│
├── ai/
│   ├── embedder.go            # HTTP client for the embedding server
│   └── embedder_test.go       # Unit tests for the embedder client
│
└── cmd/
    ├── backfill.go            # Backfill() — full SQL sweep into Meilisearch
    └── lightning/
        └── main.go            # lightning CLI (status, watch, diff, parse)
```
