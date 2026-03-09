package worker

import "time"

// AnnotationClaimedBy is set on a GPUWorkload when a deployment worker claims it.
// The value is the worker identity (e.g. pod name) to avoid duplicate execution.
const AnnotationClaimedBy = "scheduler.ishanchopra.dev/claimed-by"

// Env vars for the scale-focused worker Deployment (deployment loop mode).
const (
	EnvDeploymentNamespace     = "NAMESPACE"
	EnvWorkerID                = "WORKER_ID"
	EnvLogCompletionTimestamps = "LOG_COMPLETION_TIMESTAMPS" // when "true" or "1", log completion timestamps for demo/validation
)

// DefaultMaxConcurrentWorkloads is the concurrency model for deployment workers: one workload
// at a time per pod. Scale horizontally by adding pods rather than running multiple workloads
// per pod.
const DefaultMaxConcurrentWorkloads = 1

// AllocateStartFailureBackoff is how long to wait after Allocate or Start fails (e.g. simulator
// at capacity) before claiming another workload. Reduces thundering herd when workers are overscaled.
var AllocateStartFailureBackoff = 10 * time.Second
