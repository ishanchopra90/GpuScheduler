package scheduler

// NoAdmissionReason describes why no workload was admitted in a scheduling cycle.
// Used for metrics (e.g. quota_denied_total). Empty when a workload was admitted.
const (
	NoAdmissionReasonQuota = "quota"
	NoAdmissionReasonNoFit = "no_fit"
	NoAdmissionReasonEmpty = ""
)

// SelectOneForAdmission runs the shared admission policy on the given inputs
// and returns at most one workload to admit. It uses a two-pass policy:
//  1. First pass: pick the first candidate (in fair order) that fits without
//     preemption, so we avoid preempting when another queued workload can use
//     free capacity.
//  2. Second pass: only if no one fits without preemption, pick the first
//     candidate that can be admitted with preemption.
//
// Caller must have called inputs.Validate() before calling this; otherwise
// the result is undefined. Returns empty selectedID if no candidate can be
// admitted. noAdmissionReason is set when selectedID is empty and there were
// queued candidates: "quota" when all were skipped only due to tenant quota,
// "no_fit" otherwise.
func SelectOneForAdmission(inputs SchedulingInputs) (selectedID string, victims []RunningWorkload, reason, message, noAdmissionReason string) {
	queued := inputs.QueuedWorkloads
	running := inputs.RunningWorkloads
	fleet := inputs.FleetFreeCapacity
	quotas := inputs.TenantQuotas

	ordered := OrderQueued(queued)
	fair := WeightedRoundRobinByTenant(ordered, quotas)
	// Quota counts both scheduled (admitted, not yet running) and running workloads.
	usage := TenantGPUsUsed(append(inputs.ScheduledWorkloads, inputs.RunningWorkloads...))

	reason = "SelectedForAdmission"
	message = "Scheduler selected this workload for admission"

	// First pass: pick the first candidate that fits without preemption.
	for _, candidate := range fair {
		if ExceedsTenantGPUCap(candidate, usage, quotas) {
			continue
		}
		if err := CompatibleProfileAndKind(candidate, fleet); err != nil {
			continue
		}
		fitsCount, err := FitsGPUCount(candidate, fleet)
		if err != nil || !fitsCount {
			continue
		}
		fitsMemory, err := FitsPerDeviceMemory(candidate, fleet)
		if err != nil || !fitsMemory {
			continue
		}
		selectedID = candidate.WorkloadID
		return selectedID, nil, reason, message, NoAdmissionReasonEmpty
	}

	// Second pass: only if no one fits without preemption, pick the first
	// candidate that can be admitted with preemption.
	for _, candidate := range fair {
		if ExceedsTenantGPUCap(candidate, usage, quotas) {
			continue
		}
		if err := CompatibleProfileAndKind(candidate, fleet); err != nil {
			continue
		}
		victims, preemptErr := SelectPreemptionVictimsGreedyMinimal(candidate, running, fleet)
		if preemptErr == nil && len(victims) > 0 {
			selectedID = candidate.WorkloadID
			reason = "AdmittedWithPreemption"
			message = "Scheduler selected this workload for admission with preemption"
			return selectedID, victims, reason, message, NoAdmissionReasonEmpty
		}
	}

	// No one admitted. Determine reason for metrics.
	if len(fair) == 0 {
		return "", nil, "", "", NoAdmissionReasonEmpty
	}
	allSkippedDueToQuota := true
	for _, candidate := range fair {
		if !ExceedsTenantGPUCap(candidate, usage, quotas) {
			allSkippedDueToQuota = false
			break
		}
	}
	if allSkippedDueToQuota {
		return "", nil, "", "", NoAdmissionReasonQuota
	}
	return "", nil, "", "", NoAdmissionReasonNoFit
}
