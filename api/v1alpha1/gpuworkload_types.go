package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// WorkloadKind indicates the high-level execution mode of a GPU workload.
// It intentionally mirrors the simulator's supported kinds.
//
// +kubebuilder:validation:Enum=training;inference;eval;fine_tune;rlhf;embedding;data_preprocess;distillation
type WorkloadKind string

const (
	WorkloadKindTraining       WorkloadKind = "training"
	WorkloadKindInference      WorkloadKind = "inference"
	WorkloadKindEval           WorkloadKind = "eval"
	WorkloadKindFineTune       WorkloadKind = "fine_tune"
	WorkloadKindRLHF           WorkloadKind = "rlhf"
	WorkloadKindEmbedding      WorkloadKind = "embedding"
	WorkloadKindDataPreprocess WorkloadKind = "data_preprocess"
	WorkloadKindDistillation   WorkloadKind = "distillation"
)

// GPUWorkloadPhase indicates the high-level lifecycle phase of a GPU workload.
//
// +kubebuilder:validation:Enum=Queued;Scheduled;Running;Succeeded;Failed;Preempted
type GPUWorkloadPhase string

const (
	GPUWorkloadPhaseQueued    GPUWorkloadPhase = "Queued"
	GPUWorkloadPhaseScheduled GPUWorkloadPhase = "Scheduled"
	GPUWorkloadPhaseRunning   GPUWorkloadPhase = "Running"
	GPUWorkloadPhaseSucceeded GPUWorkloadPhase = "Succeeded"
	GPUWorkloadPhaseFailed    GPUWorkloadPhase = "Failed"
	GPUWorkloadPhasePreempted GPUWorkloadPhase = "Preempted"
)

// TrainingRuntimeSpec captures training-specific knobs.
type TrainingRuntimeSpec struct {
	// GlobalBatchSize is the effective per-update batch across all devices/steps.
	// +kubebuilder:validation:Minimum=0
	GlobalBatchSize int32 `json:"globalBatchSize"`
	// MicroBatchSize is the per-device batch used in one forward/backward pass.
	// +kubebuilder:validation:Minimum=0
	MicroBatchSize int32 `json:"microBatchSize"`
	// GradAccumSteps is the number of micro-batches accumulated before one update.
	// +kubebuilder:validation:Minimum=0
	GradAccumSteps int32 `json:"gradAccumSteps"`
	// SequenceLength is tokens per sample/sequence.
	// +kubebuilder:validation:Minimum=0
	SequenceLength int32 `json:"sequenceLength"`
}

// InferenceRuntimeSpec captures inference-specific knobs.
type InferenceRuntimeSpec struct {
	// BatchSize controls inference throughput with diminishing returns.
	// +kubebuilder:validation:Minimum=0
	BatchSize int32 `json:"batchSize"`
}

// EvalRuntimeSpec captures evaluation-specific knobs.
type EvalRuntimeSpec struct {
	// BatchSize controls eval throughput similarly to inference, with diminishing returns.
	// +kubebuilder:validation:Minimum=0
	BatchSize int32 `json:"batchSize"`
	// MetricOverheadPct is extra metric-computation cost as a fraction in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	MetricOverheadPct float64 `json:"metricOverheadPct"`
	// ValidationOverheadPct is extra validation/checkpoint cost as a fraction in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	ValidationOverheadPct float64 `json:"validationOverheadPct"`
}

// FineTuneRuntimeSpec captures fine-tuning-specific knobs.
type FineTuneRuntimeSpec struct {
	// GlobalBatchSize is the effective per-update batch across all devices/steps.
	// +kubebuilder:validation:Minimum=0
	GlobalBatchSize int32 `json:"globalBatchSize"`
	// MicroBatchSize is the per-device batch used in one forward/backward pass.
	// +kubebuilder:validation:Minimum=0
	MicroBatchSize int32 `json:"microBatchSize"`
	// GradAccumSteps is the number of micro-batches accumulated before one update.
	// +kubebuilder:validation:Minimum=0
	GradAccumSteps int32 `json:"gradAccumSteps"`
	// WarmStartOverheadPct models checkpoint/model-load overhead as a fraction in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	WarmStartOverheadPct float64 `json:"warmStartOverheadPct"`
	// CheckpointLoadOverheadPct models additional checkpoint restore cost in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	CheckpointLoadOverheadPct float64 `json:"checkpointLoadOverheadPct"`
}

// RLHFRuntimeSpec captures RLHF-specific knobs.
type RLHFRuntimeSpec struct {
	// RolloutBatchSize controls how many rollout samples are generated per step.
	// +kubebuilder:validation:Minimum=0
	RolloutBatchSize int32 `json:"rolloutBatchSize"`
	// RewardBatchSize controls batching for reward-model scoring.
	// +kubebuilder:validation:Minimum=0
	RewardBatchSize int32 `json:"rewardBatchSize"`
	// UpdateBatchSize controls policy-update batch efficiency.
	// +kubebuilder:validation:Minimum=0
	UpdateBatchSize int32 `json:"updateBatchSize"`
	// PolicyUpdateSteps controls how many update steps run per RLHF cycle.
	// +kubebuilder:validation:Minimum=0
	PolicyUpdateSteps int32 `json:"policyUpdateSteps"`
}

// EmbeddingRuntimeSpec captures embedding workload-specific knobs.
type EmbeddingRuntimeSpec struct {
	// BatchSize controls embedding throughput with diminishing returns.
	// +kubebuilder:validation:Minimum=0
	BatchSize int32 `json:"batchSize"`
	// VectorDimension is the output embedding width (for example 768, 1024, 1536).
	// +kubebuilder:validation:Minimum=0
	VectorDimension int32 `json:"vectorDimension"`
	// AvgTokenLength is the average input token length per item.
	// +kubebuilder:validation:Minimum=0
	AvgTokenLength int32 `json:"avgTokenLength"`
}

// DataPreprocessRuntimeSpec captures preprocessing workload-specific knobs.
type DataPreprocessRuntimeSpec struct {
	// InputBytes is total input size in bytes for throughput-based preprocessing cost.
	// +kubebuilder:validation:Minimum=0
	InputBytes int64 `json:"inputBytes"`
	// TokenizationOverheadPct is extra tokenization/parser overhead as a fraction in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	TokenizationOverheadPct float64 `json:"tokenizationOverheadPct"`
	// AugmentationOverheadPct is optional augmentation/transform overhead in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	AugmentationOverheadPct float64 `json:"augmentationOverheadPct"`
}

// DistillationRuntimeSpec captures distillation workload-specific knobs.
type DistillationRuntimeSpec struct {
	// TeacherProfile optionally overrides teacher hardware profile used for influence modeling.
	// +optional
	TeacherProfile string `json:"teacherProfile,omitempty"`
	// BatchSize controls student-side distillation throughput.
	// +kubebuilder:validation:Minimum=0
	BatchSize int32 `json:"batchSize"`
	// TeacherForwardOverheadPct models extra teacher forward-pass cost in [0,1].
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	TeacherForwardOverheadPct float64 `json:"teacherForwardOverheadPct"`
}

// JitterConfigSpec controls optional runtime perturbation.
type JitterConfigSpec struct {
	Enabled bool `json:"enabled"`
	// Pct is a relative fraction. Example: 0.10 means +/-10%.
	// +kubebuilder:default:=0.1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Pct float64 `json:"pct"`
	// Seed enables reproducible jitter when set.
	// +optional
	Seed *int64 `json:"seed,omitempty"`
}

// GPUWorkloadSpec defines the desired state of GPUWorkload
// +kubebuilder:validation:XValidation:rule="self.kind != 'training' || has(self.training)",message="training config must be set when kind is training"
// +kubebuilder:validation:XValidation:rule="self.kind != 'inference' || has(self.inference)",message="inference config must be set when kind is inference"
// +kubebuilder:validation:XValidation:rule="self.kind != 'eval' || has(self.eval)",message="eval config must be set when kind is eval"
// +kubebuilder:validation:XValidation:rule="self.kind != 'fine_tune' || has(self.fineTune)",message="fineTune config must be set when kind is fine_tune"
// +kubebuilder:validation:XValidation:rule="self.kind != 'rlhf' || has(self.rlhf)",message="rlhf config must be set when kind is rlhf"
// +kubebuilder:validation:XValidation:rule="self.kind != 'embedding' || has(self.embedding)",message="embedding config must be set when kind is embedding"
// +kubebuilder:validation:XValidation:rule="self.kind != 'data_preprocess' || has(self.dataPreprocess)",message="dataPreprocess config must be set when kind is data_preprocess"
// +kubebuilder:validation:XValidation:rule="self.kind != 'distillation' || has(self.distillation)",message="distillation config must be set when kind is distillation"
type GPUWorkloadSpec struct {
	// Tenant is the workload owner / fairness identity.
	// +kubebuilder:validation:MinLength=1
	Tenant string `json:"tenant"`

	// Priority orders queued workloads; higher values run first.
	Priority int32 `json:"priority"`

	// GPUCount is the number of devices requested.
	// +kubebuilder:validation:Minimum=1
	GPUCount int32 `json:"gpuCount"`

	// GPUMemoryMiB is the per-device memory requirement in MiB.
	// +kubebuilder:validation:Minimum=0
	GPUMemoryMiB int32 `json:"gpuMemoryMiB"`

	// Tokens is the total token volume used for runtime estimation.
	// +kubebuilder:validation:Minimum=1
	Tokens int64 `json:"tokens"`

	// Profile is the requested hardware profile name (for example "h100_sxm").
	// +kubebuilder:validation:MinLength=1
	Profile string `json:"profile"`

	// MaxRuntimeSeconds is an optional upper bound used for admission control.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxRuntimeSeconds *int64 `json:"maxRuntimeSeconds,omitempty"`

	// Kind selects the runtime model for this workload.
	Kind WorkloadKind `json:"kind"`

	// Kind-specific runtime configuration. These are interpreted based on Kind.
	// They are optional here; coherence and defaults are handled by validation/defaulting logic.
	// +optional
	Training *TrainingRuntimeSpec `json:"training,omitempty"`
	// +optional
	Inference *InferenceRuntimeSpec `json:"inference,omitempty"`
	// +optional
	Eval *EvalRuntimeSpec `json:"eval,omitempty"`
	// +optional
	FineTune *FineTuneRuntimeSpec `json:"fineTune,omitempty"`
	// +optional
	RLHF *RLHFRuntimeSpec `json:"rlhf,omitempty"`
	// +optional
	Embedding *EmbeddingRuntimeSpec `json:"embedding,omitempty"`
	// +optional
	DataPreprocess *DataPreprocessRuntimeSpec `json:"dataPreprocess,omitempty"`
	// +optional
	Distillation *DistillationRuntimeSpec `json:"distillation,omitempty"`

	// Jitter optionally perturbs runtime estimation.
	// +optional
	Jitter *JitterConfigSpec `json:"jitter,omitempty"`
}

// GPUWorkloadStatus defines the observed state of GPUWorkload.
type GPUWorkloadStatus struct {
	// Phase is the current high-level lifecycle state.
	// +optional
	Phase GPUWorkloadPhase `json:"phase,omitempty"`

	// QueuedAt records when the workload first entered the queue.
	// +optional
	QueuedAt *metav1.Time `json:"queuedAt,omitempty"`

	// conditions represent the current state of the GPUWorkload resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GPUWorkload is the Schema for the gpuworkloads API
type GPUWorkload struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GPUWorkload
	// +required
	Spec GPUWorkloadSpec `json:"spec"`

	// status defines the observed state of GPUWorkload
	// +optional
	Status GPUWorkloadStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GPUWorkloadList contains a list of GPUWorkload
type GPUWorkloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GPUWorkload `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUWorkload{}, &GPUWorkloadList{})
}
