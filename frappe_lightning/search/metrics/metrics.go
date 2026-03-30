package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// SearchDuration tracks the latency of search requests in seconds.
	SearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lightning_search_duration_seconds",
		Help:    "Latency of search requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"site"})

	// BinlogLag tracks the replication lag from MariaDB in seconds.
	BinlogLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lightning_binlog_lag_seconds",
		Help: "Replication lag from MariaDB in seconds.",
	}, []string{"site"})

	// IndexingTotal tracks total documents processed.
	IndexingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lightning_indexing_total",
		Help: "Total number of documents indexed successfully.",
	}, []string{"site", "doctype", "action"})

	// IndexingErrors tracks indexing failures.
	IndexingErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lightning_indexing_errors_total",
		Help: "Total number of document indexing failures.",
	}, []string{"site", "doctype"})
)
