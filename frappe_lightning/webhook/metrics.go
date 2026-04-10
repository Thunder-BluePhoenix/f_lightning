package webhook

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// WebhookMetrics holds Prometheus instruments for the webhook engine.
type WebhookMetrics struct {
	deliveriesTotal  *prometheus.CounterVec
	deliveryLatency  *prometheus.HistogramVec
	pending          prometheus.Gauge
	dlqDepth         prometheus.Gauge
	retryTotal       *prometheus.CounterVec
}

// NewWebhookMetrics registers and returns metrics for a site.
func NewWebhookMetrics(site string) *WebhookMetrics {
	labels := prometheus.Labels{"site": site}
	return &WebhookMetrics{
		deliveriesTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name:        "lightning_webhook_deliveries_total",
			Help:        "Total webhook delivery attempts.",
			ConstLabels: labels,
		}, []string{"doctype", "event", "status"}),

		deliveryLatency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "lightning_webhook_delivery_latency_seconds",
			Help:        "Webhook delivery latency in seconds.",
			ConstLabels: labels,
			Buckets:     prometheus.DefBuckets,
		}, []string{"endpoint"}),

		pending: promauto.NewGauge(prometheus.GaugeOpts{
			Name:        "lightning_webhook_pending",
			Help:        "Number of events pending delivery.",
			ConstLabels: labels,
		}),

		dlqDepth: promauto.NewGauge(prometheus.GaugeOpts{
			Name:        "lightning_webhook_dlq_depth",
			Help:        "Number of events in the dead-letter queue.",
			ConstLabels: labels,
		}),

		retryTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name:        "lightning_webhook_retry_total",
			Help:        "Total retry attempts by attempt number.",
			ConstLabels: labels,
		}, []string{"attempt_num"}),
	}
}
