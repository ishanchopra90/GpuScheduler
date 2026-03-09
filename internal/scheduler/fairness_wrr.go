package scheduler

// WeightedRoundRobinByTenant returns queued workloads interleaved by tenant using
// a simple weighted round-robin algorithm.
//
// This is intended as a fairness layer: it assumes the input slice is already
// ordered by global preference (e.g., priority desc, queuedAt asc) and then
// spreads admissions across tenants.
//
// Rules:
//   - Workloads preserve FIFO order within each tenant (relative to the input slice).
//   - Tenant weight comes from TenantQuota.Weight; missing or non-positive weight
//     defaults to 1.
//
// Example:
// - Quotas: a.weight=2, b.weight=1
// - Input (already globally ordered): [a1, a2, a3, b1, b2]
// - Output: [a1, a2, b1, a3, b2]
func WeightedRoundRobinByTenant(queued []QueuedWorkload, quotas map[string]TenantQuota) []QueuedWorkload {
	if len(queued) == 0 {
		return nil
	}

	type tenantQueue struct {
		tenant string
		items  []QueuedWorkload
		weight int
	}

	byTenant := make(map[string]*tenantQueue)
	tenantsInOrder := make([]*tenantQueue, 0)

	for _, workload := range queued {
		tq, ok := byTenant[workload.Tenant]
		if !ok {
			weight := 1
			if quota, ok := quotas[workload.Tenant]; ok && quota.Weight > 0 {
				weight = quota.Weight
			}
			tq = &tenantQueue{tenant: workload.Tenant, weight: weight}
			byTenant[workload.Tenant] = tq
			tenantsInOrder = append(tenantsInOrder, tq)
		}
		tq.items = append(tq.items, workload)
	}

	out := make([]QueuedWorkload, 0, len(queued))
	remaining := len(queued)
	for remaining > 0 {
		progress := false
		for _, tq := range tenantsInOrder {
			if len(tq.items) == 0 {
				continue
			}
			take := tq.weight
			if take <= 0 {
				take = 1
			}
			if take > len(tq.items) {
				take = len(tq.items)
			}
			out = append(out, tq.items[:take]...)
			tq.items = tq.items[take:]
			remaining -= take
			progress = true
		}
		if !progress {
			break
		}
	}
	return out
}
