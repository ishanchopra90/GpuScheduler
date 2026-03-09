package sim

import (
	"github.com/prometheus/client_golang/prometheus"
)

const metricsNamespace = "sim"

var (
	DevicesTotalGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "devices_total",
		Help:      "Total number of simulated GPU devices across all registered pools",
	})

	DevicesAllocatedGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "devices_allocated",
		Help:      "Number of simulated GPU devices currently allocated to workloads",
	})

	RunDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "run_duration_seconds",
		Help:      "Simulated run duration in seconds (from Start to Succeeded/Preempted/Failed)",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 16),
	}, []string{"kind"})

	AllocationFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "allocation_failures_total",
		Help:      "Total number of Allocate requests that failed (no capacity, duplicate, invalid, etc.)",
	})
)

func init() {
	prometheus.MustRegister(
		DevicesTotalGauge,
		DevicesAllocatedGauge,
		RunDurationSeconds,
		AllocationFailuresTotal,
	)
}

// RecordRunDuration observes a completed run duration for metrics. kind is the workload kind (e.g. training, inference).
func RecordRunDuration(kind string, durationSeconds float64) {
	if kind == "" {
		kind = "unknown"
	}
	RunDurationSeconds.WithLabelValues(kind).Observe(durationSeconds)
}
