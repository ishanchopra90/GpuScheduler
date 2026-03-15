package sim

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mustRegisterFleet(t *testing.T, spec GPUNodePoolSpec) *Simulator {
	t.Helper()
	s := NewSimulator()
	if err := s.RegisterFleet(DefaultPoolKey, spec); err != nil {
		t.Fatalf("RegisterFleet() error = %v", err)
	}
	return s
}

func nodeFromDeviceID(t *testing.T, deviceID string) string {
	t.Helper()
	parts := strings.Split(deviceID, "/")
	if len(parts) != 2 || parts[0] == "" {
		t.Fatalf("unexpected deviceID format: %q", deviceID)
	}
	return parts[0]
}

func nodesUsed(t *testing.T, deviceIDs []string) map[string]int {
	t.Helper()
	m := make(map[string]int)
	for _, id := range deviceIDs {
		m[nodeFromDeviceID(t, id)]++
	}
	return m
}

func TestAllocateWithOptions_PlacementPolicies(t *testing.T) {
	spec := GPUNodePoolSpec{
		NodeCount:          3,
		DevicesPerNode:     4,
		MemoryMiBPerDevice: 16000,
		DeviceType:         "nvidia",
		Profile:            "h100_sxm",
	}

	t.Run("first_fit picks in node order", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		got, err := s.AllocateWithOptions("w1", 5, 1000, AllocationOptions{PlacementPolicy: PlacementPolicyFirstFit})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}

		want := []string{
			"node-0/gpu-0",
			"node-0/gpu-1",
			"node-0/gpu-2",
			"node-0/gpu-3",
			"node-1/gpu-0",
		}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("pack prefers nodes with most eligible devices", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		// Make remaining eligible counts differ by marking some devices as already allocated.
		s.allocations["existing-0"] = &Allocation{
			WorkloadID: "existing-0",
			DeviceIDs: []string{
				"node-0/gpu-0",
				"node-0/gpu-1",
				"node-0/gpu-2", // node-0: 1 free
				"node-2/gpu-0", // node-2: 3 free
			},
		}

		got, err := s.AllocateWithOptions("w1", 5, 1000, AllocationOptions{PlacementPolicy: PlacementPolicyPack})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}

		want := []string{
			"node-1/gpu-0",
			"node-1/gpu-1",
			"node-1/gpu-2",
			"node-1/gpu-3",
			"node-2/gpu-1",
		}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("spread round-robins across nodes", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		got, err := s.AllocateWithOptions("w1", 5, 1000, AllocationOptions{PlacementPolicy: PlacementPolicySpread})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}

		want := []string{
			"node-0/gpu-0",
			"node-1/gpu-0",
			"node-2/gpu-0",
			"node-0/gpu-1",
			"node-1/gpu-1",
		}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("locality_aware chooses single-node when possible", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		got, err := s.AllocateWithOptions("w1", 3, 1000, AllocationOptions{PlacementPolicy: PlacementPolicyLocalityAware})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}
		used := nodesUsed(t, got.DeviceIDs)
		if len(used) != 1 {
			t.Fatalf("expected single-node allocation, got nodes=%v (deviceIDs=%v)", used, got.DeviceIDs)
		}
		if used["node-0"] != 3 {
			t.Fatalf("expected node-0 to be used (first node with enough), got nodes=%v (deviceIDs=%v)", used, got.DeviceIDs)
		}
	})

	t.Run("locality_aware falls back to pack when single-node not possible", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		s.allocations["existing-0"] = &Allocation{
			WorkloadID: "existing-0",
			DeviceIDs: []string{
				"node-0/gpu-0",
				"node-0/gpu-1",
				"node-0/gpu-2", // node-0: 1 free
				"node-2/gpu-0", // node-2: 3 free
			},
		}

		pack, err := s.AllocateWithOptions("w-pack", 5, 1000, AllocationOptions{PlacementPolicy: PlacementPolicyPack})
		if err != nil {
			t.Fatalf("AllocateWithOptions(pack) error = %v", err)
		}
		if err := s.Release("w-pack"); err != nil {
			t.Fatalf("Release(w-pack) error = %v", err)
		}

		got, err := s.AllocateWithOptions("w-locality", 5, 1000, AllocationOptions{PlacementPolicy: PlacementPolicyLocalityAware})
		if err != nil {
			t.Fatalf("AllocateWithOptions(locality_aware) error = %v", err)
		}
		if !reflect.DeepEqual(got.DeviceIDs, pack.DeviceIDs) {
			t.Fatalf("expected locality_aware to match pack fallback:\n got: %#v\nwant: %#v", got.DeviceIDs, pack.DeviceIDs)
		}
	})
}

func TestAllocateWithOptions_ConstraintsAndFilters(t *testing.T) {
	spec := GPUNodePoolSpec{
		NodeCount:          3,
		DevicesPerNode:     4,
		MemoryMiBPerDevice: 16000,
		DeviceType:         "nvidia",
		Profile:            "h100_sxm",
	}

	t.Run("requireSameNode picks node with most eligible devices", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		// Reduce eligibility on node-0 so node-1 wins.
		s.allocations["existing-0"] = &Allocation{
			WorkloadID: "existing-0",
			DeviceIDs: []string{
				"node-0/gpu-0",
				"node-0/gpu-1", // node-0 now has only 2 free, cannot fit 3.
			},
		}

		got, err := s.AllocateWithOptions("w1", 3, 1000, AllocationOptions{
			PlacementPolicy:  PlacementPolicySpread, // should be ignored under RequireSameNode
			RequireSameNode:  true,
			MaxNodes:         0,
			PreferredProfile: "",
		})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}

		want := []string{"node-1/gpu-0", "node-1/gpu-1", "node-1/gpu-2"}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("maxNodes limits selection and can cause insufficient devices", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 5, 1000, AllocationOptions{
			PlacementPolicy: PlacementPolicyFirstFit,
			MaxNodes:        1,
		}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("spread with maxNodes=1 stays on the first node", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		got, err := s.AllocateWithOptions("w1", 3, 1000, AllocationOptions{
			PlacementPolicy: PlacementPolicySpread,
			MaxNodes:        1,
		})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}
		want := []string{"node-0/gpu-0", "node-0/gpu-1", "node-0/gpu-2"}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("preferred device type filters eligible devices", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		nodes := s.pools[DefaultPoolKey]
		for _, d := range nodes[0].Devices {
			d.DeviceType = "type-a"
		}
		for i := 1; i < len(nodes); i++ {
			for _, d := range nodes[i].Devices {
				d.DeviceType = "type-b"
			}
		}

		got, err := s.AllocateWithOptions("w1", 2, 1000, AllocationOptions{
			PlacementPolicy:     PlacementPolicyFirstFit,
			PreferredDeviceType: "type-a",
			PreferredProfile:    "",
			RequireSameNode:     false,
			MaxNodes:            0,
		})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}
		want := []string{"node-0/gpu-0", "node-0/gpu-1"}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("preferred profile filters eligible devices", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		nodes := s.pools[DefaultPoolKey]
		for _, d := range nodes[0].Devices {
			d.Profile = "p1"
		}
		for i := 1; i < len(nodes); i++ {
			for _, d := range nodes[i].Devices {
				d.Profile = "p2"
			}
		}

		got, err := s.AllocateWithOptions("w1", 2, 1000, AllocationOptions{
			PlacementPolicy:  PlacementPolicyFirstFit,
			PreferredProfile: "p1",
		})
		if err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}
		want := []string{"node-0/gpu-0", "node-0/gpu-1"}
		if !reflect.DeepEqual(got.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch:\n got: %#v\nwant: %#v", got.DeviceIDs, want)
		}
	})

	t.Run("unknown placement policy errors", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 1, 1000, AllocationOptions{
			PlacementPolicy: PlacementPolicy("nope"),
		}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
}

// TestAllocate_CapacityEnforcement_NoOverAllocation verifies that when the fleet is full,
// Allocate returns an error and does not record an allocation (no over-allocation).
// Overscaled workers get fast failure and can back off.
func TestAllocate_CapacityEnforcement_NoOverAllocation(t *testing.T) {
	spec := GPUNodePoolSpec{
		NodeCount:          1,
		DevicesPerNode:     2,
		MemoryMiBPerDevice: 16000,
		DeviceType:         "nvidia",
		Profile:            "h100_sxm",
	}
	s := mustRegisterFleet(t, spec)

	alloc1, err := s.Allocate("w1", 2, 1000)
	if err != nil {
		t.Fatalf("first Allocate() error = %v", err)
	}
	if len(alloc1.DeviceIDs) != 2 {
		t.Fatalf("first allocation: want 2 devices, got %d", len(alloc1.DeviceIDs))
	}

	_, err = s.Allocate("w2", 1, 1000)
	if err == nil {
		t.Fatalf("second Allocate() with full fleet: expected error, got nil")
	}

	if _, ok := s.allocations["w2"]; ok {
		t.Fatalf("Allocate failed but w2 was added to allocations (over-allocation)")
	}

	usage := s.FleetUsage()
	if usage.TotalDevices != 2 || usage.AllocatedDevices != 2 || usage.AvailableDevices != 0 {
		t.Fatalf("FleetUsage after failed Allocate: want Total=2 Allocated=2 Available=0, got %+v", usage)
	}
}

func TestLifecycleFlow_SucceedFailPreemptAndRelease(t *testing.T) {
	spec := GPUNodePoolSpec{
		NodeCount:          2,
		DevicesPerNode:     2,
		MemoryMiBPerDevice: 16000,
		DeviceType:         "nvidia",
		Profile:            "h100_sxm",
	}

	t.Run("allocate->start->running->succeeded->release allows reuse", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		alloc, err := s.Allocate("w1", 2, 1000)
		if err != nil {
			t.Fatalf("Allocate() error = %v", err)
		}
		if alloc.WorkloadID != "w1" || len(alloc.DeviceIDs) != 2 {
			t.Fatalf("unexpected allocation: %#v", alloc)
		}

		runID, err := s.Start("w1", 1, "h100_sxm")
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		st, err := s.GetStatus(runID)
		if err != nil {
			t.Fatalf("GetStatus() error = %v", err)
		}
		if st != RunStatusRunning {
			t.Fatalf("expected Running, got %q", st)
		}

		// Force completion without waiting for wall-clock time.
		s.mu.Lock()
		s.runs[runID].EndsAt = time.Now().Add(-time.Second)
		s.mu.Unlock()

		st, err = s.GetStatus(runID)
		if err != nil {
			t.Fatalf("GetStatus() error = %v", err)
		}
		if st != RunStatusSucceeded {
			t.Fatalf("expected Succeeded, got %q", st)
		}

		if err := s.Release("w1"); err != nil {
			t.Fatalf("Release() error = %v", err)
		}

		alloc2, err := s.Allocate("w2", 2, 1000)
		if err != nil {
			t.Fatalf("Allocate() error = %v", err)
		}
		// First-fit on a freed pool should deterministically reuse the first devices.
		want := []string{"node-0/gpu-0", "node-0/gpu-1"}
		if !reflect.DeepEqual(alloc2.DeviceIDs, want) {
			t.Fatalf("DeviceIDs mismatch after reuse:\n got: %#v\nwant: %#v", alloc2.DeviceIDs, want)
		}
	})

	t.Run("failed is terminal and does not auto-transition to succeeded", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		if _, err := s.Allocate("w1", 1, 1000); err != nil {
			t.Fatalf("Allocate() error = %v", err)
		}
		runID, err := s.Start("w1", 1, "h100_sxm")
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		s.mu.Lock()
		s.runs[runID].Status = RunStatusFailed
		s.runs[runID].EndsAt = time.Now().Add(-time.Second)
		s.mu.Unlock()

		st, err := s.GetStatus(runID)
		if err != nil {
			t.Fatalf("GetStatus() error = %v", err)
		}
		if st != RunStatusFailed {
			t.Fatalf("expected Failed, got %q", st)
		}
	})

	t.Run("preempt sets preempted status", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)

		if _, err := s.Allocate("w1", 1, 1000); err != nil {
			t.Fatalf("Allocate() error = %v", err)
		}
		runID, err := s.Start("w1", 1, "h100_sxm")
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}

		if err := s.Preempt("w1"); err != nil {
			t.Fatalf("Preempt() error = %v", err)
		}
		st, err := s.GetStatus(runID)
		if err != nil {
			t.Fatalf("GetStatus() error = %v", err)
		}
		if st != RunStatusPreempted {
			t.Fatalf("expected Preempted, got %q", st)
		}
	})
}

func TestAllocateWithOptions_ValidationErrors(t *testing.T) {
	spec := GPUNodePoolSpec{NodeCount: 1, DevicesPerNode: 1, MemoryMiBPerDevice: 16000}

	t.Run("nil simulator", func(t *testing.T) {
		var s *Simulator
		if _, err := s.AllocateWithOptions("w1", 1, 1, AllocationOptions{}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("fleet not registered", func(t *testing.T) {
		s := NewSimulator()
		if _, err := s.AllocateWithOptions("w1", 1, 1, AllocationOptions{}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("workloadID required", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("", 1, 1, AllocationOptions{}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("gpuCount must be positive", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 0, 1, AllocationOptions{}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("memMiB must be positive", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 1, 0, AllocationOptions{}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("duplicate allocation errors", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 1, 1, AllocationOptions{}); err != nil {
			t.Fatalf("AllocateWithOptions() error = %v", err)
		}
		if _, err := s.AllocateWithOptions("w1", 1, 1, AllocationOptions{}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("maxNodes negative errors", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 1, 1, AllocationOptions{MaxNodes: -1}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("requireSameNode errors when no node can fit", func(t *testing.T) {
		s := mustRegisterFleet(t, spec)
		if _, err := s.AllocateWithOptions("w1", 2, 1, AllocationOptions{RequireSameNode: true}); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
}

func TestHardwareProfiles_MinimalFields(t *testing.T) {
	profiles := HardwareProfiles()
	if len(profiles) == 0 {
		t.Fatalf("expected non-empty hardware profile registry")
	}

	for key, p := range profiles {
		if p.Name == "" {
			t.Fatalf("profile %q missing name", key)
		}
		if p.MemoryMiB <= 0 {
			t.Fatalf("profile %q missing/invalid memoryMiB: %d", key, p.MemoryMiB)
		}
		if p.MemBandwidthGBps <= 0 {
			t.Fatalf("profile %q missing/invalid memBandwidthGBps: %v", key, p.MemBandwidthGBps)
		}
		if p.Notes == "" {
			t.Fatalf("profile %q missing notes", key)
		}
		if p.SourceURL == "" {
			t.Fatalf("profile %q missing source_url", key)
		}
		if len(p.SourceURLs) == 0 {
			t.Fatalf("profile %q missing source_urls", key)
		}
		for i, u := range p.SourceURLs {
			if strings.TrimSpace(u) == "" {
				t.Fatalf("profile %q has empty source_urls[%d]", key, i)
			}
		}
	}
}

func TestHardwareProfiles_RuntimeThroughputCoverage(t *testing.T) {
	for key := range hardwareProfiles {
		if _, ok := profileTokensPerSecond[key]; !ok {
			t.Fatalf("profile %q has hardware metadata but no runtime throughput", key)
		}
	}
	for key := range profileTokensPerSecond {
		if _, ok := hardwareProfiles[key]; !ok {
			t.Fatalf("profile %q has runtime throughput but no hardware metadata", key)
		}
	}
}

func TestEstimateDuration_HardwareProfileMetricsAffectRuntime(t *testing.T) {
	baseTokens := int64(5000000)
	baseInput := RuntimeInput{
		WorkloadID: "w-metrics",
		Tokens:     baseTokens,
		Profile:    "h100_sxm",
	}

	t.Run("lower memory bandwidth increases duration", func(t *testing.T) {
		orig := hardwareProfiles["h100_sxm"]
		slower := orig
		slower.MemBandwidthGBps = 1200
		hardwareProfiles["h100_sxm"] = slower
		t.Cleanup(func() {
			hardwareProfiles["h100_sxm"] = orig
		})

		dSlow, err := estimateDuration(baseInput, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		hardwareProfiles["h100_sxm"] = orig
		dBaseline, err := estimateDuration(baseInput, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}

		if dSlow <= dBaseline {
			t.Fatalf("expected lower mem bandwidth to increase duration: slow=%v baseline=%v", dSlow, dBaseline)
		}
	})

	t.Run("lower compute increases duration when BF16 throughput is present", func(t *testing.T) {
		orig := hardwareProfiles["h100_sxm"]
		if orig.PeakTFLOPSBF16 == nil {
			t.Fatalf("expected h100_sxm to have peakTFLOPSBF16")
		}
		slower := orig
		low := 120.0
		slower.PeakTFLOPSBF16 = &low
		hardwareProfiles["h100_sxm"] = slower
		t.Cleanup(func() {
			hardwareProfiles["h100_sxm"] = orig
		})

		dSlow, err := estimateDuration(baseInput, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		hardwareProfiles["h100_sxm"] = orig
		dBaseline, err := estimateDuration(baseInput, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		if dSlow <= dBaseline {
			t.Fatalf("expected lower compute to increase duration: slow=%v baseline=%v", dSlow, dBaseline)
		}
	})

	t.Run("interconnect only affects multi-device duration", func(t *testing.T) {
		orig := hardwareProfiles["h100_sxm"]
		if orig.InterconnectGBps == nil {
			t.Fatalf("expected h100_sxm to have interconnectGBps")
		}
		slower := orig
		low := 100.0
		slower.InterconnectGBps = &low
		hardwareProfiles["h100_sxm"] = slower
		t.Cleanup(func() {
			hardwareProfiles["h100_sxm"] = orig
		})

		dSlowOne, err := estimateDuration(baseInput, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		dSlowTwo, err := estimateDuration(baseInput, 2)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}

		hardwareProfiles["h100_sxm"] = orig
		dBaseOne, err := estimateDuration(baseInput, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		dBaseTwo, err := estimateDuration(baseInput, 2)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}

		if dSlowOne != dBaseOne {
			t.Fatalf("expected interconnect to have no 1-device impact: slow=%v baseline=%v", dSlowOne, dBaseOne)
		}
		if dSlowTwo <= dBaseTwo {
			t.Fatalf("expected lower interconnect to increase 2-device duration: slow=%v baseline=%v", dSlowTwo, dBaseTwo)
		}
	})
}

func TestEstimateDuration_Jitter(t *testing.T) {
	input := RuntimeInput{
		WorkloadID: "w-jitter",
		Tokens:     5000000,
		Profile:    "h100_sxm",
	}

	t.Run("disabled jitter does not change duration", func(t *testing.T) {
		base, err := estimateDuration(input, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		disabled := input
		disabled.Jitter = &JitterConfig{Enabled: false, Pct: 0.25, Seed: 123}
		got, err := estimateDuration(disabled, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		if got != base {
			t.Fatalf("expected disabled jitter to keep duration unchanged: got=%v base=%v", got, base)
		}
	})

	t.Run("same seed yields deterministic duration", func(t *testing.T) {
		withJitter := input
		withJitter.Jitter = &JitterConfig{Enabled: true, Pct: 0.25, Seed: 42}
		d1, err := estimateDuration(withJitter, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		d2, err := estimateDuration(withJitter, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		if d1 != d2 {
			t.Fatalf("expected deterministic jitter with same seed: first=%v second=%v", d1, d2)
		}
	})

	t.Run("different seeds can yield different durations within bounds", func(t *testing.T) {
		base, err := estimateDuration(input, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}
		withSeed1 := input
		withSeed1.Jitter = &JitterConfig{Enabled: true, Pct: 0.25, Seed: 1}
		d1, err := estimateDuration(withSeed1, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}

		withSeed2 := input
		withSeed2.Jitter = &JitterConfig{Enabled: true, Pct: 0.25, Seed: 2}
		d2, err := estimateDuration(withSeed2, 1)
		if err != nil {
			t.Fatalf("estimateDuration() error = %v", err)
		}

		lower := time.Duration(math.Ceil(float64(base) * (1 - 0.25)))
		upper := time.Duration(math.Ceil(float64(base) * (1 + 0.25)))
		if d1 < lower || d1 > upper {
			t.Fatalf("seed1 jitter out of bounds: got=%v range=[%v,%v]", d1, lower, upper)
		}
		if d2 < lower || d2 > upper {
			t.Fatalf("seed2 jitter out of bounds: got=%v range=[%v,%v]", d2, lower, upper)
		}
		if d1 == d2 {
			t.Fatalf("expected different seeds to produce different durations; both were %v", d1)
		}
	})
}

func TestEstimateDuration_InferenceBatchEfficiencyCurve(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-inf-batch",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindInference,
	}

	withBatch1 := base
	withBatch1.Inference = &InferenceRuntime{BatchSize: 1}
	d1, err := estimateDuration(withBatch1, 1)
	if err != nil {
		t.Fatalf("estimateDuration(batch=1) error = %v", err)
	}

	withBatch8 := base
	withBatch8.Inference = &InferenceRuntime{BatchSize: 8}
	d8, err := estimateDuration(withBatch8, 1)
	if err != nil {
		t.Fatalf("estimateDuration(batch=8) error = %v", err)
	}
	if d8 >= d1 {
		t.Fatalf("expected higher inference batch size to reduce duration: batch8=%v batch1=%v", d8, d1)
	}

	withBatch1024 := base
	withBatch1024.Inference = &InferenceRuntime{BatchSize: 1024}
	d1024, err := estimateDuration(withBatch1024, 1)
	if err != nil {
		t.Fatalf("estimateDuration(batch=1024) error = %v", err)
	}
	// Curve is intentionally bounded and logarithmic (not linear scaling with batch).
	if d1024*10 < d1 {
		t.Fatalf("unexpectedly aggressive batch scaling: batch1024=%v batch1=%v", d1024, d1)
	}
}

func TestEstimateDuration_TrainingRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-train-factors",
		Tokens:     50000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindTraining,
		Training: &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  32,
			GradAccumSteps:  1,
			SequenceLength:  2048,
		},
	}

	t.Run("larger micro-batch improves training throughput", func(t *testing.T) {
		small := base
		small.Training = &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  8,
			GradAccumSteps:  1,
			SequenceLength:  2048,
		}
		fSmall := trainingRuntimeSecondsMultiplier(small)

		large := base
		large.Training = &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  64,
			GradAccumSteps:  1,
			SequenceLength:  2048,
		}
		fLarge := trainingRuntimeSecondsMultiplier(large)
		if fLarge >= fSmall {
			t.Fatalf("expected larger micro-batch to reduce runtime factor: large=%v small=%v", fLarge, fSmall)
		}
	})

	t.Run("more gradient accumulation increases duration", func(t *testing.T) {
		low := base
		low.Training = &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  32,
			GradAccumSteps:  1,
			SequenceLength:  2048,
		}
		dLow, err := estimateDuration(low, 1)
		if err != nil {
			t.Fatalf("estimateDuration(low grad accum) error = %v", err)
		}

		high := base
		high.Training = &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  32,
			GradAccumSteps:  8,
			SequenceLength:  2048,
		}
		dHigh, err := estimateDuration(high, 1)
		if err != nil {
			t.Fatalf("estimateDuration(high grad accum) error = %v", err)
		}
		if dHigh <= dLow {
			t.Fatalf("expected higher grad accumulation to increase duration: high=%v low=%v", dHigh, dLow)
		}
	})

	t.Run("longer sequence length increases duration", func(t *testing.T) {
		short := base
		short.Training = &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  32,
			GradAccumSteps:  1,
			SequenceLength:  1024,
		}
		dShort, err := estimateDuration(short, 1)
		if err != nil {
			t.Fatalf("estimateDuration(short sequence) error = %v", err)
		}

		long := base
		long.Training = &TrainingRuntime{
			GlobalBatchSize: 1024,
			MicroBatchSize:  32,
			GradAccumSteps:  1,
			SequenceLength:  8192,
		}
		dLong, err := estimateDuration(long, 1)
		if err != nil {
			t.Fatalf("estimateDuration(long sequence) error = %v", err)
		}
		if dLong <= dShort {
			t.Fatalf("expected longer sequence length to increase duration: long=%v short=%v", dLong, dShort)
		}
	})
}

func TestEstimateDuration_EvalRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-eval-factors",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindEval,
		Eval: &EvalRuntime{
			BatchSize:             1,
			MetricOverheadPct:     0,
			ValidationOverheadPct: 0,
		},
	}

	t.Run("larger eval batch improves throughput", func(t *testing.T) {
		b1 := base
		b1.Eval = &EvalRuntime{BatchSize: 1}
		d1, err := estimateDuration(b1, 1)
		if err != nil {
			t.Fatalf("estimateDuration(batch=1) error = %v", err)
		}

		b8 := base
		b8.Eval = &EvalRuntime{BatchSize: 8}
		d8, err := estimateDuration(b8, 1)
		if err != nil {
			t.Fatalf("estimateDuration(batch=8) error = %v", err)
		}
		if d8 >= d1 {
			t.Fatalf("expected larger eval batch to reduce duration: batch8=%v batch1=%v", d8, d1)
		}
	})

	t.Run("metric and validation overhead increase duration", func(t *testing.T) {
		noOverhead := base
		noOverhead.Eval = &EvalRuntime{BatchSize: 4, MetricOverheadPct: 0, ValidationOverheadPct: 0}
		d0, err := estimateDuration(noOverhead, 1)
		if err != nil {
			t.Fatalf("estimateDuration(no overhead) error = %v", err)
		}

		withOverhead := base
		withOverhead.Eval = &EvalRuntime{BatchSize: 4, MetricOverheadPct: 0.20, ValidationOverheadPct: 0.10}
		d1, err := estimateDuration(withOverhead, 1)
		if err != nil {
			t.Fatalf("estimateDuration(with overhead) error = %v", err)
		}
		if d1 <= d0 {
			t.Fatalf("expected eval overheads to increase duration: with=%v without=%v", d1, d0)
		}
	})
}

func TestStartWithRuntimeInput_EvalOverheadValidation(t *testing.T) {
	s := mustRegisterFleet(t, GPUNodePoolSpec{
		NodeCount:          1,
		DevicesPerNode:     1,
		MemoryMiBPerDevice: 16000,
		Profile:            "h100_sxm",
	})
	if _, err := s.Allocate("w-eval", 1, 1000); err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}

	_, err := s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-eval",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindEval,
		Eval: &EvalRuntime{
			BatchSize:             1,
			MetricOverheadPct:     -0.1,
			ValidationOverheadPct: 0.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for negative eval.metricOverheadPct, got nil")
	}

	_, err = s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-eval",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindEval,
		Eval: &EvalRuntime{
			BatchSize:             1,
			MetricOverheadPct:     0.1,
			ValidationOverheadPct: 1.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for eval.validationOverheadPct > 1, got nil")
	}
}

func TestEstimateDuration_FineTuneRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-ft-factors",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindFineTune,
		FineTune: &FineTuneRuntime{
			GlobalBatchSize:           512,
			MicroBatchSize:            16,
			GradAccumSteps:            1,
			WarmStartOverheadPct:      0,
			CheckpointLoadOverheadPct: 0,
		},
	}

	t.Run("larger micro-batch generally improves fine-tune throughput", func(t *testing.T) {
		small := base
		small.FineTune = &FineTuneRuntime{
			GlobalBatchSize: 64,
			MicroBatchSize:  8,
			GradAccumSteps:  1,
		}
		fSmall := fineTuneRuntimeSecondsMultiplier(small)

		large := base
		large.FineTune = &FineTuneRuntime{
			GlobalBatchSize: 128,
			MicroBatchSize:  16,
			GradAccumSteps:  1,
		}
		fLarge := fineTuneRuntimeSecondsMultiplier(large)

		if fLarge >= fSmall {
			t.Fatalf("expected larger micro-batch to reduce fine-tune runtime factor: large=%v small=%v", fLarge, fSmall)
		}
	})

	t.Run("higher grad accumulation increases duration", func(t *testing.T) {
		low := base
		low.FineTune = &FineTuneRuntime{
			GlobalBatchSize: 512,
			MicroBatchSize:  16,
			GradAccumSteps:  1,
		}
		dLow, err := estimateDuration(low, 1)
		if err != nil {
			t.Fatalf("estimateDuration(low grad accum) error = %v", err)
		}

		high := base
		high.FineTune = &FineTuneRuntime{
			GlobalBatchSize: 512,
			MicroBatchSize:  16,
			GradAccumSteps:  8,
		}
		dHigh, err := estimateDuration(high, 1)
		if err != nil {
			t.Fatalf("estimateDuration(high grad accum) error = %v", err)
		}
		if dHigh <= dLow {
			t.Fatalf("expected higher grad accumulation to increase duration: high=%v low=%v", dHigh, dLow)
		}
	})

	t.Run("warm-start/checkpoint overhead increases duration", func(t *testing.T) {
		noWarm := base
		noWarm.FineTune = &FineTuneRuntime{
			GlobalBatchSize:           512,
			MicroBatchSize:            16,
			GradAccumSteps:            1,
			WarmStartOverheadPct:      0,
			CheckpointLoadOverheadPct: 0,
		}
		d0, err := estimateDuration(noWarm, 1)
		if err != nil {
			t.Fatalf("estimateDuration(no warm overhead) error = %v", err)
		}

		withWarm := base
		withWarm.FineTune = &FineTuneRuntime{
			GlobalBatchSize:           512,
			MicroBatchSize:            16,
			GradAccumSteps:            1,
			WarmStartOverheadPct:      0.20,
			CheckpointLoadOverheadPct: 0.10,
		}
		d1, err := estimateDuration(withWarm, 1)
		if err != nil {
			t.Fatalf("estimateDuration(with warm overhead) error = %v", err)
		}
		if d1 <= d0 {
			t.Fatalf("expected warm/checkpoint overhead to increase duration: with=%v without=%v", d1, d0)
		}
	})
}

func TestStartWithRuntimeInput_FineTuneOverheadValidation(t *testing.T) {
	s := mustRegisterFleet(t, GPUNodePoolSpec{
		NodeCount:          1,
		DevicesPerNode:     1,
		MemoryMiBPerDevice: 16000,
		Profile:            "h100_sxm",
	})
	if _, err := s.Allocate("w-ft", 1, 1000); err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}

	_, err := s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-ft",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindFineTune,
		FineTune: &FineTuneRuntime{
			GlobalBatchSize:           128,
			MicroBatchSize:            8,
			GradAccumSteps:            1,
			WarmStartOverheadPct:      -0.1,
			CheckpointLoadOverheadPct: 0.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for negative fineTune.warmStartOverheadPct, got nil")
	}

	_, err = s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-ft",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindFineTune,
		FineTune: &FineTuneRuntime{
			GlobalBatchSize:           128,
			MicroBatchSize:            8,
			GradAccumSteps:            1,
			WarmStartOverheadPct:      0.1,
			CheckpointLoadOverheadPct: 1.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for fineTune.checkpointLoadOverheadPct > 1, got nil")
	}
}

func TestEstimateDuration_RLHFRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-rlhf-factors",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindRLHF,
		RLHF: &RLHFRuntime{
			RolloutBatchSize:  8,
			RewardBatchSize:   8,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 1,
		},
	}

	t.Run("larger reward batch lowers reward-stage overhead", func(t *testing.T) {
		small := base
		small.RLHF = &RLHFRuntime{
			RolloutBatchSize:  8,
			RewardBatchSize:   2,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 1,
		}
		dSmall, err := estimateDuration(small, 1)
		if err != nil {
			t.Fatalf("estimateDuration(small reward batch) error = %v", err)
		}

		large := base
		large.RLHF = &RLHFRuntime{
			RolloutBatchSize:  8,
			RewardBatchSize:   16,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 1,
		}
		dLarge, err := estimateDuration(large, 1)
		if err != nil {
			t.Fatalf("estimateDuration(large reward batch) error = %v", err)
		}
		if dLarge >= dSmall {
			t.Fatalf("expected larger reward batch to reduce duration: large=%v small=%v", dLarge, dSmall)
		}
	})

	t.Run("more policy update steps increase duration", func(t *testing.T) {
		oneStep := base
		oneStep.RLHF = &RLHFRuntime{
			RolloutBatchSize:  8,
			RewardBatchSize:   8,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 1,
		}
		d1, err := estimateDuration(oneStep, 1)
		if err != nil {
			t.Fatalf("estimateDuration(one update step) error = %v", err)
		}

		manySteps := base
		manySteps.RLHF = &RLHFRuntime{
			RolloutBatchSize:  8,
			RewardBatchSize:   8,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 6,
		}
		d6, err := estimateDuration(manySteps, 1)
		if err != nil {
			t.Fatalf("estimateDuration(many update steps) error = %v", err)
		}
		if d6 <= d1 {
			t.Fatalf("expected more update steps to increase duration: many=%v one=%v", d6, d1)
		}
	})

	t.Run("tiny rollout batch increases rollout-stage overhead", func(t *testing.T) {
		tiny := base
		tiny.RLHF = &RLHFRuntime{
			RolloutBatchSize:  1,
			RewardBatchSize:   8,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 1,
		}
		dTiny, err := estimateDuration(tiny, 1)
		if err != nil {
			t.Fatalf("estimateDuration(tiny rollout batch) error = %v", err)
		}

		medium := base
		medium.RLHF = &RLHFRuntime{
			RolloutBatchSize:  8,
			RewardBatchSize:   8,
			UpdateBatchSize:   8,
			PolicyUpdateSteps: 1,
		}
		dMedium, err := estimateDuration(medium, 1)
		if err != nil {
			t.Fatalf("estimateDuration(medium rollout batch) error = %v", err)
		}
		if dTiny <= dMedium {
			t.Fatalf("expected tiny rollout batch to increase duration: tiny=%v medium=%v", dTiny, dMedium)
		}
	})
}

func TestEstimateDuration_EmbeddingRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-embed-factors",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindEmbedding,
		Embedding: &EmbeddingRuntime{
			BatchSize:       1,
			VectorDimension: 1024,
			AvgTokenLength:  512,
		},
	}

	t.Run("larger embedding batch improves throughput", func(t *testing.T) {
		b1 := base
		b1.Embedding = &EmbeddingRuntime{BatchSize: 1, VectorDimension: 1024, AvgTokenLength: 512}
		d1, err := estimateDuration(b1, 1)
		if err != nil {
			t.Fatalf("estimateDuration(batch=1) error = %v", err)
		}

		b16 := base
		b16.Embedding = &EmbeddingRuntime{BatchSize: 16, VectorDimension: 1024, AvgTokenLength: 512}
		d16, err := estimateDuration(b16, 1)
		if err != nil {
			t.Fatalf("estimateDuration(batch=16) error = %v", err)
		}
		if d16 >= d1 {
			t.Fatalf("expected larger embedding batch to reduce duration: batch16=%v batch1=%v", d16, d1)
		}
	})

	t.Run("larger vector dimension and token length increase duration", func(t *testing.T) {
		small := base
		small.Embedding = &EmbeddingRuntime{
			BatchSize:       8,
			VectorDimension: 512,
			AvgTokenLength:  256,
		}
		dSmall, err := estimateDuration(small, 1)
		if err != nil {
			t.Fatalf("estimateDuration(small embedding shape) error = %v", err)
		}

		large := base
		large.Embedding = &EmbeddingRuntime{
			BatchSize:       8,
			VectorDimension: 3072,
			AvgTokenLength:  2048,
		}
		dLarge, err := estimateDuration(large, 1)
		if err != nil {
			t.Fatalf("estimateDuration(large embedding shape) error = %v", err)
		}
		if dLarge <= dSmall {
			t.Fatalf("expected larger vector/token settings to increase duration: large=%v small=%v", dLarge, dSmall)
		}
	})
}

func TestEstimateDuration_DataPreprocessRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-pre-factors",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindDataPreprocess,
		DataPreprocess: &DataPreprocessRuntime{
			InputBytes:              4_000_000,
			TokenizationOverheadPct: 0,
			AugmentationOverheadPct: 0,
		},
	}

	t.Run("larger input bytes increase duration", func(t *testing.T) {
		small := base
		small.DataPreprocess = &DataPreprocessRuntime{
			InputBytes:              1_000_000,
			TokenizationOverheadPct: 0,
			AugmentationOverheadPct: 0,
		}
		dSmall, err := estimateDuration(small, 1)
		if err != nil {
			t.Fatalf("estimateDuration(small input bytes) error = %v", err)
		}

		large := base
		large.DataPreprocess = &DataPreprocessRuntime{
			InputBytes:              64_000_000,
			TokenizationOverheadPct: 0,
			AugmentationOverheadPct: 0,
		}
		dLarge, err := estimateDuration(large, 1)
		if err != nil {
			t.Fatalf("estimateDuration(large input bytes) error = %v", err)
		}
		if dLarge <= dSmall {
			t.Fatalf("expected larger input bytes to increase duration: large=%v small=%v", dLarge, dSmall)
		}
	})

	t.Run("tokenization and augmentation overhead increase duration", func(t *testing.T) {
		noOverhead := base
		noOverhead.DataPreprocess = &DataPreprocessRuntime{
			InputBytes:              8_000_000,
			TokenizationOverheadPct: 0,
			AugmentationOverheadPct: 0,
		}
		d0, err := estimateDuration(noOverhead, 1)
		if err != nil {
			t.Fatalf("estimateDuration(no preprocess overhead) error = %v", err)
		}

		withOverhead := base
		withOverhead.DataPreprocess = &DataPreprocessRuntime{
			InputBytes:              8_000_000,
			TokenizationOverheadPct: 0.20,
			AugmentationOverheadPct: 0.15,
		}
		d1, err := estimateDuration(withOverhead, 1)
		if err != nil {
			t.Fatalf("estimateDuration(with preprocess overhead) error = %v", err)
		}
		if d1 <= d0 {
			t.Fatalf("expected preprocess overheads to increase duration: with=%v without=%v", d1, d0)
		}
	})
}

func TestStartWithRuntimeInput_DataPreprocessOverheadValidation(t *testing.T) {
	s := mustRegisterFleet(t, GPUNodePoolSpec{
		NodeCount:          1,
		DevicesPerNode:     1,
		MemoryMiBPerDevice: 16000,
		Profile:            "h100_sxm",
	})
	if _, err := s.Allocate("w-pre", 1, 1000); err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}

	_, err := s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-pre",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindDataPreprocess,
		DataPreprocess: &DataPreprocessRuntime{
			InputBytes:              4_000_000,
			TokenizationOverheadPct: -0.1,
			AugmentationOverheadPct: 0.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for negative dataPreprocess.tokenizationOverheadPct, got nil")
	}

	_, err = s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-pre",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindDataPreprocess,
		DataPreprocess: &DataPreprocessRuntime{
			InputBytes:              4_000_000,
			TokenizationOverheadPct: 0.1,
			AugmentationOverheadPct: 1.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for dataPreprocess.augmentationOverheadPct > 1, got nil")
	}
}

func TestEstimateDuration_DistillationRuntimeFactors(t *testing.T) {
	base := RuntimeInput{
		WorkloadID: "w-distill-factors",
		Tokens:     5000000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindDistillation,
		Distillation: &DistillationRuntime{
			TeacherProfile:            "h100_sxm",
			BatchSize:                 8,
			TeacherForwardOverheadPct: 0,
		},
	}

	t.Run("teacher forward overhead increases duration", func(t *testing.T) {
		noOverhead := base
		noOverhead.Distillation = &DistillationRuntime{
			TeacherProfile:            "h100_sxm",
			BatchSize:                 8,
			TeacherForwardOverheadPct: 0,
		}
		d0, err := estimateDuration(noOverhead, 1)
		if err != nil {
			t.Fatalf("estimateDuration(no teacher overhead) error = %v", err)
		}

		withOverhead := base
		withOverhead.Distillation = &DistillationRuntime{
			TeacherProfile:            "h100_sxm",
			BatchSize:                 8,
			TeacherForwardOverheadPct: 0.35,
		}
		d1, err := estimateDuration(withOverhead, 1)
		if err != nil {
			t.Fatalf("estimateDuration(with teacher overhead) error = %v", err)
		}
		if d1 <= d0 {
			t.Fatalf("expected teacher overhead to increase duration: with=%v without=%v", d1, d0)
		}
	})

	t.Run("slower teacher profile increases duration", func(t *testing.T) {
		fastTeacher := base
		fastTeacher.Distillation = &DistillationRuntime{
			TeacherProfile:            "h100_sxm",
			BatchSize:                 8,
			TeacherForwardOverheadPct: 0.10,
		}
		dFast, err := estimateDuration(fastTeacher, 1)
		if err != nil {
			t.Fatalf("estimateDuration(fast teacher) error = %v", err)
		}

		slowTeacher := base
		slowTeacher.Distillation = &DistillationRuntime{
			TeacherProfile:            "trn1_instance",
			BatchSize:                 8,
			TeacherForwardOverheadPct: 0.10,
		}
		dSlow, err := estimateDuration(slowTeacher, 1)
		if err != nil {
			t.Fatalf("estimateDuration(slow teacher) error = %v", err)
		}
		if dSlow <= dFast {
			t.Fatalf("expected slower teacher profile to increase duration: slow=%v fast=%v", dSlow, dFast)
		}
	})
}

func TestStartWithRuntimeInput_DistillationValidation(t *testing.T) {
	s := mustRegisterFleet(t, GPUNodePoolSpec{
		NodeCount:          1,
		DevicesPerNode:     1,
		MemoryMiBPerDevice: 16000,
		Profile:            "h100_sxm",
	})
	if _, err := s.Allocate("w-distill", 1, 1000); err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}

	_, err := s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-distill",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindDistillation,
		Distillation: &DistillationRuntime{
			TeacherProfile:            "unknown_profile",
			BatchSize:                 4,
			TeacherForwardOverheadPct: 0.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for unknown distillation teacher profile, got nil")
	}

	_, err = s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: "w-distill",
		Tokens:     1000,
		Profile:    "h100_sxm",
		Kind:       WorkloadKindDistillation,
		Distillation: &DistillationRuntime{
			TeacherProfile:            "h100_sxm",
			BatchSize:                 4,
			TeacherForwardOverheadPct: 1.1,
		},
	})
	if err == nil {
		t.Fatalf("expected error for distillation.teacherForwardOverheadPct > 1, got nil")
	}
}

func TestHardwareProfiles_LoadedFromEmbeddedJSON(t *testing.T) {
	if len(hardwareProfilesJSON) == 0 {
		t.Fatalf("expected embedded hardwareProfilesJSON to be non-empty")
	}

	loaded := mustLoadHardwareProfilesFromJSON(hardwareProfilesJSON)
	if !reflect.DeepEqual(loaded, hardwareProfiles) {
		t.Fatalf("embedded JSON profiles did not match in-memory registry:\nloaded=%#v\nregistry=%#v", loaded, hardwareProfiles)
	}
}

func TestHardwareProfiles_LoaderPanicsOnInvalidJSON(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic, got nil")
		}
	}()
	_ = mustLoadHardwareProfilesFromJSON([]byte(`{"not":"an array"}`))
}

func TestHardwareProfiles_LoaderPanicsOnInvalidEntry(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic, got nil")
		}
	}()
	_ = mustLoadHardwareProfilesFromJSON([]byte(`[
		{
			"name": "",
			"memoryMiB": 1,
			"memBandwidthGBps": 1,
			"notes": "x",
			"source_url": "y"
		}
	]`))
}
