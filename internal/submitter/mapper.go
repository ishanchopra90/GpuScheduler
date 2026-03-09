package submitter

import (
	"github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ToGPUWorkload converts a validated WorkloadRequest into a GPUWorkload for the given namespace.
// The CR name is derived from request_id via CRNameFromRequestID.
func ToGPUWorkload(req *kafka.WorkloadRequest, namespace string) *v1alpha1.GPUWorkload {
	name := CRNameFromRequestID(req.RequestID)
	if name == "" {
		return nil
	}
	spec := v1alpha1.GPUWorkloadSpec{
		Tenant:            req.Tenant,
		Priority:          req.Priority,
		GPUCount:          req.GPUCount,
		GPUMemoryMiB:      req.GPUMemoryMiB,
		Tokens:            req.Tokens,
		Profile:           req.ModelProfile,
		MaxRuntimeSeconds: req.MaxRuntimeSeconds,
		Kind:              v1alpha1.WorkloadKind(req.Kind),
	}
	if req.Training != nil {
		spec.Training = &v1alpha1.TrainingRuntimeSpec{
			GlobalBatchSize: req.Training.GlobalBatchSize,
			MicroBatchSize:  req.Training.MicroBatchSize,
			GradAccumSteps:  req.Training.GradAccumSteps,
			SequenceLength:  req.Training.SequenceLength,
		}
	}
	if req.Inference != nil {
		spec.Inference = &v1alpha1.InferenceRuntimeSpec{
			BatchSize: req.Inference.BatchSize,
		}
	}
	if req.Eval != nil {
		spec.Eval = &v1alpha1.EvalRuntimeSpec{
			BatchSize:             req.Eval.BatchSize,
			MetricOverheadPct:     req.Eval.MetricOverheadPct,
			ValidationOverheadPct: req.Eval.ValidationOverheadPct,
		}
	}
	if req.FineTune != nil {
		spec.FineTune = &v1alpha1.FineTuneRuntimeSpec{
			GlobalBatchSize:           req.FineTune.GlobalBatchSize,
			MicroBatchSize:            req.FineTune.MicroBatchSize,
			GradAccumSteps:            req.FineTune.GradAccumSteps,
			WarmStartOverheadPct:      req.FineTune.WarmStartOverheadPct,
			CheckpointLoadOverheadPct: req.FineTune.CheckpointLoadOverheadPct,
		}
	}
	if req.RLHF != nil {
		spec.RLHF = &v1alpha1.RLHFRuntimeSpec{
			RolloutBatchSize:  req.RLHF.RolloutBatchSize,
			RewardBatchSize:   req.RLHF.RewardBatchSize,
			UpdateBatchSize:   req.RLHF.UpdateBatchSize,
			PolicyUpdateSteps: req.RLHF.PolicyUpdateSteps,
		}
	}
	if req.Embedding != nil {
		spec.Embedding = &v1alpha1.EmbeddingRuntimeSpec{
			BatchSize:       req.Embedding.BatchSize,
			VectorDimension: req.Embedding.VectorDimension,
			AvgTokenLength:  req.Embedding.AvgTokenLength,
		}
	}
	if req.DataPreprocess != nil {
		spec.DataPreprocess = &v1alpha1.DataPreprocessRuntimeSpec{
			InputBytes:              req.DataPreprocess.InputBytes,
			TokenizationOverheadPct: req.DataPreprocess.TokenizationOverheadPct,
			AugmentationOverheadPct: req.DataPreprocess.AugmentationOverheadPct,
		}
	}
	if req.Distillation != nil {
		spec.Distillation = &v1alpha1.DistillationRuntimeSpec{
			TeacherProfile:            req.Distillation.TeacherProfile,
			BatchSize:                 req.Distillation.BatchSize,
			TeacherForwardOverheadPct: req.Distillation.TeacherForwardOverheadPct,
		}
	}
	return &v1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: spec,
	}
}
