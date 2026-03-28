# Phase 6: Smart Ranking, Pluggable Hooks & Multi-Tenancy

**Goal:** Make results smarter, make the system customizable for any industry vertical, and make it production-ready for multi-site Frappe deployments. This phase is what separates a demo from a product companies trust.

---

## 1. Smart Ranking Engine

### The Problem

Default Meilisearch ranking is generic: it doesn't know that a Sales Invoice `name` match (exact doc number) matters more than a match in `remarks`. It doesn't know that a user who clicks invoice X twenty times probably wants it first. It doesn't know that sales users care about invoices more than purchase orders.

### Solution: Config-Driven Field Boosting

```yaml
# config/ranking.yaml
ranking:
  Sales Invoice:
    field_weights:
      name:            10.0   # Exact doc reference match → highest priority
      customer_name:   7.0
      customer:        5.0
      status:          3.0
      grand_total:     2.0
      remarks:         0.5    # Notes are low-signal for search intent

  Customer:
    field_weights:
      name:            10.0
      customer_name:   8.0
      mobile_no:       6.0
      email_id:        5.0
      territory:       2.0

  Item:
    field_weights:
      item_code:   10.0
      item_name:   8.0
      description: 3.0
```

Applied to Meilisearch's `attributeWeights` setting on index initialization.

### Role-Aware Ranking

```go
// ranking/role_ranking.go
package ranking

// RoleIndexPriority defines which DocType indexes to query first per role
var RoleIndexPriority = map[string][]string{
    "Sales User":     {"sales_invoice", "customer", "item", "lead"},
    "Purchase User":  {"purchase_order", "supplier", "item"},
    "Accounts User":  {"sales_invoice", "purchase_invoice", "payment_entry"},
    "Stock User":     {"item", "delivery_note", "purchase_receipt"},
}

func GetIndexOrder(roles []string) []string {
    for _, role := range roles {
        if order, ok := RoleIndexPriority[role]; ok {
            return order
        }
    }
    return []string{"sales_invoice", "customer", "item"} // default
}
```

### Usage-Based Ranking (Click Signals)

Every time a user clicks a search result, a signal is stored in Redis:

```go
// ranking/signals.go
package ranking

import (
    "context"
    "fmt"
    "github.com/redis/go-redis/v9"
)

const clickKeyFmt = "lightning:clicks:%s:%s" // site:docname

func RecordClick(site, docname string, rdb *redis.Client) {
    key := fmt.Sprintf(clickKeyFmt, site, docname)
    rdb.Incr(context.Background(), key)
    rdb.Expire(context.Background(), key, 30*24*time.Hour) // 30-day rolling window
}

func GetClickScore(site, docname string, rdb *redis.Client) float64 {
    key := fmt.Sprintf(clickKeyFmt, site, docname)
    val, err := rdb.Get(context.Background(), key).Float64()
    if err != nil {
        return 0
    }
    return val
}
```

Click scores are used to boost documents in the ranking pipeline before returning results to the client.

---

## 2. Synonym Engine

Synonyms allow users to search naturally regardless of the exact Frappe field values:

```yaml
# config/synonyms.yaml
synonyms:
  global:
    - [invoice, bill, sinv]
    - [customer, client, buyer]
    - [item, product, sku, material]
    - [supplier, vendor, seller]
    - [purchase order, po, procurement]
    - [overdue, unpaid, pending payment]

  # Industry-specific synonym sets (loaded per-site config)
  manufacturing:
    - [item, bom, raw material]
    - [work order, production order, job]

  retail:
    - [customer, shopper, buyer, end user]
    - [item, sku, product, variant]
```

Applied to Meilisearch index settings on startup:

```go
func applySynonyms(index meilisearch.IndexManager, syns [][]string) {
    synMap := make(map[string][]string)
    for _, group := range syns {
        for _, term := range group {
            synMap[term] = group
        }
    }
    index.UpdateSynonyms(&synMap)
}
```

---

## 3. Pluggable Hook System (Full Implementation)

### Hook Registry

```go
// search/hooks.go
package search

import "sync"

type IndexHooks struct {
    BeforeIndex  func(doc map[string]interface{}) bool
    TransformDoc func(doc map[string]interface{}) map[string]interface{}
}

var (
    hooksMu sync.RWMutex
    registry = map[string]*IndexHooks{} // doctype → hooks
)

func RegisterHooks(doctype string, hooks *IndexHooks) {
    hooksMu.Lock()
    defer hooksMu.Unlock()
    registry[doctype] = hooks
}

func GetHooks(doctype string) *IndexHooks {
    hooksMu.RLock()
    defer hooksMu.RUnlock()
    return registry[doctype]
}
```

### Built-in Hook Examples

```go
func init() {
    // Sales Invoice: only index submitted documents, add display field
    RegisterHooks("Sales Invoice", &IndexHooks{
        BeforeIndex: func(doc map[string]interface{}) bool {
            docstatus, _ := doc["docstatus"].(int64)
            return docstatus == 1 // Skip Draft (0) and Cancelled (2)
        },
        TransformDoc: func(doc map[string]interface{}) map[string]interface{} {
            if gt, ok := doc["grand_total"].(float64); ok {
                doc["grand_total_display"] = formatINR(gt)
            }
            return doc
        },
    })

    // Customer: normalize email to lowercase for search consistency
    RegisterHooks("Customer", &IndexHooks{
        TransformDoc: func(doc map[string]interface{}) map[string]interface{} {
            if email, ok := doc["email_id"].(string); ok {
                doc["email_id"] = strings.ToLower(email)
            }
            return doc
        },
    })
}
```

### YAML-Driven Hooks (for non-Go developers)

```yaml
# config/hooks.yaml
hooks:
  Sales Invoice:
    skip_docstatus: [0, 2]        # Skip Draft and Cancelled
    computed_fields:
      - name: grand_total_display
        template: "₹{grand_total}"
      - name: overdue_label
        condition: "status == 'Overdue'"
        value: "⚠ Overdue"

  Item:
    skip_if:
      field: disabled
      value: 1
    computed_fields:
      - name: search_text
        concat: [item_code, item_name, description]
        separator: " | "
```

---

## 4. Multi-Tenant / Multi-Site Support

### Architecture

One Go binary. Multiple Frappe sites. Fully isolated.

```
config.yaml
└── sites:
    ├── erp.acme.com
    │   ├── mariadb: { host, port, user, pass, server_id: 100 }
    │   ├── meilisearch: { host, master_key }
    │   ├── redis: { host, port: 11000 }
    │   └── doctypes: [Sales Invoice, Customer, Item]
    │
    ├── erp.globex.com
    │   ├── mariadb: { host, port, user, pass, server_id: 200 }
    │   ├── meilisearch: { host, master_key }
    │   ├── redis: { host, port: 13000 }
    │   └── doctypes: [Purchase Order, Supplier, Item]
    │
    └── demo.frappe.cloud
        └── ...
```

Each site gets:
- Its own binlog listener goroutine with its own `server_id`
- Its own Meilisearch namespace: `erp_acme_com_sales_invoice`
- Its own Redis connection for session validation

### Site Detection in Proxy

```go
// api/middleware/site.go
func SiteMiddleware(c *fiber.Ctx) error {
    host := c.Hostname() // e.g. "erp.acme.com"
    site, ok := config.GetSite(host)
    if !ok {
        return c.Status(404).JSON(fiber.Map{"error": "unknown site"})
    }
    c.Locals("site", site)
    c.Locals("meili", site.MeiliClient)
    c.Locals("redis", site.RedisClient)
    return c.Next()
}
```

---

## 5. Offline / Edge Search

### Graceful Degradation

If the Go Lightning proxy is unreachable, the frontend automatically falls back to Frappe's native search:

```javascript
// In LightningSearch.search()
async search(query) {
    try {
        const res = await fetch(`/api/v1/search?q=${encodeURIComponent(query)}`, {
            credentials: 'include',
            signal: AbortSignal.timeout(3000), // 3s timeout
        });
        // ... render results
    } catch (err) {
        // Proxy is down — fall back to Frappe's built-in search
        this.renderError();
        frappe.utils.global_search(query); // Native Frappe search
    }
}
```

### Browser IndexedDB Cache (Top-N Results)

Frequently accessed results cached locally for instant recall:

```javascript
class LightningCache {
    async get(query) {
        const db = await this.openDB();
        const tx = db.transaction('queries', 'readonly');
        const record = await tx.store.get(query);
        if (record && Date.now() - record.ts < 5 * 60 * 1000) { // 5 min TTL
            return record.hits;
        }
        return null;
    }

    async set(query, hits) {
        const db = await this.openDB();
        const tx = db.transaction('queries', 'readwrite');
        tx.store.put({ query, hits, ts: Date.now() });
    }
}
```

---

## Validation Checklist

- [ ] Field weights in `ranking.yaml` applied correctly — test `name` match ranks above `remarks` match
- [ ] Role-aware index ordering: Sales User searches return invoices before purchase orders
- [ ] Click signal stored in Redis after clicking a result; score influences next search
- [ ] Synonyms: searching `"bill"` returns Sales Invoices; `"client"` returns Customers
- [ ] `BeforeIndex` hook prevents cancelled invoices from appearing in results
- [ ] `TransformDoc` hook adds `grand_total_display` field with Indian number format
- [ ] YAML hooks loaded and applied correctly without Go recompile
- [ ] Multi-site: requests from `erp.acme.com` only return that site's indexed data
- [ ] Two sites can run simultaneously with their own binlog listeners without conflict
- [ ] Graceful degradation: kill Go proxy → frontend shows error and falls back to Frappe search
- [ ] IndexedDB cache: repeat query returns instantly from cache within 5-minute TTL
