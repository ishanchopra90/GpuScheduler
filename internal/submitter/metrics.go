package submitter

import (
	"github.com/prometheus/client_golang/prometheus"
)

const submitterNamespace = "gpu_scheduler_submitter"

var (
	// MessagesConsumedTotal is the number of Kafka messages successfully consumed and committed.
	// Use rate() for messages per second (§2.2).
	MessagesConsumedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: submitterNamespace,
		Name:      "messages_consumed_total",
		Help:      "Total number of Kafka workload messages successfully consumed and committed",
	})

	// WorkloadCreationsTotal is the number of GPUWorkload CRs created (excludes AlreadyExists).
	// Use rate() for workload creation rate (§2.2).
	WorkloadCreationsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: submitterNamespace,
		Name:      "workload_creations_total",
		Help:      "Total number of GPUWorkload CRs created (idempotent create; excludes AlreadyExists)",
	})
)

// RegisterMetrics registers submitter Prometheus metrics with the default registry.
// Call once from the submitter binary before exposing /metrics.
// The default registry already includes process and Go collectors (memory/CPU) when prometheus is imported.
//
// Consumer group lag for scaling: use Strimzi/Kafka metrics (or Kafka Exporter) and
// KEDA ScaledObject with the kafka trigger (broker) or prometheus trigger (lag metric).
func RegisterMetrics() {
	prometheus.MustRegister(MessagesConsumedTotal, WorkloadCreationsTotal)
}
