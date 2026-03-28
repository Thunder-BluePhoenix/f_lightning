# ⚡ Frappe Lightning

> **A real-time Go-powered search layer that syncs MariaDB changes to Meilisearch, delivering instant, sub-10ms "search-as-you-type" across millions of Frappe records.**

---

## The Problem

Frappe's native **Global Search** polls the database dynamically and processes results through multiple Python layers — each adding latency before data reaches the user. On sites with millions of records, it becomes visibly sluggish. Users feel it. Productivity drops.

## The Solution

Frappe Lightning is an **infra-level upgrade disguised as a feature**.

A dedicated Go service listens directly to the MariaDB **Binary Log** (via `go-mysql/canal`). Every time a record — a Sales Invoice, Customer, Item — is saved in Frappe, Go **instantly** captures the change and indexes it into Meilisearch. Zero polling. Zero Python overhead.

The result: A custom Go-powered **Cmd+K Command Palette** injected into Frappe Desk, giving users true "search-as-you-type" across millions of records with **sub-10ms latency**. Every. Single. Time.

---

## ✨ Core Features

| Feature | Description |
|---|---|
| ⚡ **Real-Time Binlog Sync** | Go listens to MariaDB binlog → instant Meilisearch upsert on every save |
| 🧠 **NLP Rule Engine** | Natural language → structured query (`"unpaid invoices last month above 10k"`) |
| 🔐 **Permission-Aware Search** | RBAC filters injected at query time; respects Frappe roles |
| 🖥️ **Cmd+K Command Palette** | VS Code-style omnibox with keyboard navigation + inline preview |
| 📊 **Search Analytics** | Top queries, zero-result tracking, latency dashboards |
| 🧩 **Pluggable Indexing** | `before_index` / `transform_doc` hooks per DocType |
| 📦 **Multi-Site Support** | Isolated Meilisearch indexes per Frappe site |
| 🔄 **Full Backfill** | One-command initial sync for existing millions of rows |
| 🧪 **Dev Tools** | Live binlog viewer, index diff checker, latency profiler |

---

## 🏗️ System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        FRAPPE DESK                          │
│          (Cmd+K Omnibox → Lightning JS Component)           │
└────────────────────────────┬────────────────────────────────┘
                             │  HTTP GET /search?q=...
                             ▼
┌─────────────────────────────────────────────────────────────┐
│              GO SEARCH PROXY (frappe_lightning)              │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐ │
│  │  NLP Engine  │  │  Auth / RBAC │  │  Meilisearch Fwd  │ │
│  │  (nlp/)      │  │  (Redis SID) │  │  (search/)        │ │
│  └──────────────┘  └──────────────┘  └───────────────────┘ │
└────────────────────────────┬────────────────────────────────┘
                             │  Filtered Query
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                      MEILISEARCH                            │
│    (per-site namespaced indexes, vector-ready)              │
└─────────────────────────────────────────────────────────────┘
                             ▲
              Instant Upsert │ on change
                             │
┌─────────────────────────────────────────────────────────────┐
│              GO BINLOG LISTENER (canal/)                     │
│   Reads MariaDB ROW-format binlog events in real-time       │
└────────────────────────────┬────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                  MARIADB (binlog_format=ROW)                 │
│    tabSales Invoice | tabCustomer | tabItem | ...           │
└─────────────────────────────────────────────────────────────┘
```

---

## 🧠 NLP Engine — "AI-feel without AI dependency"

Type natural language queries without any LLM:

```
"unpaid invoices last month above 10k"
→ { doctype: "Sales Invoice", status: "Overdue", date: last_month, amount: > 10000 }

"amazon customers"
→ { doctype: "Customer", text: "amazon" }

"purchase orders above 5 lakhs this year"
→ { doctype: "Purchase Order", grand_total: > 500000, date: this_year }
```

Power users also get an explicit DSL:
```
customer:"Amazon" AND status:"Overdue"
amount > 50000
date: last_30_days
```

---

## 🗺️ Roadmap

| Phase | Description | Status |
|---|---|---|
| **Phase 1** | Infrastructure & Binlog Connection | 🔄 In Progress |
| **Phase 2** | Indexing & Data Pipeline | 📋 Planned |
| **Phase 3** | Search API Proxy & Auth | 📋 Planned |
| **Phase 4** | Frappe Frontend UI & Analytics | 📋 Planned |
| **Phase 5** | NLP Rule Engine & Advanced Query Language | 🔄 In Progress |
| **Phase 6** | Smart Ranking, Pluggable Hooks & Multi-Tenancy | 📋 Planned |
| **Future** | AI Mode, Edge Cache, Dev Tools | 🔮 Vision |

---

## 🚀 Installation

```bash
cd $PATH_TO_YOUR_BENCH
bench get-app $URL_OF_THIS_REPO --branch develop
bench install-app f_lightning
```

### Prerequisites

- Frappe v14+
- MariaDB with `binlog_format=ROW`
- Meilisearch v1.x (Docker recommended)
- Go 1.21+

---

## ⚙️ Go Service Setup

```bash
cd frappe_lightning
go mod download
go run main.go --config config.yaml
```

### Backfill existing data

```bash
go run main.go backfill --doctype="Sales Invoice" --site=erp.local --batch=500
```

---

## 🤝 Contributing

This app uses `pre-commit` for code formatting and linting:

```bash
cd apps/f_lightning
pre-commit install
```

Tools configured: `ruff`, `eslint`, `prettier`, `pyupgrade`

---

## 📄 License

GPL-3.0
