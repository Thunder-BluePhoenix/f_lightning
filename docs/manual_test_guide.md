# Lightning Manual Test Guide

This guide covers every test item marked `(manual)` in `tracker.md` for phases 9–12.  
Run these after deploying the bench (`bench start`) with Lightning running alongside.

---

## Prerequisites

```bash
# Lightning must be compiled and running
cd ~/frappe-bench/apps/f_lightning/frappe_lightning
go build -o ../lightning .
../lightning --config config.yaml &

# Confirm health
curl -s http://localhost:8765/health | python3 -m json.tool
```

Expected:
```json
{ "status": "ok", "sites": [...] }
```

---

## Phase 9 — API Gateway

### Setup

Enable the gateway in `config.yaml`:

```yaml
gateway:
  enabled: true
  listen_port: 7000
  sites:
    - name: erp.local
      upstream_workers:
        - http://127.0.0.1:8000   # Frappe gunicorn
  auth:
    skip_paths:
      - /assets/
      - /api/method/login
  rate_limits:
    - path_prefix: /api/
      per_user_rps: 20
  cache:
    enabled: true
    rules:
      - path_prefix: /api/resource/
        ttl_seconds: 60
  circuit_breaker:
    failure_threshold: 5
    open_duration_sec: 30
    half_open_probe_interval: 10
```

Restart Lightning. Confirm:

```bash
lightning gateway status --config config.yaml
# Expect: Listen port: 7000, Sites configured: 1
```

---

### T9-1 — Frappe Desk loads through gateway

```bash
# Browse to http://localhost:7000
# Log in as Administrator
# Verify: Desk loads, modules visible, no console errors
# Open browser DevTools → Network tab
# Confirm requests go to :7000 not :8000
```

**Pass:** Desk fully functional through :7000.

---

### T9-2 — Cache: GET cached in <1ms on second request

```bash
# First request — cache miss
time curl -s -b "sid=<your-sid>" \
  "http://localhost:7000/api/resource/Customer?limit=5" > /dev/null

# Second request — cache hit (should be much faster)
time curl -s -b "sid=<your-sid>" \
  "http://localhost:7000/api/resource/Customer?limit=5" > /dev/null

# Check stats
lightning gateway cache-stats --config config.yaml
# Expect: Hits > 0, Hit Rate > 0%
```

**Pass:** Second request completes in <10ms (or cache hit count increments).

---

### T9-3 — Circuit breaker opens after 5 failures

```bash
# Stop gunicorn to simulate upstream failure
pkill -f gunicorn

# Send 5+ requests through the gateway
for i in $(seq 1 6); do
  curl -s -o /dev/null -w "%{http_code}\n" http://localhost:7000/api/method/ping
done
# Expect: first 5 return 502/503, then 503 immediately (circuit open)

# Check Lightning log — should show:
# "circuit breaker: opened — upstream is unhealthy"

# Restart gunicorn and wait 30s for half-open probe
bench start &
sleep 35

# One request should succeed (half-open probe) → circuit closes
curl -s http://localhost:7000/api/method/ping
# Expect: 200
```

**Pass:** Logged "opened", then "closed — upstream recovered".

---

### T9-4 — Rate limit blocks at threshold

```bash
# Send 25 rapid requests as the same user (limit is 20/s)
for i in $(seq 1 25); do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -b "sid=<your-sid>" \
    "http://localhost:7000/api/resource/Customer" &
done
wait
# Expect: some 429 responses appear after request 20
```

**Pass:** At least one 429 in the output.

---

## Phase 10 — Background Job Runner

### Setup

```yaml
# config.yaml
job_runner:
  enabled: true
  bench_path: /home/frappe/frappe-bench
  queues:
    - name: high
      concurrency: 10
    - name: default
      concurrency: 20
    - name: low
      concurrency: 5
  max_retries: 3
```

---

### T10-1 — Enqueue RQ job from Python → Go runner picks up

```bash
# Open Frappe bench console
bench --site erp.local console
```

In the console:

```python
import frappe
frappe.init(site='erp.local')
frappe.connect()

# Enqueue a simple job
import frappe.utils.background_jobs as bg
bg.enqueue('frappe.utils.background_jobs.get_jobs', queue='default', site='erp.local')
```

Back in the terminal:

```bash
# Watch Lightning logs
# Expect: "job finished" log line with call_string = frappe.utils.background_jobs.get_jobs
lightning jobs status erp.local --config config.yaml
# Expect: default queue pending count drops by 1
```

**Pass:** Job log shows `"status": "finished"` in Redis.

```bash
# Verify in Redis
redis-cli -p 13000 hget rq:job:<uuid> status
# Expect: "finished"
```

---

### T10-2 — Failed job retries 3 times then moves to rq:queue:failed

```bash
# Enqueue a job that will always fail
bench --site erp.local console
```

```python
import frappe.utils.background_jobs as bg
# This method does not exist — will throw ImportError
bg.enqueue('frappe.does_not_exist.fake_method', queue='default', site='erp.local')
```

```bash
# Watch Lightning logs — expect 3 retry attempts:
# "job failed — scheduling retry" (attempt=1, delay=2s)
# "job failed — scheduling retry" (attempt=2, delay=4s)
# "job permanently failed — moved to dead-letter queue"

# Verify in Redis
redis-cli -p 13000 llen rq:queue:failed
# Expect: 1

redis-cli -p 13000 hget rq:job:<uuid> status
# Expect: "failed"

# Via CLI
lightning jobs status erp.local --config config.yaml
# Expect: failed queue = 1

# Recover: retry all
lightning jobs retry-all erp.local --config config.yaml
# Expect: "re-enqueued 1 failed jobs → default queue ✓"
```

**Pass:** Job appears in `rq:queue:failed` after 3 failed attempts.

---

### T10-3 — Scheduled task fires at correct frequency

```bash
# Verify there is at least one Hourly scheduled task in Frappe
bench --site erp.local console
```

```python
import frappe
frappe.db.get_all('Scheduled Job Type', filters={'frequency': 'Hourly'}, fields=['method'])
# Expect: list of methods
```

```bash
# Restart Lightning with job_runner enabled and watch logs
# After ~1 minute (the scheduler polls every tick), you should see:
# "scheduled job enqueued" with site=erp.local

# To trigger immediately without waiting, temporarily set frequency in DB:
# UPDATE `tabScheduled Job Type` SET frequency='All' WHERE frequency='Hourly' LIMIT 1;
# Restart Lightning — job should enqueue within 5 minutes
```

**Pass:** "scheduled job enqueued" log line appears within the expected interval.

---

## Phase 11 — frapctl CLI

### Build the binary

```bash
cd ~/frappe-bench/apps/f_lightning/frappe_lightning
go build -o /usr/local/bin/frapctl ./cmd/frapctl/
frapctl --help
# Expect: usage output with site/app/migrate/cache/service/config/shell/console subcommands
```

---

### T11-1 — frapctl site list auto-discovers bench

```bash
# Navigate into any directory inside the bench
cd ~/frappe-bench/apps/frappe

frapctl site list
# Expect: list of sites WITHOUT needing --bench flag
# e.g.  erp.local   frappe, erpnext, f_lightning

# Also test from the bench root itself
cd ~/frappe-bench
frapctl site list
```

**Pass:** Sites listed without `--bench` flag from inside the bench.

---

### T11-2 — frapctl cache clear

```bash
# Check how many Redis keys exist before
redis-cli -p 13005 dbsize

# Clear cache for one site
frapctl cache clear --site erp.local
# Expect: "cleared N cache keys for erp.local ✓"

# Clear all
frapctl cache clear --all
# Expect: "cache flushed: OK ✓"

# Verify stats
frapctl cache stats
```

**Pass:** Completes in <500ms (direct Redis, no Python startup).

---

### T11-3 — frapctl config get/set atomic write

```bash
# Read a value
frapctl config get db_name --site erp.local
# Expect: _erplocal (or similar)

# Set maintenance mode
frapctl config set maintenance_mode 1 --site erp.local
# Expect: "maintenance_mode = 1 ✓"

# Verify file was written
cat ~/frappe-bench/sites/erp.local/site_config.json | python3 -m json.tool | grep maintenance
# Expect: "maintenance_mode": "1"

# Reset
frapctl config set maintenance_mode 0 --site erp.local
```

**Pass:** File updated atomically; no partial JSON on disk.

---

### T11-4 — frapctl shell

```bash
frapctl shell "frappe.db.count('Customer')" --site erp.local
# Expect: integer count printed
```

**Pass:** Output matches `bench --site erp.local execute "frappe.db.count('Customer')"`.

---

### T11-5 — Shell completion

```bash
# bash
source <(frapctl completion bash)
frapctl <TAB><TAB>
# Expect: site, app, migrate, cache, service, config, shell, console, completion

# zsh
source <(frapctl completion zsh)
frapctl site <TAB>
# Expect: list, create, drop, backup, restore

# fish
frapctl completion fish | source
```

**Pass:** Tab completion works in at least one shell.

---

### T11-6 — ~/.frapctl.yaml defaults

```bash
cat > ~/.frapctl.yaml << 'EOF'
bench: /home/frappe/frappe-bench
default_site: erp.local
EOF

# Should work without any flags now
cd /tmp
frapctl site list
# Expect: sites from ~/frappe-bench, no --bench required

frapctl config show
# Expect: erp.local site_config.json printed, no --site required
```

**Pass:** Both commands work with zero flags.

---

### T11-7 — Binary runs without Python

```bash
# On a clean Ubuntu box or Docker container with no Python:
docker run --rm -v /usr/local/bin/frapctl:/usr/local/bin/frapctl ubuntu:22.04 \
  frapctl --help
# Expect: usage output (no "python3: not found" error)
```

**Pass:** Binary executes with no Python dependency.

---

## Phase 12 — Webhook Engine

### Setup

```yaml
# config.yaml
webhook:
  enabled: true
  worker_pool: 20
```

After `bench migrate` (to create `Lightning Webhook` and `Lightning Webhook Log` tables):

```bash
bench --site erp.local migrate
```

Create a test subscription in Frappe:
1. Go to **Lightning Webhook** DocType → New
2. Name: `test-customer-hook`
3. DocType: `Customer`
4. Events: `["on_update", "after_insert"]`
5. Endpoint URL: `https://webhook.site/<your-id>` (get a free URL at webhook.site)
6. Secret Key: `test-secret-123`
7. Enabled: ✓
8. Save

---

### T12-1 — Webhook delivered within 500ms of document save

```bash
# Restart Lightning with webhook.enabled: true
pkill lightning
./lightning --config config.yaml &

# In Frappe, edit any Customer and save
# Or via console:
bench --site erp.local console
```

```python
doc = frappe.get_doc('Customer', frappe.get_list('Customer', limit=1)[0].name)
doc.flags.ignore_permissions = True
doc.save()
```

```bash
# On webhook.site, verify the delivery arrived
# Check Lightning logs:
# "webhook engine started" site=erp.local
# "delivery worker" started
# After save: POST logged with status 200

# Check delivery log CLI
lightning webhooks list erp.local --config config.yaml
# Expect: row with success=✓ and latency_ms
```

**Pass:** Webhook appears on webhook.site within 500ms of the save.

---

### T12-2 — Verify X-Lightning-Signature header

On webhook.site, inspect the request headers. You should see:

```
X-Lightning-Signature: sha256=<64-char hex>
X-Lightning-Event: on_update
X-Lightning-Delivery: <uuid>
X-Lightning-Attempt: 1
```

Verify the signature manually:

```python
import hmac, hashlib, json

secret = "test-secret-123"
# Copy the raw body from webhook.site
body = b'{"site":"erp.local","doctype":"Customer",...}'

expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
print(expected)
# Compare to X-Lightning-Signature header value
```

**Pass:** Computed signature matches the header.

---

### T12-3 — Failed endpoint retried 5 times → moved to DLQ

Update the test subscription's endpoint to a URL that always returns 500:

```bash
# Use a local httpbin for controlled responses
docker run -p 9999:80 kennethreitz/httpbin
# Endpoint: http://localhost:9999/status/500
```

Update the subscription endpoint URL to `http://localhost:9999/status/500`, save.

Save a Customer document to trigger the webhook.

```bash
# Watch logs — expect:
# attempt 1 → failed (500) → retry in 10s
# attempt 2 → failed (500) → retry in 30s
# attempt 3 → failed (500) → retry in 2m
# attempt 4 → failed (500) → retry in 10m
# attempt 5 → failed (500) → moved to DLQ

# Check DLQ depth
lightning webhooks dlq-list erp.local --config config.yaml
# Expect: 1 item with delivery_id, endpoint, attempt_num=5

# DLQ depth gauge should update in Prometheus:
curl -s http://localhost:8765/metrics | grep lightning_webhook_dlq_depth
# Expect: lightning_webhook_dlq_depth{site="erp.local"} 1
```

**Pass:** After 5 failed attempts, entry appears in DLQ list.

---

### T12-4 — DLQ replay re-delivers successfully

Fix the endpoint (update subscription URL back to webhook.site):

```bash
lightning webhooks dlq-replay erp.local --config config.yaml
# Expect: "replayed N items — N succeeded, 0 failed ✓"

# Verify on webhook.site — delivery should arrive
# Verify DLQ is now empty
lightning webhooks dlq-list erp.local --config config.yaml
# Expect: 0 items
```

**Pass:** DLQ flushed, delivery confirmed on receiver.

---

### T12-5 — Webhook Dashboard in Frappe Desk

```bash
bench --site erp.local migrate   # ensure page is registered
bench build --app f_lightning
bench --site erp.local clear-cache
```

Navigate to:  
`http://localhost:8000/app/lightning-webhooks`

Verify:
- [ ] KPI strip shows: Deliveries (24h), Success Rate, Failed, Retried, Avg Latency, Subscriptions
- [ ] Hourly trend table populated with last 24h data
- [ ] Retry breakdown shows attempt counts
- [ ] Subscriptions table shows all configured hooks with per-sub stats
- [ ] Recent Deliveries table shows last 50 rows with status, latency, attempt#
- [ ] **Replay** button appears on failed rows; clicking it queues a re-delivery
- [ ] Refresh button reloads all data without page reload

**Pass:** All sections render with live data; replay button works end-to-end.

---

## Summary Checklist

| # | Phase | Test | Manual |
|---|-------|------|--------|
| T9-1 | Gateway | Frappe Desk loads through :7000 | [ ] |
| T9-2 | Gateway | Cached GET faster on 2nd request | [ ] |
| T9-3 | Gateway | Circuit opens after 5 failures, closes on recovery | [ ] |
| T9-4 | Gateway | Rate limit returns 429 beyond threshold | [ ] |
| T10-1 | Jobs | RQ job enqueued from Python → Go runner executes | [ ] |
| T10-2 | Jobs | Failed job retries 3× → rq:queue:failed | [ ] |
| T10-3 | Jobs | Scheduled task fires at correct interval | [ ] |
| T11-1 | frapctl | `site list` without `--bench` from inside bench | [ ] |
| T11-2 | frapctl | `cache clear` completes <500ms | [ ] |
| T11-3 | frapctl | `config set` writes atomically | [ ] |
| T11-4 | frapctl | `shell` runs Python expression | [ ] |
| T11-5 | frapctl | Tab completion works in bash/zsh | [ ] |
| T11-6 | frapctl | `~/.frapctl.yaml` defaults respected | [ ] |
| T11-7 | frapctl | Binary runs without Python runtime | [ ] |
| T12-1 | Webhooks | Delivery arrives within 500ms of save | [ ] |
| T12-2 | Webhooks | `X-Lightning-Signature` passes HMAC verification | [ ] |
| T12-3 | Webhooks | 5 failures → DLQ | [ ] |
| T12-4 | Webhooks | DLQ replay delivers successfully | [ ] |
| T12-5 | Webhooks | Dashboard page renders with live data | [ ] |
