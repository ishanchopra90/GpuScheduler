package scheduler

import "testing"

func TestCompatibleProfileAndKind(t *testing.T) {
	fleet := FleetFreeCapacity{
		ByProfile: map[string]ProfileFreeCapacity{
			"h100_sxm": {FreeDevices: 1, FreeMemoryMiB: 16000, MemoryMiBPerDevice: 16000},
		},
	}

	t.Run("ok for known kind and profile", func(t *testing.T) {
		err := CompatibleProfileAndKind(QueuedWorkload{Kind: WorkloadKindTraining, Profile: "h100_sxm"}, fleet)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("normalizes profile case", func(t *testing.T) {
		err := CompatibleProfileAndKind(QueuedWorkload{Kind: WorkloadKindTraining, Profile: "H100_SXM"}, fleet)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("rejects unknown kind", func(t *testing.T) {
		err := CompatibleProfileAndKind(QueuedWorkload{Kind: WorkloadKind("nope"), Profile: "h100_sxm"}, fleet)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("rejects profile not in fleet snapshot", func(t *testing.T) {
		err := CompatibleProfileAndKind(QueuedWorkload{Kind: WorkloadKindTraining, Profile: "a100_80gb"}, fleet)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("rejects profile unknown to simulator registry", func(t *testing.T) {
		fleet2 := FleetFreeCapacity{
			ByProfile: map[string]ProfileFreeCapacity{
				"custom_profile": {FreeDevices: 1, FreeMemoryMiB: 1, MemoryMiBPerDevice: 1},
			},
		}
		err := CompatibleProfileAndKind(QueuedWorkload{Kind: WorkloadKindTraining, Profile: "custom_profile"}, fleet2)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})
}
