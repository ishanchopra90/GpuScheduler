package scheduler

import "fmt"

// FitsGPUCount reports whether the fleet has enough free devices to satisfy the
// workload's GPUCount for its requested profile.
//
// This is a pure input-layer fit check based on FleetFreeCapacity, not a
// placement/allocation simulation.
func FitsGPUCount(w QueuedWorkload, fleet FleetFreeCapacity) (bool, error) {
	if w.GPUCount <= 0 {
		return false, fmt.Errorf("gpuCount must be positive, got %d", w.GPUCount)
	}
	if w.Profile == "" {
		return false, fmt.Errorf("profile is required")
	}
	cap, ok := fleet.ByProfile[w.Profile]
	if !ok {
		return false, fmt.Errorf("no free-capacity entry for profile %q", w.Profile)
	}
	return cap.FreeDevices >= w.GPUCount, nil
}

// FitsPerDeviceMemory reports whether the workload's per-device memory request
// can fit on a single device of the requested profile.
func FitsPerDeviceMemory(w QueuedWorkload, fleet FleetFreeCapacity) (bool, error) {
	if w.GPUMemoryMiB <= 0 {
		return false, fmt.Errorf("gpuMemoryMiB must be positive, got %d", w.GPUMemoryMiB)
	}
	if w.Profile == "" {
		return false, fmt.Errorf("profile is required")
	}
	cap, ok := fleet.ByProfile[w.Profile]
	if !ok {
		return false, fmt.Errorf("no free-capacity entry for profile %q", w.Profile)
	}
	if cap.MemoryMiBPerDevice <= 0 {
		return false, fmt.Errorf("missing memoryMiBPerDevice for profile %q", w.Profile)
	}
	return cap.MemoryMiBPerDevice >= w.GPUMemoryMiB, nil
}
