package sim

// GPUNodePoolSpec describes the logical accelerator fleet to register with the simulator.
// It corresponds to the spec of the GPUNodePool CR and defines the capacity the
// scheduler can allocate from (nodes, devices per node, memory per device).
type GPUNodePoolSpec struct {
	// NodeCount is the number of logical nodes in the fleet.
	NodeCount int
	// DevicesPerNode is the number of GPUs/accelerators per node.
	DevicesPerNode int
	// MemoryMiBPerDevice is the memory capacity per device in MiB.
	MemoryMiBPerDevice int
	// DeviceType is the homogeneous device type for this pool (optional).
	DeviceType string
	// Profile is the homogeneous hardware profile for this pool (optional).
	Profile string
}

// HardwareProfile defines minimal vendor-backed hardware metadata used by the
// simulator for fit checks and runtime estimation calibration.
type HardwareProfile struct {
	// Name is the stable profile identifier (for example: "h100_sxm").
	Name string `json:"name"`
	// MemoryMiB is on-device memory capacity in MiB.
	MemoryMiB int `json:"memoryMiB"`
	// MemBandwidthGBps is peak memory bandwidth in GB/s.
	MemBandwidthGBps float64 `json:"memBandwidthGBps"`
	// PeakTFLOPSBF16 is optional peak BF16 compute throughput.
	PeakTFLOPSBF16 *float64 `json:"peakTFLOPSBF16,omitempty"`
	// InterconnectGBps is optional interconnect bandwidth.
	InterconnectGBps *float64 `json:"interconnectGBps,omitempty"`
	// Notes stores context/assumptions for this profile entry.
	Notes string `json:"notes"`
	// SourceURL is the canonical vendor URL used for this entry.
	SourceURL string `json:"source_url"`
	// SourceURLs stores supporting official vendor/doc URLs for this entry.
	SourceURLs []string `json:"source_urls,omitempty"`
}

// Device represents a single GPU/accelerator on a node.
type Device struct {
	ID         string
	MemoryMiB  int
	DeviceType string
	Profile    string
}

// Node represents a logical accelerator node in the fleet.
// Each node hosts devices (GPUs/accelerators).
type Node struct {
	ID      string
	Devices []*Device
}

// Allocation describes the result of reserving simulated devices for a workload.
// The operator uses this to record which devices were assigned and to pass to
// Start when execution begins.
type Allocation struct {
	// WorkloadID identifies the workload this allocation belongs to.
	WorkloadID string
	// DeviceIDs lists the simulated device identifiers that were reserved.
	// Format is implementation-defined (e.g., "node-0/gpu-0", "node-1/gpu-2").
	DeviceIDs []string
}

// PlacementPolicy controls how AllocateWithOptions selects devices.
type PlacementPolicy string

const (
	PlacementPolicyFirstFit      PlacementPolicy = "first_fit"
	PlacementPolicyPack          PlacementPolicy = "pack"
	PlacementPolicySpread        PlacementPolicy = "spread"
	PlacementPolicyLocalityAware PlacementPolicy = "locality_aware"
)

// AllocationOptions controls placement strategy and constraints.
type AllocationOptions struct {
	// PlacementPolicy chooses the selection algorithm.
	// Defaults to first_fit when empty.
	PlacementPolicy PlacementPolicy
	// RequireSameNode enforces all selected devices come from one node.
	RequireSameNode bool
	// MaxNodes limits how many nodes can be used by an allocation.
	// 0 means no limit.
	MaxNodes int
	// PreferredDeviceType filters eligible devices by type when set.
	PreferredDeviceType string
	// PreferredProfile filters eligible devices by profile when set.
	PreferredProfile string
}

// WorkloadKind indicates the high-level execution mode of a workload.
type WorkloadKind string

const (
	// WorkloadKindTraining is full training/pretraining with model weight updates.
	WorkloadKindTraining WorkloadKind = "training"
	// WorkloadKindInference is online or batch serving-style model execution.
	WorkloadKindInference WorkloadKind = "inference"
	// WorkloadKindEval is model evaluation/validation workloads, often inference-like.
	WorkloadKindEval WorkloadKind = "eval"
	// WorkloadKindFineTune is adaptation/fine-tuning of a base model.
	WorkloadKindFineTune WorkloadKind = "fine_tune"
	// WorkloadKindRLHF is preference/rlhf style training with rollout + policy updates.
	WorkloadKindRLHF WorkloadKind = "rlhf"
	// WorkloadKindEmbedding is embedding/vector generation workloads.
	WorkloadKindEmbedding WorkloadKind = "embedding"
	// WorkloadKindDataPreprocess is GPU-accelerated data preprocessing/tokenization style work.
	WorkloadKindDataPreprocess WorkloadKind = "data_preprocess"
	// WorkloadKindDistillation is teacher-student distillation training.
	WorkloadKindDistillation WorkloadKind = "distillation"
)

// RuntimeInput describes workload runtime behavior inputs used by Start.
// It allows modeling mixed workloads and future runtime refinements:
// - batch-efficiency for inference/eval via per-kind batch settings
// - optional jitter via Jitter
type RuntimeInput struct {
	WorkloadID string
	Tokens     int64
	Profile    string

	Kind WorkloadKind

	Training       *TrainingRuntime
	Inference      *InferenceRuntime
	Eval           *EvalRuntime
	FineTune       *FineTuneRuntime
	RLHF           *RLHFRuntime
	Embedding      *EmbeddingRuntime
	DataPreprocess *DataPreprocessRuntime
	Distillation   *DistillationRuntime

	Jitter *JitterConfig
}

// TrainingRuntime captures training-specific knobs.
type TrainingRuntime struct {
	// GlobalBatchSize is the effective per-update batch across all devices/steps.
	GlobalBatchSize int
	// MicroBatchSize is the per-device batch used in one forward/backward pass.
	MicroBatchSize int
	// GradAccumSteps is the number of micro-batches accumulated before one update.
	GradAccumSteps int
	// SequenceLength is tokens per sample/sequence; longer usually costs more runtime.
	SequenceLength int
}

// InferenceRuntime captures inference-specific knobs where batch has strong
// throughput/latency implications.
type InferenceRuntime struct {
	BatchSize int
}

// EvalRuntime captures evaluation-specific knobs.
type EvalRuntime struct {
	// BatchSize controls eval throughput similarly to inference, with diminishing returns.
	BatchSize int
	// MetricOverheadPct is extra metric-computation cost as a fraction in [0,1].
	MetricOverheadPct float64
	// ValidationOverheadPct is extra validation/checkpoint cost as a fraction in [0,1].
	ValidationOverheadPct float64
}

// FineTuneRuntime captures fine-tuning-specific knobs.
type FineTuneRuntime struct {
	// GlobalBatchSize is the effective per-update batch across all devices/steps.
	GlobalBatchSize int
	// MicroBatchSize is the per-device batch used in one forward/backward pass.
	MicroBatchSize int
	// GradAccumSteps is the number of micro-batches accumulated before one update.
	GradAccumSteps int
	// WarmStartOverheadPct models checkpoint/model-load overhead as a fraction in [0,1].
	WarmStartOverheadPct float64
	// CheckpointLoadOverheadPct models additional checkpoint restore cost in [0,1].
	CheckpointLoadOverheadPct float64
}

// RLHFRuntime captures RLHF-specific knobs.
type RLHFRuntime struct {
	// RolloutBatchSize controls how many rollout samples are generated per step.
	RolloutBatchSize int
	// RewardBatchSize controls batching for reward-model scoring.
	RewardBatchSize int
	// UpdateBatchSize controls policy-update batch efficiency.
	UpdateBatchSize int
	// PolicyUpdateSteps controls how many update steps run per RLHF cycle.
	PolicyUpdateSteps int
}

// EmbeddingRuntime captures embedding workload-specific knobs.
type EmbeddingRuntime struct {
	// BatchSize controls embedding throughput with diminishing returns.
	BatchSize int
	// VectorDimension is the output embedding width (for example 768, 1024, 1536).
	VectorDimension int
	// AvgTokenLength is the average input token length per item.
	AvgTokenLength int
}

// DataPreprocessRuntime captures preprocessing workload-specific knobs.
type DataPreprocessRuntime struct {
	// InputBytes is total input size in bytes for throughput-based preprocessing cost.
	InputBytes int64
	// TokenizationOverheadPct is extra tokenization/parser overhead as a fraction in [0,1].
	TokenizationOverheadPct float64
	// AugmentationOverheadPct is optional augmentation/transform overhead in [0,1].
	AugmentationOverheadPct float64
}

// DistillationRuntime captures distillation workload-specific knobs.
type DistillationRuntime struct {
	// TeacherProfile optionally overrides teacher hardware profile used for influence modeling.
	TeacherProfile string
	// BatchSize controls student-side distillation throughput.
	BatchSize int
	// TeacherForwardOverheadPct models extra teacher forward-pass cost in [0,1].
	TeacherForwardOverheadPct float64
}

// JitterConfig controls optional runtime perturbation.
type JitterConfig struct {
	Enabled bool
	// Pct is a relative fraction. Example: 0.10 means +/-10%.
	Pct float64
	// Seed enables reproducible jitter when set.
	Seed int64
}

// RunStatus is the lifecycle state of a simulated workload run.
// Preempted indicates the run was stopped by Preempt to free devices for higher-priority work.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "Running"
	RunStatusSucceeded RunStatus = "Succeeded"
	RunStatusFailed    RunStatus = "Failed"
	RunStatusPreempted RunStatus = "Preempted"
)
