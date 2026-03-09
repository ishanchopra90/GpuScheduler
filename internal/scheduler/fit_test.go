package scheduler

import "testing"

func TestFitsGPUCount(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm":  {FreeDevices: 4, MemoryMiBPerDevice: 80000},
			"a100_80gb": {FreeDevices: 1, MemoryMiBPerDevice: 40000},
		},
	}

	t.Run("fits when enough devices free", func(t *testing.T) {
		ok, err := FitsGPUCount(QueuedWorkload{GPUCount: 2, Profile: "h100_sxm"}, fleet)
		if err != nil {
			t.Fatalf("FitsGPUCount() error = %v", err)
		}
		if !ok {
			t.Fatalf("expected fit, got not-fit")
		}
	})

	t.Run("does not fit when insufficient devices free", func(t *testing.T) {
		ok, err := FitsGPUCount(QueuedWorkload{GPUCount: 2, Profile: "a100_80gb"}, fleet)
		if err != nil {
			t.Fatalf("FitsGPUCount() error = %v", err)
		}
		if ok {
			t.Fatalf("expected not-fit, got fit")
		}
	})

	t.Run("errors on missing profile entry", func(t *testing.T) {
		_, err := FitsGPUCount(QueuedWorkload{GPUCount: 1, Profile: "nope"}, fleet)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("errors on invalid gpuCount", func(t *testing.T) {
		_, err := FitsGPUCount(QueuedWorkload{GPUCount: 0, Profile: "h100_sxm"}, fleet)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
}

func TestFitsPerDeviceMemory(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm":  {MemoryMiBPerDevice: 80000},
			"a100_80gb": {MemoryMiBPerDevice: 40000},
		},
	}

	t.Run("fits when per-device capacity is sufficient", func(t *testing.T) {
		ok, err := FitsPerDeviceMemory(QueuedWorkload{GPUMemoryMiB: 20000, Profile: "a100_80gb"}, fleet)
		if err != nil {
			t.Fatalf("FitsPerDeviceMemory() error = %v", err)
		}
		if !ok {
			t.Fatalf("expected fit, got not-fit")
		}
	})

	t.Run("does not fit when per-device capacity is insufficient", func(t *testing.T) {
		ok, err := FitsPerDeviceMemory(QueuedWorkload{GPUMemoryMiB: 60000, Profile: "a100_80gb"}, fleet)
		if err != nil {
			t.Fatalf("FitsPerDeviceMemory() error = %v", err)
		}
		if ok {
			t.Fatalf("expected not-fit, got fit")
		}
	})

	t.Run("errors when memoryMiBPerDevice is missing", func(t *testing.T) {
		_, err := FitsPerDeviceMemory(QueuedWorkload{GPUMemoryMiB: 1, Profile: "nope"}, fleet)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
}
