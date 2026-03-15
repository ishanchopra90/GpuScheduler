package controller

import (
	"strings"
	"time"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/scheduler"
)

// MapWorkloadKind maps API WorkloadKind to scheduler WorkloadKind.
func MapWorkloadKind(kind schedulerv1alpha1.WorkloadKind) scheduler.WorkloadKind {
	switch kind {
	case schedulerv1alpha1.WorkloadKindTraining:
		return scheduler.WorkloadKindTraining
	case schedulerv1alpha1.WorkloadKindInference:
		return scheduler.WorkloadKindInference
	case schedulerv1alpha1.WorkloadKindEval:
		return scheduler.WorkloadKindEval
	case schedulerv1alpha1.WorkloadKindFineTune:
		return scheduler.WorkloadKindFineTune
	case schedulerv1alpha1.WorkloadKindRLHF:
		return scheduler.WorkloadKindRLHF
	case schedulerv1alpha1.WorkloadKindEmbedding:
		return scheduler.WorkloadKindEmbedding
	case schedulerv1alpha1.WorkloadKindDataPreprocess:
		return scheduler.WorkloadKindDataPreprocess
	case schedulerv1alpha1.WorkloadKindDistillation:
		return scheduler.WorkloadKindDistillation
	default:
		return scheduler.WorkloadKind(kind)
	}
}

// GPUWorkloadToQueuedWorkload converts a GPUWorkload CR (in Queued phase) into a scheduler QueuedWorkload.
// workloadID is typically namespace/name; queuedAt is taken from status.queuedAt or time.Now().
func GPUWorkloadToQueuedWorkload(w *schedulerv1alpha1.GPUWorkload, workloadID string, queuedAt time.Time) scheduler.QueuedWorkload {
	return scheduler.QueuedWorkload{
		WorkloadID:   workloadID,
		Tenant:       w.Spec.Tenant,
		Priority:     w.Spec.Priority,
		QueuedAt:     queuedAt,
		GPUCount:     int(w.Spec.GPUCount),
		GPUMemoryMiB: int(w.Spec.GPUMemoryMiB),
		Profile:      strings.ToLower(strings.TrimSpace(w.Spec.Profile)),
		Tokens:       w.Spec.Tokens,
		Kind:         MapWorkloadKind(w.Spec.Kind),
	}
}

// GPUWorkloadToRunningWorkload converts a GPUWorkload CR (Scheduled or Running) into a scheduler RunningWorkload.
// workloadID is typically namespace/name; startedAt is when the workload was admitted or started.
func GPUWorkloadToRunningWorkload(w *schedulerv1alpha1.GPUWorkload, workloadID string, startedAt time.Time) scheduler.RunningWorkload {
	return scheduler.RunningWorkload{
		WorkloadID:   workloadID,
		Tenant:       w.Spec.Tenant,
		Priority:     w.Spec.Priority,
		StartedAt:    startedAt,
		GPUCount:     int(w.Spec.GPUCount),
		GPUMemoryMiB: int(w.Spec.GPUMemoryMiB),
		Profile:      strings.ToLower(strings.TrimSpace(w.Spec.Profile)),
		Tokens:       w.Spec.Tokens,
		Kind:         MapWorkloadKind(w.Spec.Kind),
	}
}
