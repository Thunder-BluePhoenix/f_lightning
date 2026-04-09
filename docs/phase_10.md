# Phase 10: Frappe Background Job Runner

**Goal:** Replace Frappe's Python RQ (Redis Queue) workers with a Go-based job runner that handles thousands of concurrent jobs using goroutines, provides priority queues, job scheduling, retry logic, and a live monitoring CLI — all without changing how Frappe enqueues jobs.

---

## Why This Matters

Frappe's background workers run on RQ + Python. Each worker is a separate OS process, consumes ~150MB RAM at idle, and can only execute one job at a time. Scaling to handle burst load means spawning more processes.

A Go worker can handle thousands of concurrent jobs in goroutines from a single process with a tiny memory footprint, while consuming the exact same Redis queues that RQ produces — meaning zero changes to Frappe's application code.

---

## Architecture

```
Frappe (Python)
  │  enqueues jobs the normal RQ way
  ▼
┌──────────────────────────────────────────────────────┐
│              Redis Job Queues                        │
│   default   high   low   long   short   (RQ format)  │
└──────────────────────┬───────────────────────────────┘
                       │  BLPOP (Go worker polls)
                       ▼
┌──────────────────────────────────────────────────────┐
│         Lightning Job Runner  (Go)                   │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │  Job Dispatcher                              │    │
│  │  - priority queue drain (high → default → low│    │
│  │  - concurrency limit per queue               │    │
│  │  - goroutine pool                            │    │
│  └──────────────────────┬───────────────────────┘    │
│                         │ dispatch                   │
│         ┌───────────────┼───────────────┐            │
│         ▼               ▼               ▼            │
│    Worker G1       Worker G2       Worker G3 ...     │
│   (goroutine)     (goroutine)     (goroutine)        │
│         │               │               │            │
│         └───────────────┴───────────────┘            │
│                         │                            │
│              ┌──────────▼──────────┐                 │
│              │  Python Executor    │                 │
│              │  exec frappe job    │                 │
│              │  via subprocess or  │                 │
│              │  bench execute      │                 │
│              └─────────────────────┘                 │
│                                                      │
│  ┌──────────────────────────────────────────────┐    │
│  │  Job Scheduler (cron-like)                   │    │
│  │  - parse Frappe scheduled tasks config       │    │
│  │  - enqueue on schedule via Redis             │    │
│  └──────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────┘
```

---

## Components

### 1. RQ-Compatible Job Consumer

Frappe's RQ jobs are stored in Redis as JSON blobs under keys like `rq:job:{uuid}`. The runner reads these using `BLPOP` on the queue list — identical to how the Python RQ worker reads them.

```go
type RQJob struct {
    ID          string                 `json:"id"`
    Description string                 `json:"description"`
    CallString  string                 `json:"call_string"`   // e.g. "frappe.email.queue.send_one"
    Args        []interface{}          `json:"args"`
    Kwargs      map[string]interface{} `json:"kwargs"`
    TTL         int                    `json:"ttl"`
    Timeout     int                    `json:"timeout"`
    EnqueuedAt  time.Time              `json:"enqueued_at"`
}

func (r *Runner) pop(ctx context.Context, queues []string) (*RQJob, error) {
    result, err := r.rdb.BLPop(ctx, 2*time.Second, queues...).Result()
    if err != nil || len(result) < 2 {
        return nil, err
    }
    raw, _ := r.rdb.Get(ctx, "rq:job:"+result[1]).Bytes()
    var job RQJob
    json.Unmarshal(raw, &job)
    return &job, nil
}
```

### 2. Goroutine Worker Pool

```go
type Runner struct {
    site       string
    rdb        *redis.Client
    queues     []QueueConfig     // ordered: high, default, low
    semaphores map[string]chan struct{} // per-queue concurrency limit
    log        *zap.Logger
}

func (r *Runner) Start(ctx context.Context) {
    for {
        job, err := r.pop(ctx, r.queueNames())
        if err != nil || job == nil {
            continue
        }
        queueName := r.detectQueue(job)
        sem := r.semaphores[queueName]
        sem <- struct{}{}        // acquire slot
        go func(j *RQJob) {
            defer func() { <-sem }()
            r.execute(ctx, j)
        }(job)
    }
}
```

```yaml
# config.yaml
job_runner:
  queues:
    - name: high
      concurrency: 10
    - name: default
      concurrency: 20
    - name: low
      concurrency: 5
    - name: long
      concurrency: 3      # long-running jobs get fewer slots
```

### 3. Python Job Executor

Frappe jobs are Python functions. The Go runner executes them via a subprocess call to `bench execute` or a thin Python shim:

```go
func (r *Runner) execute(ctx context.Context, job *RQJob) {
    // Set job status to "started" in Redis
    r.rdb.HSet(ctx, "rq:job:"+job.ID, "status", "started", "started_at", time.Now().Unix())

    timeout := time.Duration(job.Timeout) * time.Second
    if timeout == 0 {
        timeout = 5 * time.Minute
    }
    execCtx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    cmd := exec.CommandContext(execCtx,
        "bench", "--site", r.site, "execute", job.CallString,
        "--args", marshalArgs(job.Args),
        "--kwargs", marshalKwargs(job.Kwargs),
    )
    cmd.Dir = r.benchPath

    output, err := cmd.CombinedOutput()
    if err != nil {
        r.handleFailure(ctx, job, err, string(output))
        return
    }
    r.rdb.HSet(ctx, "rq:job:"+job.ID, "status", "finished", "ended_at", time.Now().Unix())
    r.metrics.jobsCompleted.WithLabelValues(r.site, job.CallString).Inc()
}
```

### 4. Retry with Exponential Backoff

Failed jobs are re-enqueued with an increasing delay rather than going to the dead-letter queue immediately:

```go
func (r *Runner) handleFailure(ctx context.Context, job *RQJob, err error, output string) {
    job.Retries++
    maxRetries := 3

    if job.Retries >= maxRetries {
        // Move to failed queue (RQ-compatible format)
        r.rdb.LPush(ctx, "rq:queue:failed", job.ID)
        r.rdb.HSet(ctx, "rq:job:"+job.ID, "status", "failed", "exc_info", output)
        r.log.Error("job permanently failed", zap.String("id", job.ID), zap.String("fn", job.CallString))
        return
    }

    delay := time.Duration(math.Pow(2, float64(job.Retries))) * time.Second   // 2s, 4s, 8s
    r.log.Warn("job failed — retrying", zap.String("id", job.ID), zap.Duration("delay", delay), zap.Int("attempt", job.Retries))
    time.AfterFunc(delay, func() {
        r.rdb.LPush(ctx, "rq:queue:default", job.ID)
    })
}
```

### 5. Job Scheduler

Read Frappe's `Scheduled Job Type` DocType from MariaDB and enqueue jobs on their cron schedule without any Frappe Python process running:

```go
type ScheduledTask struct {
    Method    string `db:"method"`
    Frequency string `db:"frequency"`   // "Daily", "Hourly", "Weekly", "Cron"
    CronExpr  string `db:"cron_format"` // used when frequency = "Cron"
}

func (s *Scheduler) Start(ctx context.Context) {
    tasks := s.loadScheduledTasks()
    for _, task := range tasks {
        expr := toCronExpr(task.Frequency, task.CronExpr)
        s.cron.AddFunc(expr, func(t ScheduledTask) func() {
            return func() { s.enqueue(ctx, t.Method) }
        }(task))
    }
    s.cron.Start()
}
```

### 6. Monitoring CLI

```bash
lightning jobs status --site erp.local
# Queue        Pending   Active   Failed   Workers
# high         0         2        0        10
# default      14        8        1        20
# low          3         1        0        5

lightning jobs list --site erp.local --queue default --status failed
lightning jobs retry --site erp.local --job-id <uuid>
lightning jobs retry-all --site erp.local --queue failed
lightning jobs cancel --site erp.local --job-id <uuid>
lightning jobs flush --site erp.local --queue low    # clear all pending
```

---

## Prometheus Metrics

| Metric | Type | Labels |
|--------|------|--------|
| `lightning_jobs_completed_total` | Counter | `site`, `method` |
| `lightning_jobs_failed_total` | Counter | `site`, `method` |
| `lightning_jobs_pending` | Gauge | `site`, `queue` |
| `lightning_job_duration_seconds` | Histogram | `site`, `queue` |
| `lightning_worker_goroutines` | Gauge | `site`, `queue` |

---

## Task Checklist

- [ ] Define `job_runner` config block and structs
- [ ] Implement `jobs/consumer.go` — RQ-compatible `BLPOP` reader
- [ ] Implement `jobs/pool.go` — goroutine worker pool with per-queue semaphore
- [ ] Implement `jobs/executor.go` — `bench execute` subprocess runner with timeout
- [ ] Implement `jobs/retry.go` — exponential backoff re-enqueue
- [ ] Implement `jobs/scheduler.go` — cron-based scheduled task enqueuer
- [ ] Implement `jobs/metrics.go` — Prometheus counters and histograms
- [ ] Add `lightning jobs` subcommands (`status`, `list`, `retry`, `retry-all`, `cancel`, `flush`)
- [ ] Write unit tests for retry backoff logic and queue priority ordering
- [ ] Integration test: enqueue an RQ job from Python → Go runner picks up and executes
- [ ] Test: 1000 concurrent jobs complete without goroutine leak
- [ ] Test: failed job retries 3 times then moves to `rq:queue:failed`
- [ ] Test: scheduled task fires at correct cron time

---

## Validation Checklist

- [ ] `bench execute` job enqueued via Python runs correctly via Go worker
- [ ] High-priority queue drains before default queue under load
- [ ] Failed job appears in `lightning jobs list --status failed`
- [ ] `lightning jobs retry-all` re-processes all failed jobs
- [ ] Scheduled task enqueued at correct cron interval
- [ ] Memory footprint: Go runner uses <50MB at 100 concurrent jobs vs ~150MB per Python RQ worker process
- [ ] Prometheus `lightning_jobs_pending` gauge reflects live Redis queue depth
