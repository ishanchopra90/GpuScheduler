package controller

import (
	"testing"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/scheduler"
)

func TestAggregateFleetFromPools_Empty(t *testing.T) {
	out := aggregateFleetFromPools(nil)
	if out.TotalFreeDevices != 0 || out.TotalFreeMemoryMiB != 0 || len(out.ByProfile) != 0 {
		t.Errorf("empty pools: got TotalFreeDevices=%d TotalFreeMemoryMiB=%d len(ByProfile)=%d",
			out.TotalFreeDevices, out.TotalFreeMemoryMiB, len(out.ByProfile))
	}
	out = aggregateFleetFromPools([]schedulerv1alpha1.GPUNodePool{})
	if out.TotalFreeDevices != 0 || out.TotalFreeMemoryMiB != 0 || len(out.ByProfile) != 0 {
		t.Errorf("empty slice: got TotalFreeDevices=%d TotalFreeMemoryMiB=%d len(ByProfile)=%d",
			out.TotalFreeDevices, out.TotalFreeMemoryMiB, len(out.ByProfile))
	}
}

func TestAggregateFleetFromPools_SinglePool(t *testing.T) {
	pools := []schedulerv1alpha1.GPUNodePool{
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 4}},
	}
	out := aggregateFleetFromPools(pools)
	if out.TotalFreeDevices != 4 || out.TotalFreeMemoryMiB != 4*80000 {
		t.Errorf("single pool: got TotalFreeDevices=%d TotalFreeMemoryMiB=%d", out.TotalFreeDevices, out.TotalFreeMemoryMiB)
	}
	cap, ok := out.ByProfile["h100_sxm"]
	if !ok || cap.FreeDevices != 4 || cap.FreeMemoryMiB != 4*80000 || cap.MemoryMiBPerDevice != 80000 {
		t.Errorf("single pool ByProfile[h100_sxm]: ok=%v cap=%+v", ok, cap)
	}
}

func TestAggregateFleetFromPools_MultiplePoolsSameProfile(t *testing.T) {
	// Two pools with same profile: capacity should be summed (29.2).
	pools := []schedulerv1alpha1.GPUNodePool{
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 2}},
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 3}},
	}
	out := aggregateFleetFromPools(pools)
	if out.TotalFreeDevices != 5 || out.TotalFreeMemoryMiB != 5*80000 {
		t.Errorf("same profile: got TotalFreeDevices=%d TotalFreeMemoryMiB=%d", out.TotalFreeDevices, out.TotalFreeMemoryMiB)
	}
	cap, ok := out.ByProfile["h100_sxm"]
	if !ok || cap.FreeDevices != 5 || cap.FreeMemoryMiB != 5*80000 || cap.MemoryMiBPerDevice != 80000 {
		t.Errorf("same profile ByProfile[h100_sxm]: ok=%v cap=%+v", ok, cap)
	}
}

func TestAggregateFleetFromPools_MultiplePoolsMultipleProfiles(t *testing.T) {
	// Multiple pools with different profiles: ByProfile has one entry per profile (29.2).
	pools := []schedulerv1alpha1.GPUNodePool{
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 4}},
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "a100_80gb", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 2}},
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 2}},
	}
	out := aggregateFleetFromPools(pools)
	if out.TotalFreeDevices != 8 || out.TotalFreeMemoryMiB != 6*80000+2*80000 {
		t.Errorf("multi profile: got TotalFreeDevices=%d TotalFreeMemoryMiB=%d", out.TotalFreeDevices, out.TotalFreeMemoryMiB)
	}
	if len(out.ByProfile) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(out.ByProfile))
	}
	h := out.ByProfile["h100_sxm"]
	if h.FreeDevices != 6 || h.FreeMemoryMiB != 6*80000 || h.MemoryMiBPerDevice != 80000 {
		t.Errorf("ByProfile[h100_sxm]: %+v", h)
	}
	a := out.ByProfile["a100_80gb"]
	if a.FreeDevices != 2 || a.FreeMemoryMiB != 2*80000 || a.MemoryMiBPerDevice != 80000 {
		t.Errorf("ByProfile[a100_80gb]: %+v", a)
	}
}

func TestAggregateFleetFromPools_ValidatesForScheduler(t *testing.T) {
	// Output should pass scheduler SchedulingInputs validation (totals match ByProfile sums).
	pools := []schedulerv1alpha1.GPUNodePool{
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 16000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 2}},
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "a100_80gb", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 1}},
	}
	out := aggregateFleetFromPools(pools)
	inputs := scheduler.SchedulingInputs{FleetFreeCapacity: out}
	if err := inputs.Validate(); err != nil {
		t.Errorf("aggregate output should pass Validate: %v", err)
	}
}

func TestAggregateFleetFromPools_SkipsEmptyProfile(t *testing.T) {
	pools := []schedulerv1alpha1.GPUNodePool{
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 4}},
		{Spec: schedulerv1alpha1.GPUNodePoolSpec{Profile: "h100_sxm", MemoryMiBPerDevice: 80000},
			Status: schedulerv1alpha1.GPUNodePoolStatus{AvailableDevices: 2}},
	}
	out := aggregateFleetFromPools(pools)
	// Only h100_sxm counted; empty profile skipped so totals match ByProfile.
	if out.TotalFreeDevices != 2 || len(out.ByProfile) != 1 {
		t.Errorf("empty profile should be skipped: TotalFreeDevices=%d len(ByProfile)=%d",
			out.TotalFreeDevices, len(out.ByProfile))
	}
	if err := (scheduler.SchedulingInputs{FleetFreeCapacity: out}).Validate(); err != nil {
		t.Errorf("output with skipped empty profile should still validate: %v", err)
	}
}
