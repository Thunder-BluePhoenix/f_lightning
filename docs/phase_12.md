# Phase 12: Frappe Webhook Engine

**Goal:** Build a reliable, observable outgoing webhook service for Frappe. Instead of firing webhooks synchronously from Python during document saves (which blocks the request and silently drops events on failure), the Go engine captures every webhook event, persists it, delivers it with retries, logs every attempt, and provides a management CLI and dashboard.

---

## Why This Matters

Frappe's built-in webhook system fires HTTP requests synchronously in the Python `after_save` hook. If the endpoint is slow, it makes Frappe slow. If it's down, the webhook is silently dropped — no retry, no log, no alert.

For production integrations (ERP ↔ payment gateway, ERP ↔ logistics, ERP ↔ external CRM), silent drops are unacceptable. A dedicated Go engine:

- Persists every outgoing event to Redis Streams before attempting delivery — zero data loss.
- Delivers asynchronously — the Frappe save is never blocked.
- Retries failed deliveries with exponential backoff up to a configurable maximum.
- Logs every attempt (status code, latency, response body) to a queryable store.
- Signs every outgoing request with HMAC-SHA256 so receivers can verify authenticity.
- Exposes a live dashboard and CLI for visibility and manual replay.

---

## Architecture

```
Frappe (Python)
  │  calls lightning_webhook.enqueue(doctype, event, doc_data)
  │  (one frappe.call — non-blocking)
  ▼
┌──────────────────────────────────────────────────────┐
│  Redis Stream: lightning:webhooks:{site}             │
│  (append-only, survives restarts)                    │
└──────────────────────┬───────────────────────────────┘
                       │  XREADGROUP
                       ▼
┌──────────────────────────────────────────────────────┐
│  Lightning Webhook Engine  (Go)                      │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │  Event Consumer                              │    │
│  │  - reads from Redis Stream                   │    │
│  │  - resolves matching webhook subscriptions   │    │
│  │  - dispatches to delivery worker pool        │    │
│  └──────────────────────┬───────────────────────┘    │
│                         │                            │
│         ┌───────────────┼───────────────┐            │
│         ▼               ▼               ▼            │
│    Delivery W1     Delivery W2     Delivery W3       │
│   (goroutine)     (goroutine)     (goroutine)        │
│         │               │               │            │
│         ▼               ▼               ▼            │
│    HTTP POST to     HTTP POST to    HTTP POST to     │
│    endpoint A       endpoint B      endpoint C       │
│         │                                            │
│         ▼                                            │
│  ┌──────────────────────────────────────────────┐    │
│  │  Delivery Log (Redis Hash + MariaDB write)   │    │
│  │  - attempt #, status code, latency, body     │    │
│  └──────────────────────────────────────────────┘    │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │  Retry Scheduler                             │    │
│  │  - exponential backoff: 10s 30s 2m 10m 1h   │    │
│  │  - dead letter after max_retries             │    │
│  └──────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────┘
```

---

## Components

### 1. Frappe-Side Enqueue Hook

A thin Python function in the `f_lightning` app called from Frappe's `doc_events` hooks. It serialises the document and pushes it to Redis Streams — no synchronous HTTP:

```python
# f_lightning/hooks.py
doc_events = {
    "*": {
        "after_insert": "f_lightning.webhook.enqueue",
        "on_update":    "f_lightning.webhook.enqueue",
        "on_submit":    "f_lightning.webhook.enqueue",
        "on_cancel":    "f_lightning.webhook.enqueue",
        "on_trash":     "f_lightning.webhook.enqueue",
    }
}
```

```python
# f_lightning/webhook.py
import frappe, json

def enqueue(doc, method):
    site = frappe.local.site
    rdb  = frappe.cache()
    payload = {
        "site":    site,
        "doctype": doc.doctype,
        "name":    doc.name,
        "event":   method,
        "data":    doc.as_dict(),
    }
    rdb.xadd(
        f"lightning:webhooks:{site}",
        {"payload": json.dumps(payload)},
        maxlen=100_000,
    )
```

### 2. Webhook Subscription Config

Subscriptions are stored in a Frappe DocType (`Lightning Webhook`) and cached in Redis on the Go side:

```go
type WebhookSubscription struct {
    Name       string   `json:"name"`
    DocType    string   `json:"doctype"`     // "*" for all
    Events     []string `json:"events"`      // ["after_insert", "on_update"]
    EndpointURL string  `json:"endpoint_url"`
    SecretKey  string   `json:"secret_key"`  // for HMAC signing
    Enabled    bool     `json:"enabled"`
    MaxRetries int      `json:"max_retries"` // default 5
    TimeoutSec int      `json:"timeout_sec"` // default 10
}
```

### 3. Event Consumer

```go
func (e *Engine) Consume(ctx context.Context) {
    for {
        msgs, err := e.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
            Group:    "lightning-webhooks",
            Consumer: "worker-" + e.id,
            Streams:  []string{"lightning:webhooks:" + e.site, ">"},
            Count:    50,
            Block:    2 * time.Second,
        }).Result()

        for _, msg := range msgs[0].Messages {
            var event WebhookEvent
            json.Unmarshal([]byte(msg.Values["payload"].(string)), &event)

            subs := e.matchSubscriptions(event.DocType, event.Event)
            for _, sub := range subs {
                e.pool <- DeliveryTask{Event: event, Sub: sub, StreamID: msg.ID}
            }
        }
    }
}
```

### 4. Delivery Worker

```go
func (e *Engine) deliver(ctx context.Context, task DeliveryTask) DeliveryResult {
    payload, _ := json.Marshal(task.Event)
    sig := hmacSign(task.Sub.SecretKey, payload)

    req, _ := http.NewRequestWithContext(ctx, "POST", task.Sub.EndpointURL, bytes.NewReader(payload))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Lightning-Signature", "sha256="+sig)
    req.Header.Set("X-Lightning-Event", task.Event.Event)
    req.Header.Set("X-Lightning-Delivery", task.DeliveryID)

    client := &http.Client{Timeout: time.Duration(task.Sub.TimeoutSec) * time.Second}
    start := time.Now()
    resp, err := client.Do(req)
    latency := time.Since(start)

    result := DeliveryResult{
        DeliveryID: task.DeliveryID,
        AttemptNum: task.AttemptNum,
        LatencyMs:  latency.Milliseconds(),
    }

    if err != nil {
        result.Success = false
        result.Error = err.Error()
    } else {
        defer resp.Body.Close()
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
        result.StatusCode = resp.StatusCode
        result.ResponseBody = string(body)
        result.Success = resp.StatusCode >= 200 && resp.StatusCode < 300
    }
    return result
}
```

### 5. HMAC Signature

Every outgoing request is signed so receivers can verify it came from Frappe Lightning:

```go
func hmacSign(secret string, payload []byte) string {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    return hex.EncodeToString(mac.Sum(nil))
}
```

Receivers verify with:
```python
import hmac, hashlib
expected = "sha256=" + hmac.new(secret.encode(), payload, hashlib.sha256).hexdigest()
assert request.headers["X-Lightning-Signature"] == expected
```

### 6. Retry Scheduler

```go
var backoffSchedule = []time.Duration{
    10 * time.Second,
    30 * time.Second,
    2 * time.Minute,
    10 * time.Minute,
    1 * time.Hour,
}

func (e *Engine) scheduleRetry(task DeliveryTask) {
    if task.AttemptNum >= task.Sub.MaxRetries {
        e.moveToDLQ(task)
        return
    }
    delay := backoffSchedule[min(task.AttemptNum, len(backoffSchedule)-1)]
    time.AfterFunc(delay, func() {
        task.AttemptNum++
        e.pool <- task
    })
}
```

### 7. Delivery Log

Every attempt is written to a `Lightning Webhook Log` Frappe DocType (for the dashboard) and to Redis for fast recent-history lookups:

```go
type DeliveryLog struct {
    DeliveryID   string    `json:"delivery_id"`
    Subscription string    `json:"subscription"`
    DocType      string    `json:"doctype"`
    DocName      string    `json:"doc_name"`
    Event        string    `json:"event"`
    AttemptNum   int       `json:"attempt_num"`
    EndpointURL  string    `json:"endpoint_url"`
    StatusCode   int       `json:"status_code"`
    LatencyMs    int64     `json:"latency_ms"`
    Success      bool      `json:"success"`
    Error        string    `json:"error,omitempty"`
    ResponseBody string    `json:"response_body,omitempty"`
    AttemptedAt  time.Time `json:"attempted_at"`
}
```

---

## CLI Commands

```bash
# Service status
lightning webhooks status --site erp.local

# List recent deliveries
lightning webhooks list --site erp.local --last 50
lightning webhooks list --site erp.local --status failed
lightning webhooks list --site erp.local --doctype "Sales Invoice"

# Inspect a delivery
lightning webhooks show <delivery-id> --site erp.local

# Replay a delivery (re-send to the same endpoint)
lightning webhooks replay <delivery-id> --site erp.local

# Replay all failed deliveries for a subscription
lightning webhooks replay-failed --site erp.local --subscription my-sub

# List subscriptions
lightning webhooks subscriptions --site erp.local

# Test a subscription with a sample payload
lightning webhooks test --site erp.local --subscription my-sub --doctype "Customer" --name CUST-0001

# DLQ management
lightning webhooks dlq list --site erp.local
lightning webhooks dlq replay --site erp.local
lightning webhooks dlq flush --site erp.local
```

---

## Prometheus Metrics

| Metric | Type | Labels |
|--------|------|--------|
| `lightning_webhook_deliveries_total` | Counter | `site`, `doctype`, `event`, `status` |
| `lightning_webhook_delivery_latency_seconds` | Histogram | `site`, `endpoint` |
| `lightning_webhook_pending` | Gauge | `site` |
| `lightning_webhook_dlq_depth` | Gauge | `site` |
| `lightning_webhook_retry_total` | Counter | `site`, `attempt_num` |

---

## Task Checklist

- [ ] Create `Lightning Webhook` and `Lightning Webhook Log` Frappe DocTypes
- [ ] Implement `f_lightning/webhook.py` — `enqueue()` Python function
- [ ] Wire `doc_events` in `hooks.py` to call `enqueue` for all DocTypes
- [ ] Implement `webhook/consumer.go` — Redis Streams XREADGROUP consumer
- [ ] Implement `webhook/subscriptions.go` — load and cache subscriptions from Frappe DB
- [ ] Implement `webhook/delivery.go` — HTTP POST with HMAC signing and timeout
- [ ] Implement `webhook/retry.go` — backoff scheduler and DLQ management
- [ ] Implement `webhook/log.go` — write attempt results to Redis + Frappe DocType
- [ ] Implement `webhook/metrics.go` — Prometheus counters and histograms
- [ ] Add `lightning webhooks` subcommands to the CLI
- [ ] Build Frappe Page: Webhook Dashboard (delivery log, retry rate, DLQ depth)
- [ ] Write unit tests for HMAC signing and retry backoff schedule
- [ ] Integration test: save a Frappe Customer → webhook delivered to test endpoint
- [ ] Test: endpoint returns 500 → retried 5 times → moved to DLQ
- [ ] Test: `lightning webhooks replay` re-delivers successfully to recovered endpoint

---

## Validation Checklist

- [ ] Webhook delivered within 500ms of Frappe document save
- [ ] `X-Lightning-Signature` header passes HMAC verification on receiver
- [ ] Failed endpoint retried at `10s → 30s → 2m → 10m → 1h` intervals
- [ ] After max retries, delivery appears in `lightning webhooks dlq list`
- [ ] `lightning webhooks replay` successfully re-delivers from DLQ
- [ ] Redis Stream depth (`lightning:webhooks:{site}`) stays bounded at 100,000
- [ ] Prometheus `lightning_webhook_dlq_depth` gauge reflects live DLQ depth
- [ ] Webhook Dashboard shows live delivery log with latency and status codes
