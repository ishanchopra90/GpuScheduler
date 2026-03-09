package worker

import (
	"strings"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

// WorkloadToRuntimeInput converts a GPUWorkload spec into sim.RuntimeInput for the simulator.
func WorkloadToRuntimeInput(workload *schedulerv1alpha1.GPUWorkload) sim.RuntimeInput {
	workloadID := workload.Namespace + "/" + workload.Name
	input := sim.RuntimeInput{
		WorkloadID: workloadID,
		Tokens:     workload.Spec.Tokens,
		Profile:    strings.ToLower(strings.TrimSpace(workload.Spec.Profile)),
		Kind:       sim.WorkloadKind(workload.Spec.Kind),
	}
	if workload.Spec.Training != nil {
		input.Training = &sim.TrainingRuntime{
			GlobalBatchSize: int(workload.Spec.Training.GlobalBatchSize),
			MicroBatchSize:  int(workload.Spec.Training.MicroBatchSize),
			GradAccumSteps:  int(workload.Spec.Training.GradAccumSteps),
			SequenceLength:  int(workload.Spec.Training.SequenceLength),
		}
	}
	if workload.Spec.Inference != nil {
		input.Inference = &sim.InferenceRuntime{
			BatchSize: int(workload.Spec.Inference.BatchSize),
		}
	}
	if workload.Spec.Eval != nil {
		input.Eval = &sim.EvalRuntime{
			BatchSize:             int(workload.Spec.Eval.BatchSize),
			MetricOverheadPct:     workload.Spec.Eval.MetricOverheadPct,
			ValidationOverheadPct: workload.Spec.Eval.ValidationOverheadPct,
		}
	}
	if workload.Spec.FineTune != nil {
		input.FineTune = &sim.FineTuneRuntime{
			GlobalBatchSize:           int(workload.Spec.FineTune.GlobalBatchSize),
			MicroBatchSize:            int(workload.Spec.FineTune.MicroBatchSize),
			GradAccumSteps:            int(workload.Spec.FineTune.GradAccumSteps),
			WarmStartOverheadPct:      workload.Spec.FineTune.WarmStartOverheadPct,
			CheckpointLoadOverheadPct: workload.Spec.FineTune.CheckpointLoadOverheadPct,
		}
	}
	if workload.Spec.RLHF != nil {
		input.RLHF = &sim.RLHFRuntime{
			RolloutBatchSize:  int(workload.Spec.RLHF.RolloutBatchSize),
			RewardBatchSize:   int(workload.Spec.RLHF.RewardBatchSize),
			UpdateBatchSize:   int(workload.Spec.RLHF.UpdateBatchSize),
			PolicyUpdateSteps: int(workload.Spec.RLHF.PolicyUpdateSteps),
		}
	}
	if workload.Spec.Embedding != nil {
		input.Embedding = &sim.EmbeddingRuntime{
			BatchSize:       int(workload.Spec.Embedding.BatchSize),
			VectorDimension: int(workload.Spec.Embedding.VectorDimension),
			AvgTokenLength:  int(workload.Spec.Embedding.AvgTokenLength),
		}
	}
	if workload.Spec.DataPreprocess != nil {
		input.DataPreprocess = &sim.DataPreprocessRuntime{
			InputBytes:              workload.Spec.DataPreprocess.InputBytes,
			TokenizationOverheadPct: workload.Spec.DataPreprocess.TokenizationOverheadPct,
			AugmentationOverheadPct: workload.Spec.DataPreprocess.AugmentationOverheadPct,
		}
	}
	if workload.Spec.Distillation != nil {
		input.Distillation = &sim.DistillationRuntime{
			TeacherProfile:            strings.ToLower(strings.TrimSpace(workload.Spec.Distillation.TeacherProfile)),
			BatchSize:                 int(workload.Spec.Distillation.BatchSize),
			TeacherForwardOverheadPct: workload.Spec.Distillation.TeacherForwardOverheadPct,
		}
	}
	if workload.Spec.Jitter != nil {
		seed := int64(0)
		if workload.Spec.Jitter.Seed != nil {
			seed = *workload.Spec.Jitter.Seed
		}
		input.Jitter = &sim.JitterConfig{
			Enabled: workload.Spec.Jitter.Enabled,
			Pct:     workload.Spec.Jitter.Pct,
			Seed:    seed,
		}
	}
	return input
}
