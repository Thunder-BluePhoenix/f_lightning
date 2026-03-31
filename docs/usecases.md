# Frappe Lightning: Use Cases & Query Reference

Lightning replaces Frappe's native Global Search with a sub-10ms engine backed by Meilisearch. This document shows what it can do with the six built-in DocTypes — and how to unlock more power with the Advanced DSL and AI hybrid mode.

---

## Built-in DocTypes

Lightning tracks these DocTypes out of the box. Every record change in Frappe is reflected in the search index within milliseconds via MariaDB binlog replication.

| DocType | Key searchable fields | Key filterable fields |
|---------|-----------------------|-----------------------|
| Sales Invoice | name, customer\_name, customer | status, grand\_total, posting\_date, company |
| Customer | name, customer\_name, mobile\_no, email\_id | customer\_group, territory |
| Item | name, item\_name, item\_code, description | item\_group, disabled |
| Purchase Order | name, supplier\_name, supplier | status, grand\_total, transaction\_date |
| Supplier | name, supplier\_name, mobile\_no, email\_id | supplier\_group, country |
| Lead | name, lead\_name, company\_name, email\_id | status, lead\_owner |

---

## 1. Finance & Accounting

### Finding overdue invoices

```
unpaid invoices above 10k
```
Filters applied: `status = "Overdue"`, `grand_total > 10000`

```
overdue invoices for Amazon
```
Filters applied: `status = "Overdue"` + keyword `Amazon` matched against `customer_name`

```
unpaid invoices last month
```
Filters applied: `status = "Overdue"`, `posting_date between <first_of_last_month> <last_of_last_month>`

```
invoices above 5 lakhs this month
```
Filters applied: `grand_total > 500000`, `posting_date between <first_of_this_month> <today>`

### Finding paid / submitted invoices

```
paid invoices yesterday
```
Filters applied: `status = "Paid"`, `posting_date between <yesterday_start> <yesterday_end>`

```
submitted invoices above 1cr
```
Filters applied: `status = "Submitted"`, `grand_total > 10000000`

### Range queries

```
10k to 50k invoices
```
Filters applied: `grand_total between 10000 50000`

```
draft POs above 2 lakhs
```
Filters applied: `status = "Draft"`, `grand_total > 200000`

---

## 2. Sales & CRM

### Leads

```
open leads
```
Filters applied: `status = "Open"`

```
active leads this week
```
Filters applied: `status = "Active"`, `posting_date between <week_start> <today>`

### Customers

```
customers
```
Full-text search across `customer_name`, `name`

```
recent customers
```
Usage-based ranking surfaces the most frequently accessed Customer records first, combined with recency sort on `customer_name`.

### Combining fields with Advanced DSL

```
customer:"Acme Corp" status:"Overdue"
```
Explicit filters: `customer = "Acme Corp"`, `status = "Overdue"` — no NLP inference needed.

```
customer:"Amazon" grand_total:>50000
```
Explicit filters: `customer = "Amazon"`, `grand_total > 50000`

---

## 3. Procurement & Inventory

### Purchase Orders

```
pending purchase orders
```
Filters applied: `status = "Pending"`

```
draft POs > 1cr
```
Filters applied: `status = "Draft"`, `grand_total > 10000000`

```
POs this year
```
Filters applied: `transaction_date between <jan_1_this_year> <dec_31_this_year>`

### Items

```
items below 500
```
Filters applied: `standard_rate < 500`

```
disabled items
```
Filters applied: `disabled = 1` (via Advanced DSL: `disabled:1`)

```
SKU: APP-LPT-001
```
Exact-match keyword search against `item_code` — returns instantly.

### Suppliers

```
suppliers
```
Full-text search across `supplier_name`, `name`

```
supplier:"Tata Motors"
```
Advanced DSL exact match on the supplier name field.

---

## 4. NLP Query Reference

The NLP engine tokenises queries into lowercase tokens and applies rules sequentially. Understanding the rules helps you write effective queries.

### Status keywords

| Query token | Filter injected |
|-------------|----------------|
| `unpaid` | `status = "Overdue"` |
| `overdue` | `status = "Overdue"` |
| `paid` | `status = "Paid"` |
| `draft` | `status = "Draft"` |
| `pending` | `status = "Pending"` |
| `submitted` | `status = "Submitted"` |
| `cancelled` | `status = "Cancelled"` |
| `completed` | `status = "Completed"` |
| `open` | `status = "Open"` |
| `closed` | `status = "Closed"` |
| `active` | `status = "Active"` |

### Amount keywords

Comparators: `above`, `greater`, `>` (for greater-than) and `below`, `less`, `under`, `<` (for less-than).

| Query fragment | Filter injected |
|----------------|----------------|
| `above 10k` | `grand_total > 10000` |
| `below 500` | `grand_total < 500` |
| `above 5 lakhs` | `grand_total > 500000` |
| `> 1cr` | `grand_total > 10000000` |
| `above 2.5m` | `grand_total > 2500000` |

Supported multiplier suffixes: `k` (×1,000), `l` / `lakh` / `lakhs` (×100,000), `m` / `million` (×1,000,000), `cr` / `crore` / `crores` (×10,000,000).

### Date shorthand

The date filter is applied to `posting_date` for invoices and `transaction_date` for purchase orders.

| Query token | Date range |
|-------------|-----------|
| `today` | Today 00:00 → 23:59 |
| `yesterday` | Yesterday 00:00 → 23:59 |
| `this week` | Start of current week → end of week |
| `last week` | Start of previous week → end of that week |
| `this month` | 1st of current month → last day |
| `last month` | 1st of previous month → last day |
| `this year` | 1 Jan → 31 Dec of current year |
| `last year` | 1 Jan → 31 Dec of previous year |

---

## 5. Advanced DSL

For queries where NLP inference is not precise enough, the Advanced DSL lets you specify filters explicitly. DSL tokens are extracted before NLP rules run.

**Pattern:** `field:[operator]value` or `field:"quoted value"`

```
status:"Overdue" grand_total:>50000
```

```
customer:"Reliance Industries" posting_date:>=2026-01-01
```

```
item_group:"Electronics" disabled:0
```

You can mix DSL tokens with natural language:

```
customer:"Acme Corp" unpaid last month
```

This produces: `customer = "Acme Corp"`, `status = "Overdue"`, and a date range for last month.

Supported operators: `=` (default when no operator given), `>`, `<`, `>=`, `<=`, `!=`.

---

## 6. Multi-Tenant Isolation

Lightning runs a separate index namespace per Frappe site (`{site}_{doctype}`). A user on `company-a.local` cannot ever see results from `company-b.local` — the site tenant is resolved from the `X-Frappe-Site-Name` header before any query is executed.

Within a site, RBAC filters are injected per user at query time based on their Frappe roles, ensuring that restricted users only see records they are permitted to access.

---

## 7. Hybrid AI Search (Optional)

When `ai_mode: local` is enabled, Lightning generates a vector embedding for both the indexed document and the search query, and blends keyword and semantic scoring 50/50.

This means:

```
footwear
```
Can match documents containing `shoes`, `sneakers`, or `sandals` — even if those exact words never appear in the query.

```
payment problem
```
Can surface documents tagged with `outstanding`, `overdue`, or `debt`.

AI mode requires a running embedding server (`all-MiniLM-L6-v2` by default). See the [developer guide](developer_guide.md) for setup instructions. AI mode is `off` by default and the engine functions fully without it.

---

## 8. Developer & Integration Use Cases

### REST API access from external tools

```bash
curl "http://localhost:8765/api/v1/search?q=unpaid+invoices+above+10k" \
  -H "Authorization: Bearer your-api-token" \
  -H "X-Frappe-Site-Name: erp.local"
```

The response includes a `parsed` field showing exactly what filters were applied — useful for debugging NLP interpretation:

```json
{
  "parsed": {
    "doctype": "Sales Invoice",
    "filters": [
      {"field": "status",      "op": "=", "value": "Overdue"},
      {"field": "grand_total", "op": ">", "value": 10000}
    ]
  },
  "took_ms": 6
}
```

### Slack / chatbot integration

Because the API accepts Bearer tokens, any internal tool can query Lightning directly. A Slack bot can answer "how many overdue invoices do we have?" by hitting `/api/v1/search?q=overdue+invoices` and counting `total` in the response.

### Custom dashboards

Fetch Lightning results in a React or Vue dashboard by pointing fetch calls at `GET /api/v1/search`. The JSON response shape is stable and pagination-friendly via the `limit` parameter.

### Validating NLP parsing during development

```bash
lightning parse "draft POs above 2 lakhs this month"
lightning parse 'customer:"Acme Corp" status:"Overdue"'
lightning parse "items below 500"
```

Each call prints both the structured `Query{}` object and the exact Meilisearch DSL params — no server needed.
