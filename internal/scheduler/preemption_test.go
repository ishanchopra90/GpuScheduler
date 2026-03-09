package scheduler

import (
	"reflect"
	"testing"
	"time"
)

func rw(id string, tenant string, priority int32, startedAt int64, gpuCount int, profile string) RunningWorkload {
	return RunningWorkload{
		WorkloadID:   id,
		Tenant:       tenant,
		Priority:     priority,
		StartedAt:    time.Unix(startedAt, 0),
		GPUCount:     gpuCount,
		GPUMemoryMiB: 1000,
		Profile:      profile,
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}
}

func TestSelectPreemptionVictims_AlreadyFitsReturnsNone(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 4, FreeMemoryMiB: 64000, MemoryMiBPerDevice: 16000},
		},
	}
	incoming := QueuedWorkload{
		WorkloadID:   "w",
		Tenant:       "t",
		Priority:     100,
		QueuedAt:     time.Unix(1, 0),
		GPUCount:     2,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindInference,
	}
	victims, err := SelectPreemptionVictims(incoming, nil, fleet)
	if err != nil {
		t.Fatalf("SelectPreemptionVictims() error = %v", err)
	}
	if len(victims) != 0 {
		t.Fatalf("expected no victims, got %v", victims)
	}
}

func TestSelectPreemptionVictims_PicksLowestPriorityFirst(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 1, FreeMemoryMiB: 16000, MemoryMiBPerDevice: 16000},
		},
	}
	incoming := QueuedWorkload{
		WorkloadID:   "w",
		Tenant:       "t",
		Priority:     100,
		QueuedAt:     time.Unix(1, 0),
		GPUCount:     3,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}
	running := []RunningWorkload{
		rw("r1", "a", 5, 100, 1, "h100_sxm"),
		rw("r2", "a", 1, 200, 1, "h100_sxm"),
		rw("r3", "a", 3, 300, 1, "h100_sxm"),
	}
	victims, err := SelectPreemptionVictims(incoming, running, fleet)
	if err != nil {
		t.Fatalf("SelectPreemptionVictims() error = %v", err)
	}
	gotIDs := []string{victims[0].WorkloadID, victims[1].WorkloadID}
	wantIDs := []string{"r2", "r3"} // lowest priority first
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("victim order mismatch:\n got: %v\nwant: %v", gotIDs, wantIDs)
	}
}

func TestSelectPreemptionVictims_TieBreaksByMostRecentStart(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 0, FreeMemoryMiB: 0, MemoryMiBPerDevice: 16000},
		},
	}
	incoming := QueuedWorkload{
		WorkloadID:   "w",
		Tenant:       "t",
		Priority:     100,
		QueuedAt:     time.Unix(1, 0),
		GPUCount:     1,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}
	running := []RunningWorkload{
		rw("old", "a", 1, 100, 1, "h100_sxm"),
		rw("new", "a", 1, 200, 1, "h100_sxm"),
	}
	victims, err := SelectPreemptionVictims(incoming, running, fleet)
	if err != nil {
		t.Fatalf("SelectPreemptionVictims() error = %v", err)
	}
	if len(victims) != 1 || victims[0].WorkloadID != "new" {
		t.Fatalf("expected to preempt most recent start, got %v", victims)
	}
}

func TestSelectPreemptionVictims_IgnoresOtherProfiles(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm":  {FreeDevices: 0, FreeMemoryMiB: 0, MemoryMiBPerDevice: 16000},
			"a100_80gb": {FreeDevices: 0, FreeMemoryMiB: 0, MemoryMiBPerDevice: 16000},
		},
	}
	incoming := QueuedWorkload{
		WorkloadID:   "w",
		Tenant:       "t",
		Priority:     100,
		QueuedAt:     time.Unix(1, 0),
		GPUCount:     1,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}
	running := []RunningWorkload{
		rw("other", "a", 1, 100, 10, "a100_80gb"),
	}
	_, err := SelectPreemptionVictims(incoming, running, fleet)
	if err == nil {
		t.Fatalf("expected error due to no running workloads on requested profile, got nil")
	}
}

func TestSelectPreemptionVictimsGreedyMinimal_PicksFewerVictimsWhenPossible(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 0, FreeMemoryMiB: 0, MemoryMiBPerDevice: 16000},
		},
	}
	incoming := QueuedWorkload{
		WorkloadID:   "w",
		Tenant:       "t",
		Priority:     100,
		QueuedAt:     time.Unix(1, 0),
		GPUCount:     2,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}

	// Same priority victims; baseline selector tie-breaks by StartedAt and would pick
	// "small" first, then "big". Greedy-minimal should pick "big" only.
	running := []RunningWorkload{
		rw("big", "a", 1, 100, 2, "h100_sxm"),
		rw("small", "a", 1, 200, 1, "h100_sxm"),
	}

	v1, err := SelectPreemptionVictims(incoming, running, fleet)
	if err != nil {
		t.Fatalf("SelectPreemptionVictims() error = %v", err)
	}
	if len(v1) != 2 {
		t.Fatalf("expected 2 victims under baseline selector, got %d (%v)", len(v1), v1)
	}

	v2, err := SelectPreemptionVictimsGreedyMinimal(incoming, running, fleet)
	if err != nil {
		t.Fatalf("SelectPreemptionVictimsGreedyMinimal() error = %v", err)
	}
	if len(v2) != 1 || v2[0].WorkloadID != "big" {
		t.Fatalf("expected greedy-minimal to pick only big, got %v", v2)
	}
}

func TestSelectPreemptionVictimsGreedyMinimal_DoesNotPreemptHigherPriorityIfLowerSuffices(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 0, FreeMemoryMiB: 0, MemoryMiBPerDevice: 16000},
		},
	}
	incoming := QueuedWorkload{
		WorkloadID:   "w",
		Tenant:       "t",
		Priority:     100,
		QueuedAt:     time.Unix(1, 0),
		GPUCount:     2,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}

	running := []RunningWorkload{
		rw("low", "a", 1, 100, 2, "h100_sxm"),
		rw("high", "a", 10, 200, 2, "h100_sxm"),
	}
	victims, err := SelectPreemptionVictimsGreedyMinimal(incoming, running, fleet)
	if err != nil {
		t.Fatalf("SelectPreemptionVictimsGreedyMinimal() error = %v", err)
	}
	if len(victims) != 1 || victims[0].WorkloadID != "low" {
		t.Fatalf("expected to preempt only low priority workload, got %v", victims)
	}
}
