package jobs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds Prometheus instruments for the job runner.
type Metrics struct {
	jobsCompleted *prometheus.CounterVec
	jobsFailed    *prometheus.CounterVec
	queueDepth    *prometheus.GaugeVec
	jobDuration   *prometheus.HistogramVec
	goroutines    *prometheus.GaugeVec
}

// NewMetrics registers and returns Metrics for a site.
// Uses promauto so they are registered on the default Prometheus registry.
func NewMetrics(site string) *Metrics {
	labels := prometheus.Labels{"site": site}

	return &Metrics{
		jobsCompleted: promauto.NewCounterVec(prometheus.CounterOpts{
			Name:        "lightning_jobs_completed_total",
			Help:        "Total jobs completed successfully.",
			ConstLabels: labels,
		}, []string{"method"}),

		jobsFailed: promauto.NewCounterVec(prometheus.CounterOpts{
			Name:        "lightning_jobs_failed_total",
			Help:        "Total jobs that failed after all retries.",
			ConstLabels: labels,
		}, []string{"method"}),

		queueDepth: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "lightning_jobs_pending",
			Help:        "Current number of pending jobs per queue.",
			ConstLabels: labels,
		}, []string{"queue"}),

		jobDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "lightning_job_duration_seconds",
			Help:        "Job execution duration in seconds.",
			ConstLabels: labels,
			Buckets:     prometheus.DefBuckets,
		}, []string{"queue"}),

		goroutines: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "lightning_worker_goroutines",
			Help:        "Number of active worker goroutines per queue.",
			ConstLabels: labels,
		}, []string{"queue"}),
	}
}
