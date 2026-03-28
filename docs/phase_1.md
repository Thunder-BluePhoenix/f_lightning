# Phase 1: Infrastructure & Binlog Connection

**Goal:** Establish the Go service, configure MariaDB to emit the correct binlog format, and successfully connect the Go listener to the live event stream. This is the zero-to-one phase — getting a single binlog event to print on stdout is the win condition.

---

## Prerequisites

### MariaDB Configuration (`my.cnf`)

```ini
[mysqld]
log_bin           = /var/log/mysql/mysql-bin.log
binlog_format     = ROW
binlog_row_image  = FULL
server-id         = 1
expire_logs_days  = 7
```

After editing, restart MariaDB:

```bash
sudo systemctl restart mariadb
# Verify:
mysql -e "SHOW VARIABLES LIKE 'binlog_format';"
# Expected: ROW
```

### Dedicated Replication User

```sql
CREATE USER 'lightning'@'localhost' IDENTIFIED BY 'strong_password';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'lightning'@'localhost';
FLUSH PRIVILEGES;

-- Verify:
SHOW GRANTS FOR 'lightning'@'localhost';
```

> ⚠️ The `lightning` user only needs replication privileges — it never writes to the database.

---

## Meilisearch Setup

```bash
# Docker (recommended for dev)
docker run -d \
  --name meilisearch \
  -p 7700:7700 \
  -v $(pwd)/meilidata:/meili_data \
  -e MEILI_MASTER_KEY='your-master-key' \
  getmeili/meilisearch:latest

# Verify:
curl http://localhost:7700/health
# Expected: {"status":"available"}
```

> For production, use the official Meilisearch systemd service or a managed Meilisearch Cloud instance.

---

## Go Project Structure

```
frappe_lightning/
├── go.mod
├── go.sum
├── main.go                  ← Entrypoint: starts canal + API server
├── config/
│   ├── config.go            ← YAML config loader (DB, Meili, sites)
│   └── schema.go            ← DocType schema registry
├── canal/
│   └── listener.go          ← Binlog event handler (OnRow)
├── nlp/                     ← NLP engine (Phase 5)
│   ├── models.go
│   ├── tokenizer.go
│   ├── intent.go
│   ├── rules.go
│   ├── builder.go
│   ├── meili.go
│   └── builder_test.go
├── search/                  ← Meilisearch sync client (Phase 2)
│   ├── meilisearch.go
│   └── batcher.go
├── ranking/                 ← Smart ranking engine (Phase 6)
├── analytics/               ← Search event logging (Phase 4)
└── api/                     ← HTTP server (Phase 3)
    ├── server.go
    ├── handlers/
    └── middleware/
```

### Go dependencies (`go.mod`)

```go
module frappe_lightning

go 1.21

require (
    github.com/go-mysql-org/go-mysql v1.9.1    // Canal / binlog listener
    github.com/meilisearch/meilisearch-go v0.26.0 // Meilisearch client
    github.com/gofiber/fiber/v2 v2.52.0         // HTTP server
    github.com/redis/go-redis/v9 v9.5.1         // Redis session reads
    gopkg.in/yaml.v3 v3.0.1                     // Config parsing
    go.uber.org/zap v1.27.0                     // Structured logging
)
```

---

## Configuration YAML

```yaml
# config.yaml
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
      master_key: your-master-key
    redis:
      host: localhost
      port: 11000      # Frappe's Redis cache port

ai_mode: off           # off | local | cloud

doctypes:
  - table: tabSales Invoice
    name: Sales Invoice
  - table: tabCustomer
    name: Customer
  - table: tabItem
    name: Item
  - table: tabPurchase Order
    name: Purchase Order
  - table: tabSupplier
    name: Supplier
```

---

## Binlog Listener Implementation

```go
// canal/listener.go

package canal

import (
    "github.com/go-mysql-org/go-mysql/canal"
    "go.uber.org/zap"
)

type Handler struct {
    log      *zap.Logger
    eventsCh chan<- *RowEvent  // Sends events to the sync engine
}

// RowEvent carries the validated event downstream
type RowEvent struct {
    Table  string
    Action string // insert | update | delete
    Rows   []map[string]interface{}
}

// OnRow is called by go-mysql for every binlog row event
func (h *Handler) OnRow(e *canal.RowsEvent) error {
    tableName := e.Table.Name
    if !isTrackedTable(tableName) {
        return nil
    }

    h.log.Info("binlog event",
        zap.String("table", tableName),
        zap.String("action", e.Action),
        zap.Int("rows", len(e.Rows)),
    )

    rows := mapRows(e)
    h.eventsCh <- &RowEvent{
        Table:  tableName,
        Action: e.Action,
        Rows:   rows,
    }
    return nil
}

// Required stub implementations for the canal.EventHandler interface
func (h *Handler) OnRotate(*canal.RotateEvent) error    { return nil }
func (h *Handler) OnTableChanged(schema, table string) error { return nil }
func (h *Handler) OnDDL(string, *replication.QueryEvent) error { return nil }
func (h *Handler) OnXID(uint64) error                   { return nil }
func (h *Handler) OnGTID(gtid.GTIDSet) error            { return nil }
func (h *Handler) OnPosSynced(pos.Position, bool) error  { return nil }
func (h *Handler) String() string                        { return "LightningHandler" }
```

---

## Entrypoint

```go
// main.go
package main

import (
    "frappe_lightning/canal"
    "frappe_lightning/config"
    "frappe_lightning/search"
    "go.uber.org/zap"
)

func main() {
    log, _ := zap.NewProduction()
    defer log.Sync()

    cfg, err := config.Load("config.yaml")
    if err != nil {
        log.Fatal("failed to load config", zap.Error(err))
    }

    eventsCh := make(chan *canal.RowEvent, 1024)

    // Start binlog listener
    go canal.Start(cfg, eventsCh, log)

    // Start sync engine (writes to Meilisearch)
    go search.Start(cfg, eventsCh, log)

    // Block forever
    select {}
}
```

---

## Tracked DocTypes (Initial Set)

| MariaDB Table | Frappe DocType |
|---|---|
| `tabSales Invoice` | Sales Invoice |
| `tabCustomer` | Customer |
| `tabItem` | Item |
| `tabPurchase Order` | Purchase Order |
| `tabSupplier` | Supplier |
| `tabLead` | Lead |
| `tabOpportunity` | Opportunity |

This list is driven entirely by the YAML config — no code changes needed to add new DocTypes.

---

## Validation Checklist

- [ ] `SHOW VARIABLES LIKE 'binlog_format';` returns `ROW`
- [ ] `SHOW VARIABLES LIKE 'binlog_row_image';` returns `FULL`
- [ ] `lightning` MySQL user can connect and `SHOW MASTER STATUS` without error
- [ ] `docker ps` shows Meilisearch running and `curl :7700/health` returns available
- [ ] Go service compiles without errors (`go build ./...`)
- [ ] Go service starts without errors (`go run main.go`)
- [ ] Saving a record in Frappe produces a structured log line in stdout within <1s
- [ ] Deleting a record in Frappe produces a `delete` action log line
