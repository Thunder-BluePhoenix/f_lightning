# Frappe Lightning: User Guide

Frappe Lightning is a real-time search engine for Frappe. It connects to MariaDB's binary log and streams every record change into Meilisearch, delivering sub-10ms search responses regardless of database size.

This guide walks through setup, daily use, and the tools available to admins.

---

## 1. How It Works

Lightning runs as a Go sidecar process alongside your Frappe bench. It has two jobs:

- **Sync** — listen to the MariaDB binary log and keep Meilisearch indexes up to date within milliseconds of any save.
- **Search** — accept HTTP requests from the Frappe Desk, parse natural language queries, enforce user permissions, and return results from Meilisearch.

Neither job touches Frappe's Python layer. There is no polling, no hooks patched into Frappe, and no Python overhead on the search path.

---

## 2. Prerequisites

| Component | Requirement |
|-----------|-------------|
| MariaDB | 10.6+, `binlog_format=ROW`, `binlog_row_image=FULL` |
| Meilisearch | 1.6+, running and reachable |
| Redis | Frappe's existing Redis cache instance |
| Go | 1.21+ (only needed to build the binary) |
| Frappe | v15 or v16 |

---

## 3. Setup

Choose the path that fits your situation:

- **[Quick Setup (Automated)](#3a-quick-setup-automated)** — three commands from the `f_lightning` app directory. Reads credentials directly from your bench. Recommended for most users.
- **[Manual Setup](#3b-manual-setup)** — step-by-step instructions. Use this if you need to customise any part of the process.

---

### 3a. Quick Setup (Automated)

The `scripts/` directory contains shell scripts that read your bench's `site_config.json` and `common_site_config.json` to auto-fill credentials, configure MariaDB, start Meilisearch, build the Go binaries, and run the initial backfill.

**Run everything in one command:**

```bash
cd /path/to/frappe-bench/apps/f_lightning

./scripts/install.sh \
  --bench /path/to/frappe-bench \
  --site  erp.local
```

The installer will prompt for the MariaDB root password once and handle the rest. When it finishes you will have:
- MariaDB configured for ROW-format binlog replication.
- A dedicated `lightning` database user with replication grants.
- Meilisearch running as a Docker container (or prompted to start it manually if Docker is not available).
- A `frappe_lightning/config.yaml` generated from your bench credentials.
- `lightning-server` and `lightning` binaries built in the app root.
- Meilisearch indexes populated with your existing data via backfill.

**Installer options:**

| Flag | Description |
|------|-------------|
| `--bench PATH` | Path to the frappe-bench directory (required) |
| `--site NAME` | Frappe site name (required) |
| `--lightning-pass PASS` | MariaDB Lightning user password (auto-generated if omitted) |
| `--meili-key KEY` | Meilisearch master key (auto-generated if omitted) |
| `--api-token TOKEN` | Lightning API Bearer token (auto-generated if omitted) |
| `--meili-host URL` | Meilisearch URL (default: `http://localhost:7700`) |
| `--server-id ID` | MariaDB replication server ID (default: `100`) |
| `--ai-mode MODE` | `off` or `local` (default: `off`) |
| `--skip-mariadb` | Skip MariaDB setup — already configured |
| `--skip-meili` | Skip starting Meilisearch |
| `--skip-build` | Skip building Go binaries |
| `--skip-backfill` | Skip initial backfill |

**Re-running individual steps:**

```bash
# Just re-generate config.yaml from bench credentials
./scripts/generate-config.sh --bench /path/to/bench --site erp.local

# Just re-run the MariaDB user/binlog setup
./scripts/setup-mariadb.sh

# Just re-run the backfill for all DocTypes
./scripts/backfill-all.sh --config frappe_lightning/config.yaml --site erp.local
```

**After the installer completes:**

```bash
# Start Lightning
./lightning-server --config frappe_lightning/config.yaml

# Check everything is healthy
./lightning status --config frappe_lightning/config.yaml
./lightning diff erp.local "Sales Invoice" --config frappe_lightning/config.yaml
```

Then install the Frappe app:

```bash
cd /path/to/frappe-bench
bench --site erp.local install-app f_lightning
bench --site erp.local migrate
bench restart
```

For running as a background service, see the [deployment guide](deployment.md).

---

### 3b. Manual Setup

### Step 1 — Enable MariaDB Binary Log

Add to your MariaDB config (`/etc/mysql/mariadb.conf.d/50-server.cnf`):

```ini
[mysqld]
binlog_format    = ROW
binlog_row_image = FULL
server_id        = 1
log-bin          = mysql-bin
```

Restart MariaDB, then create a dedicated user:

```sql
CREATE USER 'lightning'@'%' IDENTIFIED BY 'strong_password';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'lightning'@'%';
GRANT SELECT ON `your_site_db`.* TO 'lightning'@'%';
FLUSH PRIVILEGES;
```

### Step 2 — Start Meilisearch

```bash
docker run -d --name meilisearch --restart unless-stopped \
  -p 7700:7700 \
  -v /var/lib/meilisearch:/meili_data \
  getmeili/meilisearch:latest \
  meilisearch --master-key="your-master-key" --env="production"
```

### Step 3 — Configure Lightning

```bash
cd frappe_lightning
cp config.yaml.example config.yaml
```

Edit `config.yaml`. The minimum changes:

```yaml
sites:
  - name: erp.local               # your Frappe site hostname
    mariadb:
      host: 127.0.0.1
      port: 3306
      user: lightning
      password: strong_password
      server_id: 100
    meilisearch:
      host: http://localhost:7700
      master_key: your-master-key
    redis:
      host: localhost
      port: 11000                 # Frappe's Redis cache port
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
api_token: "change-this-before-going-to-production"
```

### Step 4 — Build and Start Lightning

```bash
# From the f_lightning app root:
cd frappe_lightning

# Build the server binary
go build -o ../lightning-server ./main.go

# Build the CLI tool
go build -o ../lightning ./cmd/lightning/main.go

cd ..

# Start the server
./lightning-server --config frappe_lightning/config.yaml
```

You should see:

```
⚡ Frappe Lightning starting  config=config.yaml
index ready  site=erp.local  index=erp_local_sales_invoice
...
site pipeline started  site=erp.local
⚡ Lightning is running — multi-tenant mode active
starting global Multi-Tenant API server  addr=:8765
```

### Step 5 — Run the Initial Backfill

Lightning's binlog listener only captures changes going forward. To index existing records, run the backfill for all DocTypes at once:

```bash
./scripts/backfill-all.sh \
  --config frappe_lightning/config.yaml \
  --site erp.local
```

Or individually per DocType:

```bash
./lightning backfill --config frappe_lightning/config.yaml --site erp.local --doctype "Sales Invoice"
./lightning backfill --config frappe_lightning/config.yaml --site erp.local --doctype Customer
./lightning backfill --config frappe_lightning/config.yaml --site erp.local --doctype Item
./lightning backfill --config frappe_lightning/config.yaml --site erp.local --doctype "Purchase Order"
./lightning backfill --config frappe_lightning/config.yaml --site erp.local --doctype Supplier
./lightning backfill --config frappe_lightning/config.yaml --site erp.local --doctype Lead
```

The backfill streams data in batches of 5,000 rows, with a 100ms pause between batches to keep MariaDB load low. It is safe to run while the main server is running.

### Step 6 — Install the Frappe App

```bash
bench get-app f_lightning /path/to/f_lightning
bench --site erp.local install-app f_lightning
bench --site erp.local migrate
bench restart
```

---

## 4. The Search Bar

### Opening the Command Palette

Press `Cmd+K` (macOS) or `Ctrl+K` (Windows / Linux) anywhere in the Frappe Desk to open the Lightning search bar.

### Typing a Query

Results appear as you type with an 80ms debounce. Results are grouped by DocType. Use `↑` `↓` to navigate, `Enter` to open a record, `Esc` to close.

### Natural Language Queries

Lightning understands plain English. You do not need to know field names.

| What you type | What it does |
|---------------|--------------|
| `unpaid invoices above 10k` | Status = Overdue, grand\_total > 10,000 |
| `draft POs this month` | Status = Draft, transaction\_date in current month |
| `customers` | Full-text search across customer names |
| `paid invoices yesterday` | Status = Paid, posting\_date = yesterday |
| `items below 500` | standard\_rate < 500 |
| `overdue since 2026-01-01` | Status = Overdue, posting\_date ≥ 2026-01-01 |
| `10k to 50k invoices` | grand\_total between 10,000 and 50,000 |
| `invoices above 5 lakhs` | grand\_total > 500,000 |
| `draft POs > 1cr` | Status = Draft, grand\_total > 10,000,000 |

Amount suffixes supported: `k` (thousands), `lakh`/`lakhs` (100,000), `m`/`million` (1,000,000), `cr`/`crore` (10,000,000).

Date shorthand supported: `today`, `yesterday`, `this week`, `last week`, `this month`, `last month`, `this year`, `last year`.

### Advanced DSL

For precise queries, use the explicit `field:value` syntax:

```
status:"Overdue" grand_total:>50000
customer:"Acme Corp" status:"Overdue" last month
item_group:"Electronics" disabled:0
```

DSL tokens are processed before NLP rules, so you can mix both styles freely.

### Inline Preview

Hover over a result or navigate to it with the arrow keys to see a quick preview of the document's key fields without leaving the search bar.

### Saved Searches

Frequently-used queries can be pinned and accessed in one click from the search bar.

---

## 5. How Permissions Work

Lightning enforces Frappe permissions at query time. When you search:

1. Your `sid` cookie is validated against Frappe's Redis session store (no database call).
2. Your roles are read from the session.
3. A permission filter is injected into the Meilisearch query based on your roles — you will never see records you don't have access to, even if they are in the index.

This happens on every request, within the same sub-10ms budget.

---

## 6. Authentication for External Apps

External tools (mobile apps, Slack bots, custom dashboards) can access the Lightning API using a Bearer token instead of a session cookie:

```
Authorization: Bearer your-api-token
X-Frappe-Site-Name: erp.local
```

The token is set in `config.yaml` under `api_token`. Bearer token requests are treated as a privileged system user and bypass per-user permission filters — use with care.

---

## 7. Admin CLI Reference

The `lightning` CLI binary provides diagnostic and management commands. All commands accept `--config` to point at a non-default config file.

### `lightning status`

Shows the configured sites and their infrastructure details.

```bash
./lightning status --config config.yaml
```

```
⚡ Lightning Pipeline Status
----------------------------------------
Site: erp.local              [ACTIVE]
  MariaDB: 127.0.0.1:3306
  Meili:   http://localhost:7700
  Redis:   localhost:11000
```

### `lightning parse`

Tests how the NLP engine interprets a query. Use this to debug unexpected search behaviour before reporting it.

```bash
./lightning parse "unpaid invoices above 10k" --config config.yaml
```

```
🔮 NLP Parse: "unpaid invoices above 10k"
----------------------------------------
Structured Query:
{
  "text": "invoices above 10k",
  "doctype": "Sales Invoice",
  "filters": [
    {"field": "status",      "op": "=", "value": "Overdue"},
    {"field": "grand_total", "op": ">", "value": 10000}
  ],
  "limit": 20
}

Meilisearch DSL Params:
{
  "filter": ["status = \"Overdue\"", "grand_total > 10000"],
  "limit": 20
}
```

### `lightning watch`

Streams live binlog events for a site to the terminal. Useful for confirming that record saves are being captured.

```bash
./lightning watch erp.local --config config.yaml
```

```
👀 Watching events for erp.local... (Press Ctrl+C to stop)
[insert]   tabSales Invoice    event  [ACC-SINV-2025-00099, Acme Corp, ...]
[update]   tabCustomer         event  [CUST-0042, ...]
```

### `lightning diff`

Compares the row count in MariaDB against the document count in Meilisearch for a given DocType. A non-zero diff means the index is out of sync.

```bash
./lightning diff erp.local "Sales Invoice" --config config.yaml
```

```
📊 Sync Diff for erp.local [Sales Invoice]
----------------------------------------
  MariaDB Rows:     12,450
  Meilisearch Docs: 12,450
  ✅ Perfect Match!
```

If there is a diff, run the backfill to repair it:

```bash
./lightning backfill --config config.yaml --site erp.local --doctype "Sales Invoice"
```

---

## 8. Customisation

### Ranking and Synonyms

Edit `config/ranking.yaml` to adjust which fields Meilisearch searches first and to define synonym mappings.

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
```

Restart Lightning after saving this file.

### Usage-Based Ranking

Lightning tracks which documents users open after searching (`default_click_score`). Documents that are clicked more often naturally rise in results within their relevance tier. This happens automatically — no configuration needed.

### AI Hybrid Search

To enable semantic search alongside keyword search, switch to local AI mode:

```yaml
ai_mode: local
embedding_server_url: "http://localhost:5000/embed"
vector_dimensions: 384
```

Then start the embedding server (see the [developer guide](developer_guide.md)). Once enabled, queries like `"footwear"` will also match documents containing `"shoes"` or `"sneakers"`.

### Adding a New DocType

1. Add the DocType and its table name to `config.yaml` under `doctypes`.
2. Add a schema entry in `frappe_lightning/config/schema.go` (defining which fields to index, search, filter, and sort).
3. Restart Lightning.
4. Run the backfill for the new DocType.

See the [developer guide](developer_guide.md) for full instructions.

---

## 9. Monitoring

Lightning exposes Prometheus metrics at `GET /metrics` (no authentication required):

| Metric | What it measures |
|--------|-----------------|
| `lightning_search_duration_seconds` | Search request latency per site |
| `lightning_binlog_lag_seconds` | How far behind the binlog listener is (should be < 1s) |
| `lightning_indexing_total` | Documents indexed, broken down by site/doctype/action |
| `lightning_indexing_errors_total` | Failed indexing attempts |

A `lightning_binlog_lag_seconds` value consistently above 5–10 seconds means the sync pipeline is falling behind — usually caused by a slow Meilisearch instance or a large bulk import in MariaDB.

---

## 10. Troubleshooting

### The search bar doesn't appear

Ensure the Frappe app is installed and the bench has been restarted:
```bash
bench --site erp.local list-apps    # f_lightning should appear
bench restart
```

### Search returns no results

1. Check that the backfill has been run for the relevant DocType.
2. Run `lightning diff erp.local "Sales Invoice"` to compare counts.
3. Run `lightning watch erp.local` and save a record in Frappe — confirm the event appears in the terminal.

### NLP doesn't parse my query correctly

Run `lightning parse "your query"` to see exactly what filters were produced. Adjust your wording, or use the Advanced DSL (`field:value` syntax) for full control.

### 401 errors on all search requests

Check that:
- The `sid` cookie is present and not expired (log out and log back in to Frappe).
- The `X-Frappe-Site-Name` header matches a `name` entry in `config.yaml`.
- Redis is reachable at the configured `host:port`.

For external apps using Bearer tokens, verify that `api_token` in `config.yaml` matches the token being sent.

### High binlog lag

A large bulk import (e.g., `bench migrate` or a CSV upload) will temporarily spike the lag as the batcher works through the backlog. This is normal and the lag will return to near-zero once the burst is processed.

If the lag stays high, check Meilisearch's health and available disk space.
