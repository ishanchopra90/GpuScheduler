package scheduler

import (
	"fmt"
	"strings"

	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

func normalizeProfileName(p string) string {
	return strings.ToLower(strings.TrimSpace(p))
}

// CompatibleProfileAndKind checks basic runtime compatibility:
// - the workload kind is a supported runtime kind
// - the requested profile exists in the fleet snapshot
// - the requested profile is known to the simulator's hardware profile registry
//
// This is not a placement check; it only validates that the request makes sense
// against fleet + simulator metadata.
func CompatibleProfileAndKind(w QueuedWorkload, fleet FleetFreeCapacity) error {
	switch w.Kind {
	case WorkloadKindTraining, WorkloadKindInference, WorkloadKindEval, WorkloadKindFineTune,
		WorkloadKindRLHF, WorkloadKindEmbedding, WorkloadKindDataPreprocess, WorkloadKindDistillation:
	default:
		return fmt.Errorf("unsupported kind %q", w.Kind)
	}

	profile := normalizeProfileName(w.Profile)
	if profile == "" {
		return fmt.Errorf("profile is required")
	}
	if fleet.ByProfile != nil {
		if _, ok := fleet.ByProfile[profile]; !ok {
			return fmt.Errorf("profile %q is not present in fleet free-capacity snapshot", profile)
		}
	}

	if _, ok := sim.HardwareProfiles()[profile]; !ok {
		return fmt.Errorf("profile %q is not known to simulator hardware profiles", profile)
	}
	return nil
}
