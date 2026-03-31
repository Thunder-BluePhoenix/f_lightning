# Frappe Lightning: Production Deployment Guide

This guide covers going from a working development setup to a production-grade deployment. It assumes you have a running Frappe bench and a Linux server.

---

## Prerequisites

| Dependency | Version | Notes |
|------------|---------|-------|
| Go | 1.21+ | For building the binary |
| Meilisearch | 1.6+ | Standalone process or Docker |
| MariaDB | 10.6+ | Must have ROW-format binlog enabled |
| Redis | 6+ | Frappe's existing Redis cache instance |
| Frappe | v15 / v16 | Any site installed under the bench |

---

## Step 1 — Prepare MariaDB

Lightning connects to MariaDB as a **replication slave**. The binlog must be in ROW format.

### 1a. Enable Binlog

Edit `/etc/mysql/mariadb.conf.d/50-server.cnf` (or wherever your MariaDB config lives):

```ini
[mysqld]
binlog_format    = ROW
binlog_row_image = FULL
server_id        = 1
log-bin          = mysql-bin
expire_logs_days = 7
```

Restart MariaDB:

```bash
sudo systemctl restart mariadb
```

Verify:

```sql
SHOW VARIABLES LIKE 'binlog_format';   -- should show ROW
SHOW MASTER STATUS;                    -- should show a log file and position
```

### 1b. Create the Lightning Database User

```sql
CREATE USER 'lightning'@'%' IDENTIFIED BY 'strong_password';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'lightning'@'%';
GRANT SELECT ON `_your_site_db_`.* TO 'lightning'@'%';
FLUSH PRIVILEGES;
```

> The site database name in Frappe bench is typically the same as the site name (dots replaced with underscores are handled by MariaDB internally, but the actual DB name matches the site name character-for-character, e.g., `erp.local`). Check with `bench --site erp.local show-config | grep db_name`.

---

## Step 2 — Deploy Meilisearch

### Option A: Docker (Recommended for simplicity)

```bash
docker run -d \
  --name meilisearch \
  --restart unless-stopped \
  -p 7700:7700 \
  -v /var/lib/meilisearch:/meili_data \
  getmeili/meilisearch:latest \
  meilisearch --master-key="your-strong-master-key" --env="production"
```

### Option B: Binary

```bash
curl -L https://install.meilisearch.com | sh
./meilisearch --master-key="your-strong-master-key" \
              --db-path /var/lib/meilisearch \
              --env production
```

### Option C: systemd Service (after binary install)

```ini
# /etc/systemd/system/meilisearch.service
[Unit]
Description=Meilisearch
After=network.target

[Service]
User=meilisearch
ExecStart=/usr/local/bin/meilisearch \
  --master-key=your-strong-master-key \
  --db-path=/var/lib/meilisearch \
  --env=production
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now meilisearch
```

---

## Step 3 — Build the Lightning Binary

```bash
cd /opt/frappe-lightning      # or wherever you cloned the repo
git clone <repo-url> .

cd frappe_lightning
go build -o /usr/local/bin/lightning-server ./main.go
go build -o /usr/local/bin/lightning ./cmd/lightning/main.go
```

Verify:

```bash
lightning-server --help
lightning status --config /etc/frappe-lightning/config.yaml
```

---

## Step 4 — Write the Production Config

```bash
sudo mkdir -p /etc/frappe-lightning
sudo cp frappe_lightning/config.yaml.example /etc/frappe-lightning/config.yaml
sudo nano /etc/frappe-lightning/config.yaml
```

Minimum changes from the example:

1. Set `mariadb.password` to the password you set in Step 1b.
2. Set `meilisearch.master_key` to the key you chose in Step 2.
3. Set `redis.port` to Frappe's Redis cache port (default `11000` in bench; check `bench config` or `sites/common_site_config.json`).
4. Set `api_token` to a strong random string. Never leave it as `lightning-secret-dev`.
5. Set `sites[].name` to your Frappe site hostname.

Generate a strong API token:

```bash
openssl rand -hex 32
```

---

## Step 5 — Run the Backfill (Initial Sync)

Before the service starts tracking live changes, populate the Meilisearch indexes with existing data using the backfill CLI:

```bash
lightning backfill \
  --config /etc/frappe-lightning/config.yaml \
  --site erp.local \
  --doctype "Sales Invoice"

lightning backfill \
  --config /etc/frappe-lightning/config.yaml \
  --site erp.local \
  --doctype Customer
```

Run the backfill for every DocType you have listed in `config.yaml`. For large tables (millions of rows), the backfill streams in batches of 100 documents with a progress indicator.

> The backfill connects directly to MariaDB via SQL (`SELECT *`), not the binlog. Start it before launching the main service to avoid missing events during the initial sweep.

---

## Step 6 — Create the systemd Service

```ini
# /etc/systemd/system/frappe-lightning.service
[Unit]
Description=Frappe Lightning Search Engine
After=network.target mariadb.service meilisearch.service

[Service]
User=frappe
Group=frappe
WorkingDirectory=/etc/frappe-lightning
ExecStart=/usr/local/bin/lightning-server --config /etc/frappe-lightning/config.yaml
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
# Prevent the service from touching the host filesystem outside its working dir
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now frappe-lightning
sudo journalctl -u frappe-lightning -f    # tail logs
```

Expected startup output:

```
⚡ Frappe Lightning starting  config=config.yaml
index ready  site=erp.local  index=erp_local_sales_invoice
index ready  site=erp.local  index=erp_local_customer
...
site pipeline started  site=erp.local  mariadb=127.0.0.1:3306
⚡ Lightning is running — multi-tenant mode active
starting global Multi-Tenant API server  addr=:8765  tenants_initialized=1
```

---

## Step 7 — Reverse Proxy with Nginx

Do not expose port 8765 directly. Put Nginx in front to handle TLS, CORS, and tighter rate limiting.

```nginx
# /etc/nginx/sites-available/frappe-lightning
upstream lightning {
    server 127.0.0.1:8765;
}

server {
    listen 443 ssl http2;
    server_name lightning.erp.example.com;

    ssl_certificate     /etc/letsencrypt/live/lightning.erp.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/lightning.erp.example.com/privkey.pem;

    # Strict CORS — only allow your Frappe origin
    add_header Access-Control-Allow-Origin "https://erp.example.com" always;
    add_header Access-Control-Allow-Headers "Authorization, Content-Type, X-Frappe-Site-Name" always;
    add_header Access-Control-Allow-Credentials "true" always;

    location / {
        proxy_pass http://lightning;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 30s;
    }

    # Block public access to metrics
    location /metrics {
        allow 10.0.0.0/8;    # internal monitoring subnet only
        deny all;
        proxy_pass http://lightning;
    }
}
```

Update your `config.yaml` `allowed_origins` to match your Frappe domain, then reload Nginx:

```bash
sudo nginx -t && sudo systemctl reload nginx
```

---

## Step 8 — Install the Frappe App

```bash
cd /home/frappe/frappe-bench
bench get-app f_lightning /path/to/f_lightning
bench --site erp.local install-app f_lightning
bench --site erp.local migrate
bench restart
```

The app injects the `Cmd+K` search bar into Frappe Desk and configures the Lightning endpoint URL. By default it points to `http://localhost:8765`; update the Lightning endpoint in the app's site config if you're using the Nginx reverse proxy.

---

## Operational Tasks

### Verify Sync Health

Check that binlog replication is keeping up:

```bash
lightning status --config /etc/frappe-lightning/config.yaml
```

Compare MariaDB row count vs Meilisearch document count:

```bash
lightning diff erp.local "Sales Invoice" --config /etc/frappe-lightning/config.yaml
```

Watch live binlog events:

```bash
lightning watch erp.local --config /etc/frappe-lightning/config.yaml
```

### Re-index a DocType

If the Meilisearch index for a DocType gets out of sync (e.g., after schema changes or a Meilisearch wipe):

```bash
lightning backfill \
  --config /etc/frappe-lightning/config.yaml \
  --site erp.local \
  --doctype "Sales Invoice"
```

This performs a full SQL sweep and re-uploads all documents. It is safe to run while the main service is running — backfill uses SQL, not the binlog, so it does not interfere with live sync.

### Rotate the API Token

1. Update `api_token` in `/etc/frappe-lightning/config.yaml`.
2. Restart the service: `sudo systemctl restart frappe-lightning`.
3. Update the token in any external integrations.

### Upgrade Lightning

```bash
cd /opt/frappe-lightning
git pull
cd frappe_lightning
go build -o /usr/local/bin/lightning-server ./main.go
go build -o /usr/local/bin/lightning ./cmd/lightning/main.go
sudo systemctl restart frappe-lightning
```

Lightning resumes from the last saved binlog position on restart — no data loss.

### Monitor with Prometheus + Grafana

Add a scrape job to `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: frappe-lightning
    static_configs:
      - targets: ["localhost:8765"]   # or your internal IP if Prometheus is remote
```

Key metrics to alert on:

| Metric | Alert condition |
|--------|----------------|
| `lightning_binlog_lag_seconds` | > 10 seconds sustained — sync is falling behind |
| `lightning_indexing_errors_total` increase rate | > 0 per minute — documents failing to index |
| `lightning_search_duration_seconds` p99 | > 100ms — search latency degradation |

---

## Security Checklist

- [ ] `api_token` is a randomly generated 32+ character string, not the default.
- [ ] Meilisearch `master_key` is a strong random string.
- [ ] Port 8765 is not exposed to the internet; Nginx is in front.
- [ ] `/metrics` endpoint is restricted to the internal monitoring subnet.
- [ ] MariaDB `lightning` user has no `WRITE` privileges.
- [ ] Meilisearch port 7700 is firewalled from the public internet.
- [ ] TLS is enabled on the Nginx reverse proxy.
- [ ] The `frappe-lightning` systemd service runs as the `frappe` user, not root.

---

## Troubleshooting

### Lightning cannot connect to MariaDB binlog

```
canal: NewCanal failed: ... Access denied for user 'lightning'
```

Re-run the `GRANT REPLICATION SLAVE` SQL from Step 1b and flush privileges.

---

### Meilisearch index settings fail on startup

```
WARN  failed to init index  site=erp.local  index=erp_local_sales_invoice
```

This usually means Meilisearch is not yet ready. Lightning logs a warning and continues. The index will be initialized on the next restart or when the sync engine tries to write to it.

---

### Sessions not validating (401 on all requests)

Check the Redis key format. Frappe v15/v16 uses `{site}|sessiondata|{sid}`. Inspect live keys:

```bash
redis-cli -p 11000 KEYS "*sessiondata*" | head -5
```

If the key format differs from what Lightning expects, see [developer_guide.md](developer_guide.md) for the session extraction customisation point.

---

### High binlog lag

```
lightning_binlog_lag_seconds{site="erp.local"} 45
```

Possible causes:
1. **Batcher backpressure** — a slow Meilisearch instance is not draining the event channel fast enough. Check Meilisearch CPU and memory.
2. **Large bulk import in MariaDB** — `bench migrate` or a mass import generates a flood of binlog events. The batcher will catch up; lag will return to normal.
3. **Dead letter queue accumulating** — check Lightning logs for repeated retry failures.
