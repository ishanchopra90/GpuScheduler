package scheduler

import (
	"fmt"
	"sort"
)

type preemptionContext struct {
	profile    string
	needToFree int
	candidates []RunningWorkload
}

func buildPreemptionContext(incoming QueuedWorkload, running []RunningWorkload, fleet FleetFreeCapacity) (*preemptionContext, bool, error) {
	if err := CompatibleProfileAndKind(incoming, fleet); err != nil {
		return nil, false, err
	}
	if ok, err := FitsGPUCount(incoming, fleet); err != nil {
		return nil, false, err
	} else if ok {
		return nil, true, nil
	}
	if ok, err := FitsPerDeviceMemory(incoming, fleet); err != nil {
		return nil, false, err
	} else if !ok {
		// Cannot be made to fit by freeing GPUs.
		return nil, false, fmt.Errorf("incoming workload requires %d MiB per device but profile %q capacity is lower", incoming.GPUMemoryMiB, incoming.Profile)
	}

	free := 0
	if cap, ok := fleet.ByProfile[incoming.Profile]; ok {
		free = cap.FreeDevices
	}
	need := incoming.GPUCount - free
	if need <= 0 {
		return nil, true, nil
	}

	candidates := make([]RunningWorkload, 0, len(running))
	for _, r := range running {
		if r.Profile != incoming.Profile {
			continue
		}
		candidates = append(candidates, r)
	}
	// Shared deterministic ordering (both strategies rely on this base sort).
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		// More recently started first.
		return candidates[i].StartedAt.After(candidates[j].StartedAt)
	})

	return &preemptionContext{
		profile:    incoming.Profile,
		needToFree: need,
		candidates: candidates,
	}, false, nil
}

// SelectPreemptionVictims returns running workloads to preempt so that an incoming
// workload can fit.
//
// Policy:
//   - If the incoming workload already fits, returns no victims.
//   - Otherwise, consider only running workloads on the same hardware profile as
//     the incoming workload (freeing other profiles does not help).
//   - Choose victims by lowest Priority first. For deterministic tie-breaking
//     within the same priority, preempt the most recently started workload first
//     (minimizes wasted runtime in many systems).
//
// This does not attempt to find a minimal preemption set.
func SelectPreemptionVictims(incoming QueuedWorkload, running []RunningWorkload, fleet FleetFreeCapacity) ([]RunningWorkload, error) {
	ctx, fits, err := buildPreemptionContext(incoming, running, fleet)
	if err != nil {
		return nil, err
	}
	if fits {
		return nil, nil
	}

	victims := make([]RunningWorkload, 0)
	freed := 0
	for _, r := range ctx.candidates {
		victims = append(victims, r)
		freed += r.GPUCount
		if freed >= ctx.needToFree {
			return victims, nil
		}
	}

	return nil, fmt.Errorf(
		"insufficient preemptable capacity for profile %q: need to free %d GPUs but only %d GPUs are running on that profile",
		ctx.profile,
		ctx.needToFree,
		freed,
	)
}

// SelectPreemptionVictimsGreedyMinimal returns a greedy minimal victim set.
//
// Policy:
//   - Maintain the constraint that we only preempt higher priority workloads
//     if lower priorities cannot free enough capacity.
//   - Within the lowest possible priority band that can satisfy the incoming GPU
//     shortfall, greedily pick victims that free the most GPUs first. This tends
//     to minimize the number of preemptions while staying simple/deterministic.
//
// Deterministic tie-breaking when GPUCount is equal:
// - lower Priority first (same as SelectPreemptionVictims)
// - more recently started first
func SelectPreemptionVictimsGreedyMinimal(incoming QueuedWorkload, running []RunningWorkload, fleet FleetFreeCapacity) ([]RunningWorkload, error) {
	ctx, fits, err := buildPreemptionContext(incoming, running, fleet)
	if err != nil {
		return nil, err
	}
	if fits {
		return nil, nil
	}

	if len(ctx.candidates) == 0 {
		return nil, fmt.Errorf(
			"insufficient preemptable capacity for profile %q: need to free %d GPUs but no workloads are running on that profile",
			ctx.profile,
			ctx.needToFree,
		)
	}

	pool := make([]RunningWorkload, 0, len(ctx.candidates))
	for idx := 0; idx < len(ctx.candidates); {
		p := ctx.candidates[idx].Priority
		for idx < len(ctx.candidates) && ctx.candidates[idx].Priority == p {
			pool = append(pool, ctx.candidates[idx])
			idx++
		}
		// Try to satisfy need using victims from priorities <= p.
		victims := greedyPickVictimsByGPUCount(pool, ctx.needToFree)
		if victims != nil {
			return victims, nil
		}
	}

	total := 0
	for _, r := range ctx.candidates {
		total += r.GPUCount
	}
	return nil, fmt.Errorf(
		"insufficient preemptable capacity for profile %q: need to free %d GPUs but only %d GPUs are running on that profile",
		ctx.profile,
		ctx.needToFree,
		total,
	)
}

func greedyPickVictimsByGPUCount(candidates []RunningWorkload, need int) []RunningWorkload {
	if need <= 0 {
		return nil
	}
	// Greedy: pick largest GPUCount first to minimize number of victims.
	// Tie-break by more recently started first for determinism.
	sorted := append([]RunningWorkload(nil), candidates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].GPUCount != sorted[j].GPUCount {
			return sorted[i].GPUCount > sorted[j].GPUCount
		}
		return sorted[i].StartedAt.After(sorted[j].StartedAt)
	})

	victims := make([]RunningWorkload, 0)
	freed := 0
	for _, r := range sorted {
		victims = append(victims, r)
		freed += r.GPUCount
		if freed >= need {
			return victims
		}
	}
	return nil
}
