# ⚡ Frappe Lightning

> **A real-time Go-powered search layer that syncs MariaDB changes to Meilisearch, delivering instant, sub-10ms "search-as-you-type" across millions of Frappe records.**

---

## The Problem

Frappe's native **Global Search** polls the database dynamically and processes results through multiple Python layers — each adding latency before data reaches the user. On sites with millions of records, it becomes visibly sluggish. Users feel it. Productivity drops.

## The Solution

Frappe Lightning is an **infra-level upgrade disguised as a feature**.

A dedicated Go service listens directly to the MariaDB **Binary Log**. Every time a record is saved in Frappe, Go instantly captures the change and indexes it into Meilisearch. Zero polling. Zero Python overhead.

The result: a **Cmd+K Command Palette** injected into Frappe Desk, giving users true "search-as-you-type" across millions of records with **sub-10ms latency**.

---

## Features

| | Feature | Description |
|---|---|---|
| ⚡ | **Real-Time Binlog Sync** | Go listens to MariaDB binlog → instant Meilisearch upsert on every save |
| 🧠 | **NLP Rule Engine** | Natural language → structured query (`"unpaid invoices last month above 10k"`) |
| 🔐 | **Permission-Aware Search** | RBAC filters injected at query time; respects Frappe roles |
| 🖥️ | **Cmd+K Command Palette** | VS Code-style omnibox with keyboard navigation and inline preview |
| 📦 | **Multi-Site Support** | Isolated Meilisearch indexes per Frappe site, one binary for all |
| 🧩 | **Pluggable Indexing** | `BeforeIndex` / `TransformDoc` hooks per DocType |
| 🤖 | **Hybrid AI Search** | Optional semantic search via local embeddings (`ai_mode: local`) |
| 🔄 | **Full Backfill** | One-command initial sync for existing data |
| 🧪 | **Dev CLI** | Live binlog viewer, index diff checker, NLP parser tester |

---

## Quick Start

### 1. Install the Frappe app

```bash
cd /path/to/frappe-bench
bench get-app f_lightning <repo-url>
bench --site erp.local install-app f_lightning
bench --site erp.local migrate
```

### 2. Run the automated setup

The setup script reads credentials directly from your bench — no manual config editing needed.

```bash
cd apps/f_lightning

./scripts/install.sh \
  --bench /path/to/frappe-bench \
  --site  erp.local
```

This configures MariaDB binlog replication, starts Meilisearch, generates `config.yaml`, builds the Go binaries, and runs the initial backfill automatically.

### 3. Start Lightning

```bash
./lightning-server --config frappe_lightning/config.yaml
```

Press `Cmd+K` in Frappe Desk and start searching.

---

## Example Queries

Lightning understands plain English — no field names required.

```
unpaid invoices above 10k
draft POs this month
customers in Mumbai
paid invoices yesterday
items below 500
10k to 50k invoices
overdue since 2026-01-01
invoices above 5 lakhs
```

Power users can use explicit filters:

```
customer:"Acme Corp" status:"Overdue" last month
grand_total:>50000 status:"Draft"
```

---

## Documentation

| Doc | Description |
|-----|-------------|
| [User Guide](docs/userguide.md) | Setup, search bar usage, CLI reference, customisation |
| [Use Cases](docs/usecases.md) | Query examples for every built-in DocType |
| [API Reference](docs/api_reference.md) | All endpoints, auth methods, request/response format |
| [Configuration](docs/configuration.md) | Every field in `config.yaml` and `ranking.yaml` |
| [Architecture](docs/architecture.md) | How the components fit together, data flow, design decisions |
| [Deployment](docs/deployment.md) | Production setup: systemd, Nginx, Prometheus |
| [Developer Guide](docs/developer_guide.md) | Custom DocTypes, hooks, extending the NLP engine |

---

## Contributing

Contributions are welcome — bug fixes, new NLP rules, additional DocType schemas, documentation improvements, and more.

**Getting started:**

```bash
git clone <repo-url>
cd f_lightning

# Install pre-commit hooks (formatting + linting)
pre-commit install

# Run Go tests
cd frappe_lightning
go test ./...

# Test a query against the NLP engine without a running server
./lightning parse "unpaid invoices above 10k"
```

**Ways to contribute:**

- **Bug report** — open an issue with the query or config that caused the problem, and the output of `lightning parse` if it's an NLP issue.
- **New NLP rule** — add a rule function to `nlp/rules.go`, register it in `nlp/builder.go`, and add table-driven test cases to `nlp/builder_test.go`.
- **New DocType schema** — add an entry to `config/schema.go` with the fields, searchable, filterable, and sortable lists.
- **Documentation fix** — all docs are in `docs/` as plain Markdown.

Please open an issue before starting a large change so we can discuss the approach first.

---

## Reporting Issues

Found a bug or unexpected behaviour? Please include:

- Lightning version / commit hash (`git rev-parse --short HEAD`)
- Frappe and MariaDB versions
- The query that caused the problem
- Output of `./lightning parse "your query"` (if it's a search issue)
- Relevant log lines from `journalctl -u frappe-lightning` or the terminal

Open an issue at: **[GitHub Issues](<repo-url>/issues)**

---

## License

GPL-3.0

---

<div align="center">

Made with ❤️ for the Frappe community By BluePhoenix

</div>
