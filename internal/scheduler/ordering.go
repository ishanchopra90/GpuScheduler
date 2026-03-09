package scheduler

import "sort"

// OrderQueued returns queued workloads sorted for admission:
// - priority descending
// - queuedAt ascending (FIFO within same priority)
//
// Stable ordering is used so fully equal keys keep their relative input order.
func OrderQueued(workloads []QueuedWorkload) []QueuedWorkload {
	out := append([]QueuedWorkload(nil), workloads...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if !out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].QueuedAt.Before(out[j].QueuedAt)
		}
		return false
	})
	return out
}
