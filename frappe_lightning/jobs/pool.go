package jobs

import (
	"context"

	"frappe_lightning/config"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Runner is the main background job runner for a single Frappe site.
// It drains RQ queues in priority order and dispatches jobs to a goroutine pool.
type Runner struct {
	site      string
	consumer  *Consumer
	executor  *Executor
	retrier   *Retrier
	scheduler *Scheduler
	queues    []config.QueueConfig
	sems      map[string]chan struct{} // per-queue concurrency semaphore
	metrics   *Metrics
	log       *zap.Logger
}

// NewRunner creates a Runner for the given site.
func NewRunner(site string, rdb *redis.Client, cfg config.JobRunnerConfig, log *zap.Logger) *Runner {
	consumer := NewConsumer(rdb, site, log)
	metrics := NewMetrics(site)
	executor := NewExecutor(site, cfg.BenchPath, consumer, metrics, log)
	retrier := NewRetrier(rdb, site, cfg.MaxRetries, log)

	sems := make(map[string]chan struct{}, len(cfg.Queues))
	for _, q := range cfg.Queues {
		c := q.Concurrency
		if c <= 0 {
			c = 10
		}
		sems[q.Name] = make(chan struct{}, c)
	}

	var scheduler *Scheduler
	if cfg.BenchPath != "" {
		scheduler = NewScheduler(site, rdb, cfg.BenchPath, log)
	}

	return &Runner{
		site:      site,
		consumer:  consumer,
		executor:  executor,
		retrier:   retrier,
		scheduler: scheduler,
		queues:    cfg.Queues,
		sems:      sems,
		metrics:   metrics,
		log:       log,
	}
}

// Start begins draining all configured queues. Blocks until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	r.log.Info("job runner started", zap.String("site", r.site))

	// Start the scheduler in the background.
	if r.scheduler != nil {
		go r.scheduler.Start(ctx)
	}

	// Start a Prometheus queue-depth updater goroutine.
	go r.pollQueueDepths(ctx)

	queueNames := r.queueNames()
	for {
		select {
		case <-ctx.Done():
			r.log.Info("job runner stopped", zap.String("site", r.site))
			return
		default:
		}

		job, err := r.consumer.Pop(ctx, queueNames)
		if err != nil {
			r.log.Error("consumer pop error", zap.String("site", r.site), zap.Error(err))
			continue
		}
		if job == nil {
			continue // timeout — loop again
		}

		qName := job.QueueName
		if qName == "" {
			qName = "default"
		}
		sem, ok := r.sems[qName]
		if !ok {
			sem = r.sems["default"]
		}

		// Acquire a concurrency slot (blocks if queue is at capacity).
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}

		go func(j *RQJob, s chan struct{}) {
			defer func() { <-s }()
			r.executor.Execute(ctx, j, r.retrier)
		}(job, sem)
	}
}

// queueNames returns queue names in priority order.
func (r *Runner) queueNames() []string {
	names := make([]string, len(r.queues))
	for i, q := range r.queues {
		names[i] = q.Name
	}
	return names
}

// pollQueueDepths updates the Prometheus queue depth gauge every 5 seconds.
func (r *Runner) pollQueueDepths(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		for _, q := range r.queues {
			depth := r.consumer.QueueDepth(ctx, q.Name)
			r.metrics.queueDepth.WithLabelValues(q.Name).Set(float64(depth))
		}
		// Sleep without blocking context cancellation.
		select {
		case <-ctx.Done():
			return
		case <-sleepCh(5):
		}
	}
}
