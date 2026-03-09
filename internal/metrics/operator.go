package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	namespace = "gpu_scheduler"
)

var (
	// QueuedWorkloadsGauge is the number of GPUWorkload CRs in Phase=Queued.
	QueuedWorkloadsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "queued_workloads",
		Help:      "Number of GPUWorkload CRs in Phase=Queued",
	})

	// QueueWaitTimeHistogram is the time from Queued to Scheduled (admission latency).
	QueueWaitTimeHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "queue_wait_seconds",
		Help:      "Time from workload Queued to Scheduled (admission latency) in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 14),
	})

	// UtilizationRatioGauge is allocated devices / total devices across all pools (0..1).
	UtilizationRatioGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "utilization_ratio",
		Help:      "Ratio of allocated devices to total devices across all GPUNodePools (0 to 1)",
	})

	// PreemptionsCounter is the total number of workloads preempted.
	PreemptionsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "preemptions_total",
		Help:      "Total number of workloads preempted to make capacity",
	})

	// ScheduleDecisionsCounter counts schedule decisions by result: admitted, no_candidate,
	// error_preempt (preemption of victims failed), error_patch (status patch failed).
	ScheduleDecisionsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "schedule_decisions_total",
		Help:      "Total schedule decisions by result (admitted, no_candidate, error_preempt, error_patch)",
	}, []string{"result"})

	// ScheduleDecisionsByKindCounter counts admissions by workload kind.
	ScheduleDecisionsByKindCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "schedule_decisions_by_kind_total",
		Help:      "Total admissions by workload kind (training, inference, etc.)",
	}, []string{"kind"})

	// WorkloadCompletionsCounter counts workloads that reached Phase=Succeeded.
	WorkloadCompletionsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "workload_completions_total",
		Help:      "Total number of GPUWorkloads that completed successfully (Phase=Succeeded)",
	})

	// E2ELatencySeconds is time from CR creation (submit) to Phase=Succeeded (§2.1).
	E2ELatencySeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "e2e_latency_seconds",
		Help:      "Time from GPUWorkload creation (submit) to Phase=Succeeded in seconds",
		Buckets:   prometheus.ExponentialBuckets(1, 2, 16),
	})

	// QueuedByTenantGauge is the number of queued workloads per tenant.
	QueuedByTenantGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "queued_workloads_by_tenant",
		Help:      "Number of GPUWorkloads in Phase=Queued per tenant",
	}, []string{"tenant"})

	// AdmissionsByTenantCounter counts admissions per tenant.
	AdmissionsByTenantCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "admissions_by_tenant_total",
		Help:      "Total number of workloads admitted per tenant",
	}, []string{"tenant"})

	// QuotaDeniedCounter is incremented when no workload is admitted due to tenant quota.
	QuotaDeniedCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "quota_denied_total",
		Help:      "Total number of scheduling cycles where no workload was admitted due to tenant quota",
	})

	// AdmissionsPerCycleHistogram is the number of workloads admitted per scheduling cycle.
	AdmissionsPerCycleHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "admissions_per_cycle",
		Help:      "Number of workloads admitted in one scheduling cycle",
		Buckets:   []float64{0, 1, 2, 3, 5, 10, 20, 50},
	})

	// SchedulingCyclesCounter is the total number of scheduling cycles run.
	SchedulingCyclesCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scheduling_cycles_total",
		Help:      "Total number of scheduling cycles executed",
	})

	// StatusConflict409Counter is the number of status update conflicts (409) encountered.
	StatusConflict409Counter = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "status_conflict_409_total",
		Help:      "Total number of status update conflicts (HTTP 409) when patching workloads",
	})

	// EventsEmittedTotal counts Kubernetes Events emitted by the operator (§2.3).
	// Use rate() for events per second. Label reason matches Event reason (Queued, Admitted, etc.).
	EventsEmittedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "events_emitted_total",
		Help:      "Total Kubernetes Events emitted by the operator by reason",
	}, []string{"reason"})
)

func init() {
	crmetrics.Registry.MustRegister(
		QueuedWorkloadsGauge,
		QueueWaitTimeHistogram,
		UtilizationRatioGauge,
		PreemptionsCounter,
		ScheduleDecisionsCounter,
		ScheduleDecisionsByKindCounter,
		WorkloadCompletionsCounter,
		E2ELatencySeconds,
		QueuedByTenantGauge,
		AdmissionsByTenantCounter,
		QuotaDeniedCounter,
		AdmissionsPerCycleHistogram,
		SchedulingCyclesCounter,
		StatusConflict409Counter,
		EventsEmittedTotal,
	)
}
