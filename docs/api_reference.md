# Frappe Lightning: API Reference

The Lightning API is a JSON HTTP API served by the Go binary. All endpoints are under the base URL of the Lightning server (default `http://localhost:8765`).

---

## Authentication

Every protected endpoint requires one of two auth methods. Send exactly one per request.

### Method 1 — Frappe Session Cookie

For browser-based requests originating from the Frappe Desk. The browser automatically sends the `sid` cookie set by Frappe on login.

```
Cookie: sid=<frappe_session_id>
X-Frappe-Site-Name: erp.local
```

The Lightning middleware validates the `sid` against Frappe's Redis session store in under 200ms. No database call is made.

### Method 2 — Bearer Token

For external applications, mobile clients, and programmatic access.

```
Authorization: Bearer <api_token>
X-Frappe-Site-Name: erp.local
```

The token is matched against the `api_token` field in `config.yaml`. On success, the caller is treated as `"api_user"` with roles `["System Manager", "Search API User"]`, bypassing per-user permission filters.

### Error Responses

| Condition | Status | Body |
|-----------|--------|------|
| Missing `sid` cookie | `401` | `{"error": "missing session cookie"}` |
| Invalid or expired session | `401` | `{"error": "invalid or expired session"}` |
| Invalid Bearer token | `401` | `{"error": "invalid API token"}` |
| Guest session | `401` | `{"error": "forbidden"}` |
| No matching site tenant | `404` | `{"error": "site not found"}` |
| Rate limit exceeded (150 req / 10s per IP) | `429` | `{"error": "too many requests"}` |

---

## Site Routing

Every protected request must include the `X-Frappe-Site-Name` header. This tells Lightning which site tenant to use for Meilisearch, Redis, and permission lookups.

```
X-Frappe-Site-Name: erp.local
```

The value must match a `name` entry in `config.yaml > sites[]`.

---

## Endpoints

---

### `GET /api/v1/health`

Health check for the Lightning service and its dependencies. No authentication required.

**Request**

```
GET /api/v1/health
X-Frappe-Site-Name: erp.local
```

**Response `200 OK`**

```json
{
  "status": "online",
  "dependencies": {
    "redis": "ok",
    "meilisearch": "ok"
  }
}
```

**Response `503 Service Unavailable`** (when a dependency is down)

```json
{
  "status": "online",
  "dependencies": {
    "redis": "unreachable",
    "meilisearch": "ok"
  }
}
```

---

### `GET /api/v1/search`

The primary search endpoint. Accepts a natural language query, parses it with the NLP engine, injects RBAC filters, and returns Meilisearch results.

**Request**

```
GET /api/v1/search?q=unpaid+invoices+above+10k&limit=20
X-Frappe-Site-Name: erp.local
Cookie: sid=<session_id>
```

**Query Parameters**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Natural language search query |
| `limit` | int | No | Max results to return (default: 20) |

**Response `200 OK`**

```json
{
  "hits": [
    {
      "name": "ACC-SINV-2025-00042",
      "customer_name": "Acme Corp",
      "customer": "Acme Corp",
      "status": "Overdue",
      "grand_total": 85000,
      "outstanding_amount": 85000,
      "posting_date": 1736899200,
      "doctype": "Sales Invoice"
    }
  ],
  "parsed": {
    "text": "unpaid invoices above 10k",
    "doctype": "Sales Invoice",
    "filters": [
      {"field": "status",      "op": "=",  "value": "Overdue"},
      {"field": "grand_total", "op": ">",  "value": 10000}
    ],
    "limit": 20
  },
  "took_ms": 7,
  "total": 1,
  "ai_enabled": false,
  "is_semantic": false
}
```

**Response fields**

| Field | Type | Description |
|-------|------|-------------|
| `hits` | array | Meilisearch result documents. Field set depends on the DocType schema. |
| `parsed` | object | The structured query the NLP engine produced from `q`. Useful for debugging. |
| `took_ms` | int | Total round-trip latency in milliseconds (NLP + RBAC + Meilisearch). |
| `total` | int | Total matching documents in the index (before `limit`). |
| `ai_enabled` | bool | `true` if `ai_mode: local` is active for this site. |
| `is_semantic` | bool | `true` if a vector was successfully generated and sent with the query. |

**`parsed` object fields**

| Field | Type | Description |
|-------|------|-------------|
| `text` | string | The cleaned query text passed to Meilisearch for keyword matching. |
| `doctype` | string | Detected DocType, or empty string if not identified. |
| `filters` | array | List of structured filters extracted by the NLP rules. |
| `limit` | int | The effective result limit. |

**`filters[]` object**

| Field | Type | Description |
|-------|------|-------------|
| `field` | string | Meilisearch document field name. |
| `op` | string | Operator: `=`, `>`, `<`, `between`. |
| `value` | any | Scalar or `[start, end]` array for `between`. |

**When `q` is empty**

```json
{"hits": [], "took_ms": 0}
```

**Example queries and their NLP interpretation**

| Query | DocType detected | Filters injected |
|-------|-----------------|-----------------|
| `unpaid invoices above 10k` | Sales Invoice | `status = "Overdue"`, `grand_total > 10000` |
| `draft POs > 1cr` | Purchase Order | `status = "Draft"`, `grand_total > 10000000` |
| `customers in Mumbai` | Customer | keyword `Mumbai` passed to Meilisearch text search |
| `payments yesterday` | Sales Invoice | `posting_date between <yesterday_start> <yesterday_end>` |
| `overdue since 2026-01-01` | Sales Invoice | `status = "Overdue"`, `posting_date >= 2026-01-01` |
| `items below 500` | Item | `standard_rate < 500` |
| `10k to 50k invoices` | Sales Invoice | `grand_total between 10000 50000` |

---

### `GET /api/v1/suggest`

Returns search term suggestions based on the partial query. Currently provides simple suffix expansion; full log-based suggestions are planned for a future phase.

**Request**

```
GET /api/v1/suggest?q=inv
X-Frappe-Site-Name: erp.local
Cookie: sid=<session_id>
```

**Query Parameters**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Partial query string |

**Response `200 OK`**

```json
{
  "suggestions": [
    "inv invoices",
    "inv customers"
  ]
}
```

---

### `GET /api/v1/autocomplete`

Field-value autocomplete for use in filter UIs. Currently a stub; returns an empty list.

**Request**

```
GET /api/v1/autocomplete?q=Acm
X-Frappe-Site-Name: erp.local
Cookie: sid=<session_id>
```

**Response `200 OK`**

```json
{
  "options": []
}
```

---

### `POST /api/v1/analytics/click`

Records that a user opened a specific document from search results. This drives the usage-based ranking signal (`default_click_score`). The click score is incremented in Redis and asynchronously pushed to Meilisearch as a partial document update.

**Request**

```
POST /api/v1/analytics/click
Content-Type: application/json
X-Frappe-Site-Name: erp.local
Cookie: sid=<session_id>

{
  "doctype": "Sales Invoice",
  "name": "ACC-SINV-2025-00042",
  "query": "unpaid invoices above 10k"
}
```

**Request body fields**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `doctype` | string | Yes | The Frappe DocType of the clicked document |
| `name` | string | Yes | The `name` field (primary key) of the document |
| `query` | string | No | The search query that surfaced this document |

**Response `202 Accepted`**

No body. The click is processed asynchronously.

**Response `400 Bad Request`**

```json
{"error": "invalid format"}
```

---

### `GET /metrics`

Prometheus metrics endpoint. No authentication required. Exposes all standard Go runtime metrics plus Lightning-specific metrics.

**Request**

```
GET /metrics
```

**Response `200 OK`** — Prometheus text format:

```
# HELP lightning_search_duration_seconds Latency of search requests in seconds.
# TYPE lightning_search_duration_seconds histogram
lightning_search_duration_seconds_bucket{site="erp.local",le="0.005"} 142
lightning_search_duration_seconds_bucket{site="erp.local",le="0.01"} 198
...

# HELP lightning_binlog_lag_seconds Replication lag from MariaDB in seconds.
# TYPE lightning_binlog_lag_seconds gauge
lightning_binlog_lag_seconds{site="erp.local"} 0.12

# HELP lightning_indexing_total Total number of documents indexed successfully.
# TYPE lightning_indexing_total counter
lightning_indexing_total{action="insert",doctype="Sales Invoice",site="erp.local"} 5430
lightning_indexing_total{action="update",doctype="Sales Invoice",site="erp.local"} 871

# HELP lightning_indexing_errors_total Total number of document indexing failures.
# TYPE lightning_indexing_errors_total counter
lightning_indexing_errors_total{doctype="Sales Invoice",site="erp.local"} 0
```

---

## JavaScript Fetch Examples

### Search from the browser (session auth)

```javascript
const resp = await fetch(
  `http://localhost:8765/api/v1/search?q=${encodeURIComponent("unpaid invoices above 10k")}`,
  {
    credentials: "include",          // sends the sid cookie
    headers: {
      "X-Frappe-Site-Name": "erp.local",
    },
  }
);
const data = await resp.json();
console.log(data.hits);
```

### Search from an external app (Bearer token)

```javascript
const resp = await fetch(
  `http://lightning.internal:8765/api/v1/search?q=draft+POs`,
  {
    headers: {
      "Authorization": "Bearer your-api-token",
      "X-Frappe-Site-Name": "erp.local",
    },
  }
);
```

### Log a click event

```javascript
await fetch("http://localhost:8765/api/v1/analytics/click", {
  method: "POST",
  credentials: "include",
  headers: {
    "Content-Type": "application/json",
    "X-Frappe-Site-Name": "erp.local",
  },
  body: JSON.stringify({
    doctype: "Sales Invoice",
    name: "ACC-SINV-2025-00042",
    query: "unpaid invoices above 10k",
  }),
});
```

---

## curl Examples

```bash
# Health check
curl http://localhost:8765/api/v1/health \
  -H "X-Frappe-Site-Name: erp.local"

# Search with Bearer token
curl "http://localhost:8765/api/v1/search?q=overdue+invoices+last+month" \
  -H "Authorization: Bearer lightning-secret-dev" \
  -H "X-Frappe-Site-Name: erp.local"

# Search with session cookie
curl "http://localhost:8765/api/v1/search?q=customers+in+Mumbai" \
  -H "X-Frappe-Site-Name: erp.local" \
  --cookie "sid=abc123def456"

# Log a click
curl -X POST http://localhost:8765/api/v1/analytics/click \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer lightning-secret-dev" \
  -H "X-Frappe-Site-Name: erp.local" \
  -d '{"doctype":"Customer","name":"CUST-0001","query":"customers Mumbai"}'

# Prometheus metrics
curl http://localhost:8765/metrics
```

---

## Rate Limiting

The API server enforces a global rate limit of **150 requests per 10 seconds per IP address**. This applies to all endpoints. When the limit is exceeded, the server responds with:

```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json

{"error": "too many requests"}
```

For high-throughput integrations, deploy Lightning behind a reverse proxy and perform rate limiting at the Nginx/Caddy layer instead.
