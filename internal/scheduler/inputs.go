package scheduler

import (
	"fmt"
	"strings"
	"time"
)

// WorkloadKind identifies the runtime class for scheduling/runtime hints.
type WorkloadKind string

const (
	WorkloadKindTraining       WorkloadKind = "training"
	WorkloadKindInference      WorkloadKind = "inference"
	WorkloadKindEval           WorkloadKind = "eval"
	WorkloadKindFineTune       WorkloadKind = "fine_tune"
	WorkloadKindRLHF           WorkloadKind = "rlhf"
	WorkloadKindEmbedding      WorkloadKind = "embedding"
	WorkloadKindDataPreprocess WorkloadKind = "data_preprocess"
	WorkloadKindDistillation   WorkloadKind = "distillation"
)

// QueuedWorkload is the scheduler-facing representation of a workload waiting
// for admission from the queue.
//
// It is intentionally CR-agnostic so callers can map from Kubernetes objects,
// Kafka requests, or tests into one stable scheduler contract.
type QueuedWorkload struct {
	// WorkloadID is a stable unique identifier used by scheduler decisions.
	WorkloadID string
	// Tenant identifies the owning tenant for fairness/quota checks.
	Tenant string
	// Priority is higher-is-more-important for ordering/preemption logic.
	Priority int32
	// QueuedAt is when the workload entered queueing, used for FIFO tiebreaking.
	QueuedAt time.Time

	// GPUCount is requested device count.
	GPUCount int
	// GPUMemoryMiB is per-device memory requirement.
	GPUMemoryMiB int

	// Profile is requested hardware profile (for compatibility/fit policies).
	Profile string
	// Tokens is optional runtime-size hint used by policy extensions.
	Tokens int64
	// Kind identifies which runtime family this workload belongs to.
	Kind WorkloadKind
}

// RunningWorkload is the scheduler-facing representation of a currently running
// workload. It is used by fairness, quota, and preemption logic.
type RunningWorkload struct {
	// WorkloadID is a stable unique identifier used by scheduler decisions.
	WorkloadID string
	// Tenant identifies the owning tenant for fairness/quota checks.
	Tenant string
	// Priority is higher-is-more-important for ordering/preemption logic.
	Priority int32
	// StartedAt is when execution began, useful for victim/tie-break policies.
	StartedAt time.Time

	// GPUCount is allocated device count for this running workload.
	GPUCount int
	// GPUMemoryMiB is per-device memory usage/request for this running workload.
	GPUMemoryMiB int

	// Profile is active hardware profile for this workload.
	Profile string
	// Tokens is runtime-size hint carried with the workload.
	Tokens int64
	// Kind identifies which runtime family this workload belongs to.
	Kind WorkloadKind
}

// ProfileFreeCapacity summarizes currently free capacity for one hardware profile.
type ProfileFreeCapacity struct {
	FreeDevices   int
	FreeMemoryMiB int64
	// MemoryMiBPerDevice is the per-device memory capacity for this profile.
	// When FreeDevices > 0, this should be populated for per-device fit checks.
	MemoryMiBPerDevice int
}

// FleetFreeCapacity is the scheduler-facing snapshot of free simulator capacity.
// It is intentionally aggregated (not per-device) for first-pass policy inputs.
type FleetFreeCapacity struct {
	TotalFreeDevices   int
	TotalFreeMemoryMiB int64
	ByProfile          map[string]ProfileFreeCapacity
}

// TenantQuota carries scheduler-side quota/fairness limits for one tenant.
type TenantQuota struct {
	// MaxGPUs is a hard cap for total GPUs used by the tenant (0 means no cap).
	MaxGPUs int
	// MaxMemoryMiB is a hard cap for total memory used by the tenant (0 means no cap).
	MaxMemoryMiB int64
	// Weight is an optional fairness weight (0 defaults to neutral policy weight).
	Weight int
}

// SchedulingInputs groups scheduler entry-point state.
//
// QueuedWorkloads are waiting for admission. ScheduledWorkloads and
// RunningWorkloads are both counted toward tenant quota (committed + in-use).
// Only RunningWorkloads are considered for preemption victims.
type SchedulingInputs struct {
	QueuedWorkloads    []QueuedWorkload
	ScheduledWorkloads []RunningWorkload // Admitted but not yet running; counted toward quota
	RunningWorkloads   []RunningWorkload
	FleetFreeCapacity  FleetFreeCapacity
	TenantQuotas       map[string]TenantQuota
}

// Validate checks the minimal contract for queued workloads input.
func (in SchedulingInputs) Validate() error {
	seen := make(map[string]struct{}, len(in.QueuedWorkloads)+len(in.ScheduledWorkloads)+len(in.RunningWorkloads))
	for i, w := range in.QueuedWorkloads {
		if strings.TrimSpace(w.WorkloadID) == "" {
			return fmt.Errorf("queuedWorkloads[%d]: workloadID is required", i)
		}
		if _, exists := seen[w.WorkloadID]; exists {
			return fmt.Errorf("queuedWorkloads[%d]: duplicate workloadID %q", i, w.WorkloadID)
		}
		seen[w.WorkloadID] = struct{}{}

		if strings.TrimSpace(w.Tenant) == "" {
			return fmt.Errorf("queuedWorkloads[%d]: tenant is required", i)
		}
		if w.QueuedAt.IsZero() {
			return fmt.Errorf("queuedWorkloads[%d]: queuedAt is required", i)
		}
		if w.GPUCount <= 0 {
			return fmt.Errorf("queuedWorkloads[%d]: gpuCount must be positive, got %d", i, w.GPUCount)
		}
		if w.GPUMemoryMiB <= 0 {
			return fmt.Errorf("queuedWorkloads[%d]: gpuMemoryMiB must be positive, got %d", i, w.GPUMemoryMiB)
		}
		if strings.TrimSpace(w.Profile) == "" {
			return fmt.Errorf("queuedWorkloads[%d]: profile is required", i)
		}
		if w.Tokens <= 0 {
			return fmt.Errorf("queuedWorkloads[%d]: tokens must be positive, got %d", i, w.Tokens)
		}
		switch w.Kind {
		case WorkloadKindTraining, WorkloadKindInference, WorkloadKindEval, WorkloadKindFineTune,
			WorkloadKindRLHF, WorkloadKindEmbedding, WorkloadKindDataPreprocess, WorkloadKindDistillation:
		default:
			return fmt.Errorf("queuedWorkloads[%d]: unsupported kind %q", i, w.Kind)
		}
	}
	for i, w := range in.ScheduledWorkloads {
		if strings.TrimSpace(w.WorkloadID) == "" {
			return fmt.Errorf("scheduledWorkloads[%d]: workloadID is required", i)
		}
		if _, exists := seen[w.WorkloadID]; exists {
			return fmt.Errorf("scheduledWorkloads[%d]: duplicate workloadID %q", i, w.WorkloadID)
		}
		seen[w.WorkloadID] = struct{}{}

		if strings.TrimSpace(w.Tenant) == "" {
			return fmt.Errorf("scheduledWorkloads[%d]: tenant is required", i)
		}
		if w.GPUCount <= 0 {
			return fmt.Errorf("scheduledWorkloads[%d]: gpuCount must be positive, got %d", i, w.GPUCount)
		}
		if w.GPUMemoryMiB <= 0 {
			return fmt.Errorf("scheduledWorkloads[%d]: gpuMemoryMiB must be positive, got %d", i, w.GPUMemoryMiB)
		}
		if strings.TrimSpace(w.Profile) == "" {
			return fmt.Errorf("scheduledWorkloads[%d]: profile is required", i)
		}
		if w.Tokens <= 0 {
			return fmt.Errorf("scheduledWorkloads[%d]: tokens must be positive, got %d", i, w.Tokens)
		}
		switch w.Kind {
		case WorkloadKindTraining, WorkloadKindInference, WorkloadKindEval, WorkloadKindFineTune,
			WorkloadKindRLHF, WorkloadKindEmbedding, WorkloadKindDataPreprocess, WorkloadKindDistillation:
		default:
			return fmt.Errorf("scheduledWorkloads[%d]: unsupported kind %q", i, w.Kind)
		}
	}
	for i, w := range in.RunningWorkloads {
		if strings.TrimSpace(w.WorkloadID) == "" {
			return fmt.Errorf("runningWorkloads[%d]: workloadID is required", i)
		}
		if _, exists := seen[w.WorkloadID]; exists {
			return fmt.Errorf("runningWorkloads[%d]: duplicate workloadID %q", i, w.WorkloadID)
		}
		seen[w.WorkloadID] = struct{}{}

		if strings.TrimSpace(w.Tenant) == "" {
			return fmt.Errorf("runningWorkloads[%d]: tenant is required", i)
		}
		if w.StartedAt.IsZero() {
			return fmt.Errorf("runningWorkloads[%d]: startedAt is required", i)
		}
		if w.GPUCount <= 0 {
			return fmt.Errorf("runningWorkloads[%d]: gpuCount must be positive, got %d", i, w.GPUCount)
		}
		if w.GPUMemoryMiB <= 0 {
			return fmt.Errorf("runningWorkloads[%d]: gpuMemoryMiB must be positive, got %d", i, w.GPUMemoryMiB)
		}
		if strings.TrimSpace(w.Profile) == "" {
			return fmt.Errorf("runningWorkloads[%d]: profile is required", i)
		}
		if w.Tokens <= 0 {
			return fmt.Errorf("runningWorkloads[%d]: tokens must be positive, got %d", i, w.Tokens)
		}
		switch w.Kind {
		case WorkloadKindTraining, WorkloadKindInference, WorkloadKindEval, WorkloadKindFineTune,
			WorkloadKindRLHF, WorkloadKindEmbedding, WorkloadKindDataPreprocess, WorkloadKindDistillation:
		default:
			return fmt.Errorf("runningWorkloads[%d]: unsupported kind %q", i, w.Kind)
		}
	}
	if in.FleetFreeCapacity.TotalFreeDevices < 0 {
		return fmt.Errorf("fleetFreeCapacity.totalFreeDevices must be >= 0, got %d", in.FleetFreeCapacity.TotalFreeDevices)
	}
	if in.FleetFreeCapacity.TotalFreeMemoryMiB < 0 {
		return fmt.Errorf("fleetFreeCapacity.totalFreeMemoryMiB must be >= 0, got %d", in.FleetFreeCapacity.TotalFreeMemoryMiB)
	}
	sumDevices := 0
	sumMemory := int64(0)
	for profile, cap := range in.FleetFreeCapacity.ByProfile {
		if strings.TrimSpace(profile) == "" {
			return fmt.Errorf("fleetFreeCapacity.byProfile has empty profile key")
		}
		if cap.FreeDevices < 0 {
			return fmt.Errorf("fleetFreeCapacity.byProfile[%q].freeDevices must be >= 0, got %d", profile, cap.FreeDevices)
		}
		if cap.FreeMemoryMiB < 0 {
			return fmt.Errorf("fleetFreeCapacity.byProfile[%q].freeMemoryMiB must be >= 0, got %d", profile, cap.FreeMemoryMiB)
		}
		if cap.FreeDevices > 0 && cap.MemoryMiBPerDevice <= 0 {
			return fmt.Errorf("fleetFreeCapacity.byProfile[%q].memoryMiBPerDevice must be > 0 when freeDevices > 0, got %d", profile, cap.MemoryMiBPerDevice)
		}
		sumDevices += cap.FreeDevices
		sumMemory += cap.FreeMemoryMiB
	}
	if sumDevices != in.FleetFreeCapacity.TotalFreeDevices {
		return fmt.Errorf(
			"fleetFreeCapacity totals mismatch: totalFreeDevices=%d but byProfile sums to %d",
			in.FleetFreeCapacity.TotalFreeDevices,
			sumDevices,
		)
	}
	if sumMemory != in.FleetFreeCapacity.TotalFreeMemoryMiB {
		return fmt.Errorf(
			"fleetFreeCapacity totals mismatch: totalFreeMemoryMiB=%d but byProfile sums to %d",
			in.FleetFreeCapacity.TotalFreeMemoryMiB,
			sumMemory,
		)
	}
	for tenant, q := range in.TenantQuotas {
		if strings.TrimSpace(tenant) == "" {
			return fmt.Errorf("tenantQuotas has empty tenant key")
		}
		if q.MaxGPUs < 0 {
			return fmt.Errorf("tenantQuotas[%q].maxGPUs must be >= 0, got %d", tenant, q.MaxGPUs)
		}
		if q.MaxMemoryMiB < 0 {
			return fmt.Errorf("tenantQuotas[%q].maxMemoryMiB must be >= 0, got %d", tenant, q.MaxMemoryMiB)
		}
		if q.Weight < 0 {
			return fmt.Errorf("tenantQuotas[%q].weight must be >= 0, got %d", tenant, q.Weight)
		}
	}
	return nil
}

// ApplyVirtualAdmission returns a copy of inputs with the admitted workload
// removed from QueuedWorkloads, added to RunningWorkloads (with StartedAt=now),
// and FleetFreeCapacity reduced for the workload's profile. Used for batching:
// after admitting one workload in a cycle, the next SelectOneForAdmission can
// run on the updated copy so multiple workloads are admitted per cycle when
// capacity allows.
func ApplyVirtualAdmission(inputs SchedulingInputs, admitted QueuedWorkload, now time.Time) (SchedulingInputs, error) {
	out := SchedulingInputs{
		QueuedWorkloads:    make([]QueuedWorkload, 0, len(inputs.QueuedWorkloads)-1),
		ScheduledWorkloads: append([]RunningWorkload(nil), inputs.ScheduledWorkloads...),
		RunningWorkloads:   make([]RunningWorkload, 0, len(inputs.RunningWorkloads)+1),
		TenantQuotas:       inputs.TenantQuotas, // read-only, share
	}
	for _, q := range inputs.QueuedWorkloads {
		if q.WorkloadID != admitted.WorkloadID {
			out.QueuedWorkloads = append(out.QueuedWorkloads, q)
		}
	}
	for _, r := range inputs.RunningWorkloads {
		out.RunningWorkloads = append(out.RunningWorkloads, r)
	}
	out.RunningWorkloads = append(out.RunningWorkloads, RunningWorkload{
		WorkloadID:   admitted.WorkloadID,
		Tenant:       admitted.Tenant,
		Priority:     admitted.Priority,
		StartedAt:    now,
		GPUCount:     admitted.GPUCount,
		GPUMemoryMiB: admitted.GPUMemoryMiB,
		Profile:      admitted.Profile,
		Tokens:       admitted.Tokens,
		Kind:         admitted.Kind,
	})

	out.FleetFreeCapacity.TotalFreeDevices = inputs.FleetFreeCapacity.TotalFreeDevices
	out.FleetFreeCapacity.TotalFreeMemoryMiB = inputs.FleetFreeCapacity.TotalFreeMemoryMiB
	out.FleetFreeCapacity.ByProfile = make(map[string]ProfileFreeCapacity)
	for k, v := range inputs.FleetFreeCapacity.ByProfile {
		out.FleetFreeCapacity.ByProfile[k] = v
	}
	cap, ok := out.FleetFreeCapacity.ByProfile[admitted.Profile]
	if !ok {
		return out, fmt.Errorf("profile %q not in fleet", admitted.Profile)
	}
	cap.FreeDevices -= admitted.GPUCount
	cap.FreeMemoryMiB -= int64(admitted.GPUCount) * int64(cap.MemoryMiBPerDevice)
	if cap.FreeDevices < 0 || cap.FreeMemoryMiB < 0 {
		return out, fmt.Errorf("virtual admission would make capacity negative")
	}
	out.FleetFreeCapacity.ByProfile[admitted.Profile] = cap
	out.FleetFreeCapacity.TotalFreeDevices -= admitted.GPUCount
	out.FleetFreeCapacity.TotalFreeMemoryMiB -= int64(admitted.GPUCount) * int64(cap.MemoryMiBPerDevice)
	return out, nil
}

// QueuedWorkloadByID returns the QueuedWorkload with the given WorkloadID, or nil if not found.
func QueuedWorkloadByID(inputs SchedulingInputs, workloadID string) *QueuedWorkload {
	for i := range inputs.QueuedWorkloads {
		if inputs.QueuedWorkloads[i].WorkloadID == workloadID {
			return &inputs.QueuedWorkloads[i]
		}
	}
	return nil
}
