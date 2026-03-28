# Phase 2: Indexing & Data Pipeline

**Goal:** Transform raw MariaDB binlog row events into structured Meilisearch documents and keep the index perfectly in sync in real-time. This phase covers INSERT, UPDATE, DELETE handling, schema mapping, batching, retry logic, backfill, and pluggable indexing hooks.

---

## DocType Schema Mapping

Each Frappe DocType maps to a Meilisearch index with a curated set of fields. The schema is driven entirely by `config/schema.yaml` — no code changes needed to add fields:

```yaml
# config/schema.yaml
doctypes:
  - name: "Sales Invoice"
    table: "tabSales Invoice"
    index: "{site}_sales_invoice"
    fields:
      - name
      - customer
      - customer_name
      - status
      - grand_total
      - outstanding_amount
      - posting_date
      - due_date
      - company
      - currency
      - owner
    searchable:  [name, customer_name, customer]
    filterable:  [status, company, posting_date, grand_total, outstanding_amount, currency]
    sortable:    [posting_date, grand_total, outstanding_amount]

  - name: "Customer"
    table: "tabCustomer"
    index: "{site}_customer"
    fields:
      - name
      - customer_name
      - customer_group
      - customer_type
      - territory
      - mobile_no
      - email_id
      - website
    searchable:  [name, customer_name, mobile_no, email_id]
    filterable:  [customer_group, customer_type, territory]
    sortable:    [customer_name]

  - name: "Item"
    table: "tabItem"
    index: "{site}_item"
    fields:
      - name
      - item_name
      - item_code
      - item_group
      - description
      - standard_rate
      - stock_uom
      - disabled
    searchable:  [name, item_name, item_code, description]
    filterable:  [item_group, disabled, stock_uom]
    sortable:    [item_name, standard_rate]

  - name: "Purchase Order"
    table: "tabPurchase Order"
    index: "{site}_purchase_order"
    fields:
      - name
      - supplier
      - supplier_name
      - status
      - grand_total
      - transaction_date
      - schedule_date
      - company
    searchable:  [name, supplier_name, supplier]
    filterable:  [status, company, transaction_date, grand_total]
    sortable:    [transaction_date, grand_total]
```

---

## Meilisearch Index Initialization

On startup, the Go service ensures each configured index exists with correct attribute settings:

```go
// search/meilisearch.go
package search

import (
    "github.com/meilisearch/meilisearch-go"
    "frappe_lightning/config"
)

func InitIndex(client meilisearch.ServiceManager, indexName string, s config.IndexSchema) error {
    index := client.Index(indexName)

    // Settings are applied idempotently — safe to call on every startup
    _, err := index.UpdateSettings(&meilisearch.Settings{
        SearchableAttributes: s.Searchable,
        FilterableAttributes: s.Filterable,
        SortableAttributes:   s.Sortable,
        RankingRules: []string{
            "words", "typo", "proximity", "attribute", "sort", "exactness",
        },
        DistinctAttribute: ptr("name"),
        Synonyms: map[string][]string{
            "invoice":  {"bill", "sinv"},
            "customer": {"client", "buyer"},
            "item":     {"product", "sku"},
        },
    })
    return err
}

func ptr(s string) *string { return &s }
```

---

## Row → Document Mapping

```go
// search/mapper.go
package search

import (
    "fmt"
    "time"
)

// mapRowToDoc converts a raw binlog row (slice of interface{}) into a
// map[string]interface{} document suitable for Meilisearch,
// using the column list from the binlog schema event.
func mapRowToDoc(row []interface{}, columns []string, allowedFields map[string]bool) map[string]interface{} {
    doc := make(map[string]interface{}, len(columns))
    for i, col := range columns {
        if !allowedFields[col] {
            continue
        }
        val := row[i]
        // Normalize MariaDB types
        switch v := val.(type) {
        case []byte:
            doc[col] = string(v)
        case time.Time:
            doc[col] = v.Unix() // Store as UNIX timestamp for Meilisearch range filters
        default:
            doc[col] = v
        }
    }
    return doc
}
```

---

## Event Processing

```go
// search/processor.go
package search

func (s *SyncEngine) processEvent(event *canal.RowEvent) error {
    schema, ok := s.schemas[event.Table]
    if !ok {
        return nil // Not a tracked DocType
    }

    indexName := fmt.Sprintf("%s_%s", s.site, schema.IndexSuffix)
    index := s.meili.Index(indexName)

    switch event.Action {
    case "insert":
        // New document — full upsert
        doc := mapRowToDoc(event.Rows[0], event.Columns, schema.AllowedFields)
        if s.applyBeforeIndex(doc) {
            doc = s.applyTransformDoc(doc)
            s.batcher.Add(indexName, doc)
        }

    case "update":
        // Binlog UPDATE delivers before+after rows
        after := mapRowToDoc(event.Rows[1], event.Columns, schema.AllowedFields)
        if s.applyBeforeIndex(after) {
            after = s.applyTransformDoc(after)
            s.batcher.Add(indexName, after)
        }

    case "delete":
        before := mapRowToDoc(event.Rows[0], event.Columns, schema.AllowedFields)
        if id, ok := before["name"].(string); ok {
            index.DeleteDocuments([]string{id})
        }
    }
    return nil
}
```

---

## Batching & Retry Logic

For bursts (e.g., bulk imports via Frappe Data Import Tool):

```go
// search/batcher.go
package search

import (
    "sync"
    "time"
)

type Batcher struct {
    mu       sync.Mutex
    buffers  map[string][]interface{} // indexName → pending docs
    maxSize  int
    maxWait  time.Duration
    flushFn  func(indexName string, docs []interface{}) error
    deadLetter []failedBatch
}

type failedBatch struct {
    indexName string
    docs      []interface{}
    attempts  int
}

func NewBatcher(flushFn func(string, []interface{}) error) *Batcher {
    b := &Batcher{
        buffers:  make(map[string][]interface{}),
        maxSize:  100,                 // Flush after 100 documents
        maxWait:  500 * time.Millisecond, // Or after 500ms — whichever first
        flushFn:  flushFn,
    }
    go b.ticker()
    return b
}

func (b *Batcher) Add(indexName string, doc interface{}) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.buffers[indexName] = append(b.buffers[indexName], doc)
    if len(b.buffers[indexName]) >= b.maxSize {
        b.flush(indexName)
    }
}

func (b *Batcher) flush(indexName string) {
    docs := b.buffers[indexName]
    b.buffers[indexName] = nil
    go b.sendWithRetry(indexName, docs, 0)
}

func (b *Batcher) sendWithRetry(indexName string, docs []interface{}, attempt int) {
    delays := []time.Duration{1*time.Second, 2*time.Second, 4*time.Second, 8*time.Second, 60*time.Second}
    if err := b.flushFn(indexName, docs); err != nil {
        if attempt < len(delays) {
            time.Sleep(delays[attempt])
            b.sendWithRetry(indexName, docs, attempt+1)
        } else {
            b.deadLetter = append(b.deadLetter, failedBatch{indexName, docs, attempt})
        }
    }
}
```

---

## Binlog Position Persistence

To safely restart the service without re-processing old events:

```go
// canal/position.go
package canal

import (
    "encoding/json"
    "os"
    "path/filepath"
)

type BinlogPosition struct {
    File string `json:"file"`
    Pos  uint32 `json:"pos"`
    GTID string `json:"gtid,omitempty"`
}

const posFile = "~/.lightning/binlog_pos.json"

func SavePosition(pos BinlogPosition) error {
    path, _ := filepath.Abs(posFile)
    data, _ := json.Marshal(pos)
    return os.WriteFile(path, data, 0644)
}

func LoadPosition() (*BinlogPosition, error) {
    path, _ := filepath.Abs(posFile)
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err // First run — start from current binlog tail
    }
    var pos BinlogPosition
    return &pos, json.Unmarshal(data, &pos)
}
```

Position is saved after every successful batch flush. On restart, if `binlog_pos.json` exists, Canal resumes at the exact saved position — zero event loss.

---

## Initial Full-Sync Backfill

A one-time command to index all existing rows on a live site before the binlog listener takes over:

```bash
lightning backfill --doctype="Sales Invoice" --site=erp.local --batch=500
lightning backfill --all --site=erp.local --batch=500
```

```go
// cmd/backfill.go
func Backfill(db *sql.DB, meili meilisearch.ServiceManager, schema config.DocTypeSchema, site string, batchSize int) error {
    table := schema.Table
    index := meili.Index(fmt.Sprintf("%s_%s", site, schema.IndexSuffix))

    var offset int
    for {
        rows, err := db.Query(fmt.Sprintf(
            "SELECT %s FROM `%s` LIMIT %d OFFSET %d",
            strings.Join(schema.Fields, ","), table, batchSize, offset,
        ))
        if err != nil { return err }

        docs := rowsToDocs(rows, schema)
        if len(docs) == 0 { break }

        index.AddDocuments(docs, "name")
        offset += batchSize
        fmt.Printf("  ✔ Indexed %d records from %s\n", offset, table)
    }
    return nil
}
```

> ⚠️ Backfill must complete before the binlog listener starts. Use `--save-position` flag to snapshot the current binlog position at backfill start, then resume from there when the listener begins.

---

## Pluggable Hooks

Developers can register Go functions to customize the indexing pipeline per DocType:

```go
// search/hooks.go
package search

// IndexHooks lets callers customize the indexing pipeline
type IndexHooks struct {
    // BeforeIndex: return false to skip indexing this document entirely
    BeforeIndex func(doc map[string]interface{}) bool

    // TransformDoc: modify the document before it reaches Meilisearch
    TransformDoc func(doc map[string]interface{}) map[string]interface{}
}

// Example: skip cancelled and draft documents
hooks.BeforeIndex = func(doc map[string]interface{}) bool {
    docstatus, _ := doc["docstatus"].(int64)
    return docstatus == 1 // Only index submitted documents
}

// Example: add a computed field for display
hooks.TransformDoc = func(doc map[string]interface{}) map[string]interface{} {
    if gt, ok := doc["grand_total"].(float64); ok {
        doc["grand_total_display"] = fmt.Sprintf("₹%.2f", gt)
    }
    return doc
}
```

Hooks can also be configured via YAML for non-Go developers:

```yaml
# config/hooks.yaml
hooks:
  Sales Invoice:
    skip_docstatus: [0, 2]   # Skip Draft and Cancelled
    computed_fields:
      - name: grand_total_display
        template: "₹{grand_total}"
```

---

## Validation Checklist

- [ ] Schema YAML loaded correctly at startup with no missing fields
- [ ] Meilisearch indexes created with correct searchable/filterable/sortable settings
- [ ] `INSERT` event → document appears in Meilisearch index within 500ms
- [ ] `UPDATE` event → document fields updated correctly (check via Meilisearch UI)
- [ ] `DELETE` event → document removed from index; search returns 0 results for that name
- [ ] Batcher flushes at 100 docs OR 500ms — verified via logs
- [ ] Retry logic kicks in when Meilisearch is unreachable; events recovered on reconnect
- [ ] Dead letter queue logs stuck events after max retries
- [ ] Backfill script runs successfully on 100k+ rows with correct progress output
- [ ] Binlog position saved to disk after each flush
- [ ] Restarting the Go service resumes from saved position — no duplicate events
- [ ] `BeforeIndex` hook correctly prevents cancelled docs from being indexed
- [ ] `TransformDoc` hook correctly adds computed fields to indexed documents
