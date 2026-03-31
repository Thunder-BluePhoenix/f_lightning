# ⚡ Frappe Lightning: User Guide

Welcome to **Frappe Lightning**, the high-performance search engine designed for the Frappe ecosystem. By leveraging Go and Meilisearch, Lightning achieves sub-10ms query latency even on sites with millions of records.

---

## 🏗️ 1. Setup & Infrastructure

### 🐳 Step 1: Run Meilisearch
Meilisearch is the underlying search engine. The simplest way to run it is via Docker:

```bash
docker run -itd -p 7700:7700 \
  -v $(pwd)/meili_data:/meili_data \
  getmeili/meilisearch:latest \
  meilisearch --master-key="your-master-key-here"
```

### 🗄️ Step 2: Configure MariaDB Binary Logs
Lightning uses the MariaDB Binary Log (binlog) for real-time synchronization. Your MariaDB configuration must include:

```ini
[mysqld]
binlog_format=ROW
binlog_row_image=FULL
server_id=1
log-bin=mysql-bin
```
*Note: Ensure the `lightning` database user has `REPLICATION CLIENT` and `REPLICATION SLAVE` permissions.*

### 🐹 Step 3: Deploy the Go Sidecar
1. Copy `config.yaml.example` to `config.yaml`:
   ```bash
   cp frappe_lightning/config.yaml.example frappe_lightning/config.yaml
   ```
2. Update the credentials for MariaDB, Meilisearch, and Redis.
3. Start the service:
   ```bash
   cd frappe_lightning
   go run main.go
   ```

### 🧩 Step 4: Install Frappe App
Ensure `f_lightning` is installed on your site:
```bash
bench get-app f_lightning
bench --site [your-site] install-app f_lightning
bench --site [your-site] migrate
```

---

## 💎 2. Core Concepts

### 🎹 Command Palette (`Cmd + K`)
The heart of Lightning is the global command palette. Press `Cmd + K` (Mac) or `Ctrl + K` (Windows/Linux) anywhere in Frappe to open the search bar.
- **Instant Search**: Results appear as you type.
- **Categorized Results**: Grouped by DocType (Invoices, Customers, Items).
- **Keyboard Navigation**: Use `↑` `↓` to navigate and `Enter` to open a record.

### 🧠 NLP-Powered Search
Instead of strict keywords, use natural language:
- `"unpaid invoices > 10k"`
- `"customers in Mumbai"`
- `"sales invoices from last month"`
Lightning's rule-based NLP engine converts these into precise filters automatically.

---

## 🚀 3. Advanced Features

### 🖼️ Inline Preview Panel
Hover over a result or use the arrow keys to see a quick preview of the document (e.g., total amount, status, and key dates) without leaving the search bar.

### 📌 Saved & Pinned Searches
Save frequent queries for one-click access. Pinned searches appear at the top of your search results for instant lookup.

### 🤖 Hybrid AI Search
If enabled (`ai_mode: local` or `cloud`), Lightning uses vector embeddings to provide semantic search results. 
- **Example**: Searching for "shoes" will find results for "footwear" or "sneakers" even if the exact word isn't present.

---

## 🛠️ 4. Admin Tools

### 🖥️ Lightning CLI
The `lightning` command-line tool provides powerful debugging and management capabilities:
- `lightning status`: Check health of binlog sync and search engine.
- `lightning watch`: Real-time stream of incoming binlog events.
- `lightning diff`: Compare MariaDB state vs. Meilisearch index for integrity.
- `lightning parse "query"`: Test how the NLP engine interprets a search string.

### 📊 Analytics Dashboard
Navigate to the **Lightning Search Analytics** page in Frappe to view:
- **Top Searches**: What your users are looking for.
- **Zero-Result Queries**: Identify gaps in your data or synonyms.
- **Latency Trend**: Monitor P99 response times.
- **CTR (Click-Through Rate)**: Validate the relevance of your rankings.

---

## ⚙️ 5. Customization

### 📝 Adding New DocTypes
To track a new DocType, add it to `frappe_lightning/config/schema.yaml`:
```yaml
- name: Lead
  table: tabLead
  fields:
    - name: lead_name
      searchable: true
    - name: status
      filterable: true
```
Then restart the Go service to apply the changes.

### ⚖️ Ranking Rules
Adjust weights in `frappe_lightning/config/ranking.yaml` to ensure the most important results appear first. For example, boost the `name` field over `description`.

---

> [!TIP]
> Use the `lightning parse` CLI command to debug complex NLP filters before deploying them.
