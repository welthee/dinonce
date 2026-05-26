package psql

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Custom Prometheus metrics covering the parts of the request path that the
// generic HTTP metrics do not see — specifically optimistic-lock retry
// behaviour, which is the most informative signal of contention pressure on
// the lineage row. Histograms use the default request-latency buckets in
// seconds since lease operations are normally well under 100ms but can blow
// out under heavy contention.

var (
	optimisticLockRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "dinonce",
			Subsystem: "ticket",
			Name:      "optimistic_lock_retries_total",
			Help:      "Number of times an optimistic-lock-driven retry was performed, by ticket operation.",
		},
		[]string{"op"},
	)

	optimisticLockGiveUps = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "dinonce",
			Subsystem: "ticket",
			Name:      "optimistic_lock_giveups_total",
			Help:      "Number of operations that exhausted the optimistic-lock retry budget and returned ErrTooManyConcurrentRequests.",
		},
		[]string{"op"},
	)

	operationLatencySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "dinonce",
			Subsystem: "ticket",
			Name:      "operation_latency_seconds",
			Help:      "End-to-end latency of ticket operations (lease/release/close) including all internal retries.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"op", "outcome"},
	)
)
