package worker

import (
	"github.com/prometheus/client_golang/prometheus"
)

const metricsNamespace = "gpu_scheduler_worker"

var (
	WorkloadsClaimedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "workloads_claimed_total",
		Help:      "Total number of GPUWorkloads successfully claimed by this worker",
	}, []string{"worker_id"})

	RunsStartedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "runs_started_total",
		Help:      "Total number of simulator runs started by this worker",
	}, []string{"worker_id"})

	RunsCompletedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "runs_completed_total",
		Help:      "Total number of simulator runs completed by this worker",
	}, []string{"worker_id", "outcome"})

	RunDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Name:      "run_duration_seconds",
		Help:      "Time from Start() success to run completion (Succeeded/Failed/Preempted)",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 16),
	}, []string{"worker_id", "outcome"})

	ErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "errors_total",
		Help:      "Total number of worker errors (allocate, start, or claim failures)",
	}, []string{"worker_id", "reason"})
)

// RegisterMetrics registers worker Prometheus metrics with the default registry.
// Call once from the worker binary before exposing /metrics.
func RegisterMetrics() {
	prometheus.MustRegister(
		WorkloadsClaimedTotal,
		RunsStartedTotal,
		RunsCompletedTotal,
		RunDurationSeconds,
		ErrorsTotal,
	)
}

// UnknownWorkerIDLabel is the label value used when worker ID is empty.
const UnknownWorkerIDLabel = "unknown"

// RecordWorkloadClaimed increments workloads_claimed_total for the given worker.
func RecordWorkloadClaimed(workerID string) {
	if workerID == "" {
		workerID = UnknownWorkerIDLabel
	}
	WorkloadsClaimedTotal.WithLabelValues(workerID).Inc()
}

// RecordRunStarted increments runs_started_total for the given worker.
func RecordRunStarted(workerID string) {
	if workerID == "" {
		workerID = UnknownWorkerIDLabel
	}
	RunsStartedTotal.WithLabelValues(workerID).Inc()
}

// RecordRunCompleted records run completion: observes run_duration_seconds and increments runs_completed_total.
// outcome must be "succeeded", "failed", or "preempted".
func RecordRunCompleted(workerID, outcome string, durationSeconds float64) {
	if workerID == "" {
		workerID = UnknownWorkerIDLabel
	}
	if outcome == "" {
		outcome = UnknownWorkerIDLabel
	}
	RunDurationSeconds.WithLabelValues(workerID, outcome).Observe(durationSeconds)
	RunsCompletedTotal.WithLabelValues(workerID, outcome).Inc()
}

// RecordError increments errors_total for the given worker and reason.
// reason should be "allocate_failed", "start_failed", or "claim_failed".
func RecordError(workerID, reason string) {
	if workerID == "" {
		workerID = UnknownWorkerIDLabel
	}
	if reason == "" {
		reason = UnknownWorkerIDLabel
	}
	ErrorsTotal.WithLabelValues(workerID, reason).Inc()
}
