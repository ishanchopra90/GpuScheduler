package scheduler

// TenantGPUsUsed returns the total running GPU usage per tenant.
func TenantGPUsUsed(running []RunningWorkload) map[string]int {
	out := make(map[string]int)
	for _, w := range running {
		out[w.Tenant] += w.GPUCount
	}
	return out
}

// ExceedsTenantGPUCap reports whether admitting a queued workload would exceed
// the tenant's MaxGPUs hard cap.
//
// Rules:
// - If the tenant has no quota entry, there is no cap.
// - If MaxGPUs is 0, there is no cap.
func ExceedsTenantGPUCap(queued QueuedWorkload, runningUsage map[string]int, quotas map[string]TenantQuota) bool {
	q, ok := quotas[queued.Tenant]
	if !ok || q.MaxGPUs == 0 {
		return false
	}
	used := runningUsage[queued.Tenant]
	return used+queued.GPUCount > q.MaxGPUs
}
