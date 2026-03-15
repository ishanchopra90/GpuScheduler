package worker

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

func TestWorkloadToRuntimeInput_BasicFields(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "wl", Namespace: "default"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant:       "team-a",
			Priority:     5,
			GPUCount:     1,
			GPUMemoryMiB: 16000,
			Profile:      "H100_SXM",
			Tokens:       1000,
			Kind:         schedulerv1alpha1.WorkloadKindTraining,
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.WorkloadID != "default/wl" {
		t.Errorf("WorkloadID = %q, want default/wl", got.WorkloadID)
	}
	if got.Tokens != 1000 {
		t.Errorf("Tokens = %d, want 1000", got.Tokens)
	}
	if got.Profile != "h100_sxm" {
		t.Errorf("Profile = %q, want h100_sxm (normalized)", got.Profile)
	}
	if got.Kind != sim.WorkloadKindTraining {
		t.Errorf("Kind = %q, want training", got.Kind)
	}
}

func TestWorkloadToRuntimeInput_Training(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindTraining,
			Training: &schedulerv1alpha1.TrainingRuntimeSpec{
				GlobalBatchSize: 32,
				MicroBatchSize:  8,
				GradAccumSteps:  4,
				SequenceLength:  2048,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Training == nil {
		t.Fatal("Training is nil")
	}
	if got.Training.GlobalBatchSize != 32 || got.Training.MicroBatchSize != 8 ||
		got.Training.GradAccumSteps != 4 || got.Training.SequenceLength != 2048 {
		t.Errorf("Training = %+v", got.Training)
	}
}

func TestWorkloadToRuntimeInput_Inference(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "i", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind:      schedulerv1alpha1.WorkloadKindInference,
			Inference: &schedulerv1alpha1.InferenceRuntimeSpec{BatchSize: 16},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Inference == nil || got.Inference.BatchSize != 16 {
		t.Errorf("Inference = %+v", got.Inference)
	}
}

func TestWorkloadToRuntimeInput_Eval(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "e", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindEval,
			Eval: &schedulerv1alpha1.EvalRuntimeSpec{
				BatchSize:             8,
				MetricOverheadPct:     0.1,
				ValidationOverheadPct: 0.05,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Eval == nil {
		t.Fatal("Eval is nil")
	}
	if got.Eval.BatchSize != 8 || got.Eval.MetricOverheadPct != 0.1 || got.Eval.ValidationOverheadPct != 0.05 {
		t.Errorf("Eval = %+v", got.Eval)
	}
}

func TestWorkloadToRuntimeInput_FineTune(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "f", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindFineTune,
			FineTune: &schedulerv1alpha1.FineTuneRuntimeSpec{
				GlobalBatchSize:           16,
				MicroBatchSize:            4,
				GradAccumSteps:            4,
				WarmStartOverheadPct:      0.1,
				CheckpointLoadOverheadPct: 0.05,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.FineTune == nil {
		t.Fatal("FineTune is nil")
	}
	if got.FineTune.GlobalBatchSize != 16 || got.FineTune.WarmStartOverheadPct != 0.1 {
		t.Errorf("FineTune = %+v", got.FineTune)
	}
}

func TestWorkloadToRuntimeInput_RLHF(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindRLHF,
			RLHF: &schedulerv1alpha1.RLHFRuntimeSpec{
				RolloutBatchSize:  4,
				RewardBatchSize:   8,
				UpdateBatchSize:   2,
				PolicyUpdateSteps: 10,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.RLHF == nil {
		t.Fatal("RLHF is nil")
	}
	if got.RLHF.RolloutBatchSize != 4 || got.RLHF.PolicyUpdateSteps != 10 {
		t.Errorf("RLHF = %+v", got.RLHF)
	}
}

func TestWorkloadToRuntimeInput_Embedding(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "emb", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindEmbedding,
			Embedding: &schedulerv1alpha1.EmbeddingRuntimeSpec{
				BatchSize:       32,
				VectorDimension: 768,
				AvgTokenLength:  128,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Embedding == nil {
		t.Fatal("Embedding is nil")
	}
	if got.Embedding.BatchSize != 32 || got.Embedding.VectorDimension != 768 {
		t.Errorf("Embedding = %+v", got.Embedding)
	}
}

func TestWorkloadToRuntimeInput_DataPreprocess(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "dp", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindDataPreprocess,
			DataPreprocess: &schedulerv1alpha1.DataPreprocessRuntimeSpec{
				InputBytes:              1 << 20,
				TokenizationOverheadPct: 0.1,
				AugmentationOverheadPct: 0.05,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.DataPreprocess == nil {
		t.Fatal("DataPreprocess is nil")
	}
	if got.DataPreprocess.InputBytes != 1<<20 || got.DataPreprocess.TokenizationOverheadPct != 0.1 {
		t.Errorf("DataPreprocess = %+v", got.DataPreprocess)
	}
}

func TestWorkloadToRuntimeInput_Distillation(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "dist", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindDistillation,
			Distillation: &schedulerv1alpha1.DistillationRuntimeSpec{
				TeacherProfile:            "H100_SXM",
				BatchSize:                 8,
				TeacherForwardOverheadPct: 0.2,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Distillation == nil {
		t.Fatal("Distillation is nil")
	}
	if got.Distillation.TeacherProfile != "h100_sxm" || got.Distillation.BatchSize != 8 {
		t.Errorf("Distillation = %+v", got.Distillation)
	}
}

func TestWorkloadToRuntimeInput_Jitter(t *testing.T) {
	seed := int64(42)
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "j", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindTraining,
			Jitter: &schedulerv1alpha1.JitterConfigSpec{
				Enabled: true,
				Pct:     0.1,
				Seed:    &seed,
			},
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Jitter == nil {
		t.Fatal("Jitter is nil")
	}
	if !got.Jitter.Enabled || got.Jitter.Pct != 0.1 || got.Jitter.Seed != 42 {
		t.Errorf("Jitter = %+v", got.Jitter)
	}
}

func TestWorkloadToRuntimeInput_NoOptionalBlocks(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "min", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindInference,
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Training != nil || got.Inference != nil || got.Eval != nil || got.FineTune != nil ||
		got.RLHF != nil || got.Embedding != nil || got.DataPreprocess != nil || got.Distillation != nil || got.Jitter != nil {
		t.Errorf("expected no optional blocks, got %+v", got)
	}
}

func TestWorkloadToRuntimeInput_ProfileNormalization(t *testing.T) {
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: "ns"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant: "t", Priority: 1, GPUCount: 1, GPUMemoryMiB: 1000, Profile: "  A100_80GB  ", Tokens: 1,
			Kind: schedulerv1alpha1.WorkloadKindTraining,
		},
	}
	got := WorkloadToRuntimeInput(w)
	if got.Profile != "a100_80gb" {
		t.Errorf("Profile = %q, want a100_80gb", got.Profile)
	}
}
