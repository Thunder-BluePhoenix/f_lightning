package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"go.uber.org/zap"
)

// Executor runs Frappe jobs by shelling out to `bench execute`.
type Executor struct {
	site      string
	benchPath string
	consumer  *Consumer
	metrics   *Metrics
	log       *zap.Logger
}

// NewExecutor creates an Executor for the given site.
// benchPath is the bench root directory, e.g. /home/user/frappe-bench.
func NewExecutor(site, benchPath string, consumer *Consumer, metrics *Metrics, log *zap.Logger) *Executor {
	return &Executor{
		site:      site,
		benchPath: benchPath,
		consumer:  consumer,
		metrics:   metrics,
		log:       log,
	}
}

// Execute runs a single job synchronously. On failure it delegates to the Retrier.
func (e *Executor) Execute(ctx context.Context, job *RQJob, retrier *Retrier) {
	e.consumer.SetStatus(ctx, job.ID, "started", "started_at", fmt.Sprintf("%d", time.Now().Unix()))

	timeout := time.Duration(job.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	output, err := e.runBench(execCtx, job)
	elapsed := time.Since(start).Seconds()

	queue := job.QueueName
	if queue == "" {
		queue = "default"
	}

	if err != nil {
		e.metrics.jobsFailed.WithLabelValues(job.CallString).Inc()
		e.log.Error("job failed",
			zap.String("site", e.site),
			zap.String("id", job.ID),
			zap.String("fn", job.CallString),
			zap.Int("attempt", job.Retries+1),
			zap.Error(err),
		)
		retrier.HandleFailure(ctx, job, err, output)
		return
	}

	e.metrics.jobsCompleted.WithLabelValues(job.CallString).Inc()
	e.metrics.jobDuration.WithLabelValues(queue).Observe(elapsed)
	e.consumer.SetStatus(ctx, job.ID, "finished", "ended_at", fmt.Sprintf("%d", time.Now().Unix()))

	e.log.Info("job finished",
		zap.String("site", e.site),
		zap.String("id", job.ID),
		zap.String("fn", job.CallString),
		zap.Float64("elapsed_s", elapsed),
	)
}

// runBench shells out to bench execute.
func (e *Executor) runBench(ctx context.Context, job *RQJob) (string, error) {
	argsJSON, err := json.Marshal(job.Args)
	if err != nil {
		return "", fmt.Errorf("marshal args: %w", err)
	}
	kwargsJSON, err := json.Marshal(job.Kwargs)
	if err != nil {
		return "", fmt.Errorf("marshal kwargs: %w", err)
	}

	cmd := exec.CommandContext(ctx,
		"bench", "--site", e.site, "execute", job.CallString,
		"--args", string(argsJSON),
		"--kwargs", string(kwargsJSON),
	)
	cmd.Dir = e.benchPath

	out, err := cmd.CombinedOutput()
	return string(out), err
}
