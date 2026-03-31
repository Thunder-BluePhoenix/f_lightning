# Frappe Lightning: Configuration Reference

All Lightning configuration lives in a single YAML file (`config.yaml`). Copy the provided example to get started:

```bash
cp frappe_lightning/config.yaml.example frappe_lightning/config.yaml
```

Start Lightning pointing at your config:

```bash
go run main.go --config config.yaml
# or with the compiled binary:
./lightning-server --config /etc/frappe-lightning/config.yaml
```

---

## Full Example

```yaml
sites:
  - name: erp.local
    mariadb:
      host: 127.0.0.1
      port: 3306
      user: lightning
      password: strong_password
      server_id: 100
    meilisearch:
      host: http://localhost:7700
      master_key: your-master-key-here
    redis:
      host: localhost
      port: 11000
    api:
      port: 8765
      allowed_origins: "http://localhost:8000"
    doctypes:
      - name: Sales Invoice
        table: "tabSales Invoice"
      - name: Customer
        table: tabCustomer
      - name: Item
        table: tabItem
      - name: Purchase Order
        table: "tabPurchase Order"
      - name: Supplier
        table: tabSupplier
      - name: Lead
        table: tabLead

ai_mode: off
embedding_server_url: "http://localhost:5000/embed"
vector_dimensions: 384
api_token: "lightning-secret-dev"
```

---

## Top-Level Fields

### `ai_mode`

Controls whether vector embeddings are generated for hybrid search.

| Value | Behaviour |
|-------|-----------|
| `off` | Rule-based NLP only. No external embedding calls. Default. |
| `local` | Rule-based NLP + embeddings via `embedding_server_url`. Vectors stored in Meilisearch. |
| `cloud` | Reserved for future cloud LLM integration (OpenAI / Gemini / Ollama). |

### `embedding_server_url`

URL of the HTTP embedding server used when `ai_mode: local`.

- Default: `http://localhost:5000/embed`
- The server must accept `POST /embed` with body `{"text": "..."}` and return `{"vector": [0.1, 0.2, ...]}`.
- See [developer_guide.md](developer_guide.md) for running the reference Python server.

### `vector_dimensions`

Dimensionality of the embedding vectors. Must match the model used by the embedding server.

- Default: `384` (matches `all-MiniLM-L6-v2`)
- This value configures the Meilisearch `userProvided` embedder on index init. Changing it requires re-creating the indexes.

### `api_token`

A shared secret for Bearer token authentication. Used by external applications and mobile clients that cannot supply a Frappe `sid` cookie.

- Default: `lightning-secret-dev`
- **Change this before deploying to production.**
- Sent by clients as `Authorization: Bearer <token>`.

---

## `sites[]` — Per-Site Configuration

Each entry in the `sites` list starts an independent binlog listener and sync engine goroutine.

### `sites[].name`

The Frappe site hostname. Must match the site directory name under `frappe-bench/sites/`.

This value is used to:
- Namespace Meilisearch index names: `{slugified_name}_{doctype}`.
- Read sessions from Redis using the key prefix `{name}|sessiondata|{sid}`.
- Route incoming HTTP requests via the `X-Frappe-Site-Name` header.

### `sites[].mariadb`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | — | MariaDB hostname |
| `port` | int | `3306` | MariaDB port |
| `user` | string | — | MySQL user with replication privileges |
| `password` | string | — | MySQL user password |
| `server_id` | uint32 | — | Unique replication server ID (must not collide with other slaves) |

**Required MariaDB user grants:**
```sql
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'lightning'@'%';
GRANT SELECT ON `your_site_db`.* TO 'lightning'@'%';
```

**Required MariaDB server config** (`/etc/mysql/my.cnf` or `mariadb.conf.d/`):
```ini
[mysqld]
binlog_format     = ROW
binlog_row_image  = FULL
server_id         = 1
log-bin           = mysql-bin
```

### `sites[].meilisearch`

| Field | Type | Description |
|-------|------|-------------|
| `host` | string | Meilisearch HTTP URL, e.g. `http://localhost:7700` |
| `master_key` | string | Meilisearch master API key |

Each site can point to a **different** Meilisearch instance if needed. For typical single-server setups, all sites share one Meilisearch instance — isolation is by index name prefix.

### `sites[].redis`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | — | Redis hostname |
| `port` | int | — | Redis port |

This must point to Frappe's **Redis cache** instance (not the queue or socketio instance). In a standard `bench` setup this is usually port `11000`.

Redis serves two purposes:
1. **Session validation** — read Frappe `sid` sessions to authenticate search requests.
2. **Click score storage** — increment counters for clicked documents to feed the ranking engine.

### `sites[].api`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `8765` | HTTP port the Lightning API server listens on |
| `allowed_origins` | string | — | CORS origin whitelist (informational; enforce via Nginx in production) |

> All sites share one HTTP server. The `port` from the first site entry (or the first `port > 0` encountered) is used as the listen port.

### `sites[].doctypes[]`

The list of Frappe DocTypes to track via the binlog.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Frappe DocType name (e.g. `"Sales Invoice"`) |
| `table` | string | Corresponding MariaDB table name (e.g. `"tabSales Invoice"`) |

The `name` field is used as the canal table filter regex. Only tables matching an entry here will generate events.

**Built-in DocType schemas** (defined in `config/schema.go`, auto-applied):

| DocType | Table | Index suffix |
|---------|-------|--------------|
| Sales Invoice | tabSales Invoice | `sales_invoice` |
| Customer | tabCustomer | `customer` |
| Item | tabItem | `item` |
| Purchase Order | tabPurchase Order | `purchase_order` |
| Supplier | tabSupplier | `supplier` |
| Lead | tabLead | `lead` |

Adding a DocType to `doctypes[]` that does **not** have a built-in schema will cause the canal to capture its events, but the sync engine will silently skip them (no schema = no field mapping). See [developer_guide.md](developer_guide.md) for registering custom schemas.

---

## `config/ranking.yaml` — Ranking & Synonyms

This file customises Meilisearch ranking rules and synonyms per DocType. It is loaded at startup; restart Lightning after changes.

```yaml
doctypes:
  "Sales Invoice":
    searchable_attributes:
      - name
      - customer_name
      - grand_total
      - default_click_score
    synonyms:
      "bill": ["invoice"]
      "unpaid": ["overdue", "due"]

  "Customer":
    searchable_attributes:
      - customer_name
      - name
      - customer_group
      - default_click_score
    synonyms:
      "client": ["customer"]
      "borrower": ["customer"]

  "Item":
    searchable_attributes:
      - item_name
      - item_code
      - default_click_score
    synonyms:
      "product": ["item"]
      "goods": ["item"]

global:
  ranking_rules:
    - "words"
    - "typo"
    - "proximity"
    - "attribute"
    - "sort"
    - "exactness"
    - "custom_click_score:desc"
```

### `doctypes.<DocType>.searchable_attributes`

Overrides the default searchable field list for this DocType. The order matters — Meilisearch ranks matches in earlier fields higher. Put the most important field first.

### `doctypes.<DocType>.synonyms`

Bidirectional synonym map. A query for `"bill"` will also match documents containing `"invoice"` and vice versa. Keys and values are lowercased by Meilisearch.

### `global.ranking_rules`

The Meilisearch ranking rule chain applied to all indexes. The default chain is Meilisearch's built-in order plus `custom_click_score:desc` appended at the end.

`custom_click_score` is the `default_click_score` field stored in each document (a Redis-backed click count). By appending `:desc`, frequently clicked documents are boosted within the same relevance tier.

---

## Environment Variable Overrides

Lightning does not currently read environment variables directly. Use a startup wrapper script or a systemd `EnvironmentFile` to substitute secrets into `config.yaml` before launching:

```bash
envsubst < config.yaml.template > config.yaml
./lightning-server --config config.yaml
```

---

## Multi-Site Example

```yaml
sites:
  - name: company-a.local
    mariadb:
      host: 127.0.0.1
      port: 3306
      user: lightning
      password: secret_a
      server_id: 101
    meilisearch:
      host: http://localhost:7700
      master_key: meili-key
    redis:
      host: localhost
      port: 11000
    api:
      port: 8765
    doctypes:
      - name: Sales Invoice
        table: "tabSales Invoice"

  - name: company-b.local
    mariadb:
      host: 127.0.0.1
      port: 3306
      user: lightning
      password: secret_b
      server_id: 102          # different server_id from company-a
    meilisearch:
      host: http://localhost:7700
      master_key: meili-key
    redis:
      host: localhost
      port: 11001             # different Redis instance
    api:
      port: 8765
    doctypes:
      - name: Sales Invoice
        table: "tabSales Invoice"
      - name: Customer
        table: tabCustomer

ai_mode: off
api_token: "change-me-in-production"
```

Each site gets its own set of indexes: `company_a_local_sales_invoice` and `company_b_local_sales_invoice`. Users on each site can only ever query their own site's indexes.
