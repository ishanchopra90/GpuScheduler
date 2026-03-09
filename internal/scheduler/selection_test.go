package scheduler

import (
	"testing"
	"time"
)

func TestSelectOneForAdmission_PrefersFitWithoutPreemption(t *testing.T) {
	// Two queued: w-small fits in free capacity, w-large would need preemption.
	// Policy should select w-small (first pass), not w-large.
	queued := []QueuedWorkload{
		{
			WorkloadID:   "ns/w-large",
			Tenant:       "team-a",
			Priority:     10,
			QueuedAt:     time.Unix(1700000000, 0),
			GPUCount:     4,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       2000,
			Kind:         WorkloadKindTraining,
		},
		{
			WorkloadID:   "ns/w-small",
			Tenant:       "team-b",
			Priority:     10,
			QueuedAt:     time.Unix(1700000001, 0),
			GPUCount:     1,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       500,
			Kind:         WorkloadKindInference,
		},
	}
	running := []RunningWorkload{
		{
			WorkloadID:   "ns/r-1",
			Tenant:       "team-a",
			Priority:     5,
			StartedAt:    time.Unix(1700000100, 0),
			GPUCount:     3,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       1000,
			Kind:         WorkloadKindTraining,
		},
	}
	fleet := FleetFreeCapacity{
		TotalFreeDevices:   1,
		TotalFreeMemoryMiB: 16000,
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 1, FreeMemoryMiB: 16000, MemoryMiBPerDevice: 16000},
		},
	}
	quotas := map[string]TenantQuota{
		"team-a": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
		"team-b": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
	}
	inputs := SchedulingInputs{
		QueuedWorkloads:   queued,
		RunningWorkloads:  running,
		FleetFreeCapacity: fleet,
		TenantQuotas:      quotas,
	}
	if err := inputs.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	selectedID, victims, reason, _, _ := SelectOneForAdmission(inputs)
	if selectedID != "ns/w-small" {
		t.Errorf("expected selectedID ns/w-small (fits without preemption), got %q", selectedID)
	}
	if len(victims) != 0 {
		t.Errorf("expected no victims, got %d", len(victims))
	}
	if reason != "SelectedForAdmission" {
		t.Errorf("expected reason SelectedForAdmission, got %q", reason)
	}
}

// TestSelectOneForAdmission_SelectsWithPreemptionWhenNoOneFits is the e2e scenario:
// scheduler preempts a running job to admit a higher-priority (or only) queued workload.
func TestSelectOneForAdmission_SelectsWithPreemptionWhenNoOneFits(t *testing.T) {
	// One queued, needs 2 GPUs; fleet has 0 free, one running has 2. Should select with preemption.
	queued := []QueuedWorkload{
		{
			WorkloadID:   "ns/w-1",
			Tenant:       "team-a",
			Priority:     10,
			QueuedAt:     time.Unix(1700000000, 0),
			GPUCount:     2,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       1000,
			Kind:         WorkloadKindTraining,
		},
	}
	running := []RunningWorkload{
		{
			WorkloadID:   "ns/r-1",
			Tenant:       "team-b",
			Priority:     1,
			StartedAt:    time.Unix(1700000100, 0),
			GPUCount:     2,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       500,
			Kind:         WorkloadKindInference,
		},
	}
	fleet := FleetFreeCapacity{
		TotalFreeDevices:   0,
		TotalFreeMemoryMiB: 0,
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 0, FreeMemoryMiB: 0, MemoryMiBPerDevice: 16000},
		},
	}
	quotas := map[string]TenantQuota{
		"team-a": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
		"team-b": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
	}
	inputs := SchedulingInputs{
		QueuedWorkloads:   queued,
		RunningWorkloads:  running,
		FleetFreeCapacity: fleet,
		TenantQuotas:      quotas,
	}
	if err := inputs.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	selectedID, victims, reason, _, _ := SelectOneForAdmission(inputs)
	if selectedID != "ns/w-1" {
		t.Errorf("expected selectedID ns/w-1, got %q", selectedID)
	}
	if len(victims) != 1 || victims[0].WorkloadID != "ns/r-1" {
		t.Errorf("expected one victim ns/r-1, got %v", victims)
	}
	if reason != "AdmittedWithPreemption" {
		t.Errorf("expected reason AdmittedWithPreemption, got %q", reason)
	}
}

// TestSelectOneForAdmission_MixedProfiles verifies that with a mixed-profile fleet,
// admission and fit checks use only the workload's requested profile (29.3).
func TestSelectOneForAdmission_MixedProfiles(t *testing.T) {
	// Fleet: h100_sxm has 2 free, a100_80gb has 1 free. Two queued: one wants h100 (2 GPUs), one wants a100 (1 GPU).
	// Both fit in their respective profiles. Scheduler should pick one that fits (first in fair order).
	queued := []QueuedWorkload{
		{
			WorkloadID:   "ns/w-h100",
			Tenant:       "team-a",
			Priority:     10,
			QueuedAt:     time.Unix(1700000000, 0),
			GPUCount:     2,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       1000,
			Kind:         WorkloadKindTraining,
		},
		{
			WorkloadID:   "ns/w-a100",
			Tenant:       "team-b",
			Priority:     10,
			QueuedAt:     time.Unix(1700000001, 0),
			GPUCount:     1,
			GPUMemoryMiB: 16000,
			Profile:      "a100_80gb",
			Tokens:       500,
			Kind:         WorkloadKindInference,
		},
	}
	fleet := FleetFreeCapacity{
		TotalFreeDevices:   3,
		TotalFreeMemoryMiB: 2*16000 + 1*16000,
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm":  {FreeDevices: 2, FreeMemoryMiB: 2 * 16000, MemoryMiBPerDevice: 16000},
			"a100_80gb": {FreeDevices: 1, FreeMemoryMiB: 1 * 16000, MemoryMiBPerDevice: 16000},
		},
	}
	quotas := map[string]TenantQuota{
		"team-a": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
		"team-b": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
	}
	inputs := SchedulingInputs{
		QueuedWorkloads:   queued,
		RunningWorkloads:  nil,
		FleetFreeCapacity: fleet,
		TenantQuotas:      quotas,
	}
	if err := inputs.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	selectedID, victims, _, _, _ := SelectOneForAdmission(inputs)
	if selectedID == "" {
		t.Fatal("expected one workload to be selected (both fit in their profiles)")
	}
	if len(victims) != 0 {
		t.Errorf("expected no victims with mixed-profile fit, got %d", len(victims))
	}
	// Selected workload must be one that fits in its profile only (w-h100 fits in h100_sxm, w-a100 in a100_80gb).
	if selectedID != "ns/w-h100" && selectedID != "ns/w-a100" {
		t.Errorf("expected selectedID ns/w-h100 or ns/w-a100, got %q", selectedID)
	}
}

// TestSelectOneForAdmission_PicksByFairnessPolicy is the e2e scenario: multiple workloads
// queued, scheduler picks one based on fairness (weighted round-robin by tenant).
func TestSelectOneForAdmission_PicksByFairnessPolicy(t *testing.T) {
	// Two tenants: A (weight 2) and B (weight 1). One free GPU. Both have one queued workload that fits.
	// OrderQueued sorts by priority then QueuedAt; same priority so order is w-a then w-b.
	// WeightedRoundRobinByTenant with A first in tenant order gives fair order [w-a, w-b]. First that fits is w-a.
	queued := []QueuedWorkload{
		{
			WorkloadID:   "ns/w-a",
			Tenant:       "tenant-a",
			Priority:     5,
			QueuedAt:     time.Unix(1700000000, 0),
			GPUCount:     1,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       100,
			Kind:         WorkloadKindTraining,
		},
		{
			WorkloadID:   "ns/w-b",
			Tenant:       "tenant-b",
			Priority:     5,
			QueuedAt:     time.Unix(1700000001, 0),
			GPUCount:     1,
			GPUMemoryMiB: 16000,
			Profile:      "h100_sxm",
			Tokens:       100,
			Kind:         WorkloadKindTraining,
		},
	}
	fleet := FleetFreeCapacity{
		TotalFreeDevices:   1,
		TotalFreeMemoryMiB: 16000,
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 1, FreeMemoryMiB: 16000, MemoryMiBPerDevice: 16000},
		},
	}
	quotas := map[string]TenantQuota{
		"tenant-a": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 2},
		"tenant-b": {MaxGPUs: 8, MaxMemoryMiB: 0, Weight: 1},
	}
	inputs := SchedulingInputs{
		QueuedWorkloads:   queued,
		RunningWorkloads:  nil,
		FleetFreeCapacity: fleet,
		TenantQuotas:      quotas,
	}
	if err := inputs.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	selectedID, victims, _, _, _ := SelectOneForAdmission(inputs)
	if selectedID == "" {
		t.Fatal("expected one workload to be selected")
	}
	if len(victims) != 0 {
		t.Errorf("expected no victims, got %d", len(victims))
	}
	if selectedID != "ns/w-a" {
		t.Errorf("expected selectedID ns/w-a (higher-weight tenant first in fair order), got %q", selectedID)
	}
}
