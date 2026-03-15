package kafka

// TopicWorkloadSubmit is the Kafka topic for workload submission. Producers publish WorkloadRequest
// messages here; the submitter consumer group reads and creates GPUWorkload CRs.
const TopicWorkloadSubmit = "gpu.workloads.submit"

// TopicWorkloadSubmitDLQ is the dead-letter topic for invalid workload messages. The submitter
// produces failed messages here after validation errors so they can be inspected or reprocessed.
const TopicWorkloadSubmitDLQ = "gpu.workloads.submit.dlq"

// TopicWorkloadClaimable is the Kafka topic for claimable workloads. The operator produces one
// message per workload when it transitions to Phase=Scheduled so workers can discover work
// without maintaining a full informer cache of all GPUWorkloads.
const TopicWorkloadClaimable = "gpu.workloads.claimable"

// WorkloadRequest is the Kafka message schema for workload submission on topic gpu.workloads.submit.
// The submitter consumes these messages and creates GPUWorkload CRs idempotently (CR name from request_id).
// Field names and structure align with GPUWorkloadSpec for straightforward mapping.
type WorkloadRequest struct {
	// RequestID is the unique idempotency key; CR name is derived from this.
	RequestID string `json:"request_id"`

	// Tenant is the workload owner / fairness identity.
	Tenant string `json:"tenant"`

	// Priority orders queued workloads; higher values run first.
	Priority int32 `json:"priority"`

	// GPUCount is the number of devices requested.
	GPUCount int32 `json:"gpuCount"`

	// GPUMemoryMiB is the per-device memory requirement in MiB.
	GPUMemoryMiB int32 `json:"gpuMemoryMiB"`

	// Tokens is the total token volume used for runtime estimation.
	Tokens int64 `json:"tokens"`

	// MaxRuntimeSeconds is an optional upper bound used for admission control.
	MaxRuntimeSeconds *int64 `json:"maxRuntimeSeconds,omitempty"`

	// ModelProfile is the requested hardware profile name (e.g. "h100_sxm"). Maps to spec.profile on the CR.
	ModelProfile string `json:"modelProfile"`

	// Kind selects the runtime model: training, inference, eval, fine_tune, rlhf, embedding, data_preprocess, distillation.
	Kind string `json:"kind"`

	// Per-kind runtime config. Exactly one must be set when Kind matches; coherence is validated by the submitter.
	Training       *TrainingPayload       `json:"training,omitempty"`
	Inference      *InferencePayload      `json:"inference,omitempty"`
	Eval           *EvalPayload           `json:"eval,omitempty"`
	FineTune       *FineTunePayload       `json:"fineTune,omitempty"`
	RLHF           *RLHFPayload           `json:"rlhf,omitempty"`
	Embedding      *EmbeddingPayload      `json:"embedding,omitempty"`
	DataPreprocess *DataPreprocessPayload `json:"dataPreprocess,omitempty"`
	Distillation   *DistillationPayload   `json:"distillation,omitempty"`
}

// TrainingPayload is the per-kind runtime config when kind is "training".
type TrainingPayload struct {
	GlobalBatchSize int32 `json:"globalBatchSize"`
	MicroBatchSize  int32 `json:"microBatchSize"`
	GradAccumSteps  int32 `json:"gradAccumSteps"`
	SequenceLength  int32 `json:"sequenceLength"`
}

// InferencePayload is the per-kind runtime config when kind is "inference".
type InferencePayload struct {
	BatchSize int32 `json:"batchSize"`
}

// EvalPayload is the per-kind runtime config when kind is "eval".
type EvalPayload struct {
	BatchSize             int32   `json:"batchSize"`
	MetricOverheadPct     float64 `json:"metricOverheadPct"`
	ValidationOverheadPct float64 `json:"validationOverheadPct"`
}

// FineTunePayload is the per-kind runtime config when kind is "fine_tune".
type FineTunePayload struct {
	GlobalBatchSize           int32   `json:"globalBatchSize"`
	MicroBatchSize            int32   `json:"microBatchSize"`
	GradAccumSteps            int32   `json:"gradAccumSteps"`
	WarmStartOverheadPct      float64 `json:"warmStartOverheadPct"`
	CheckpointLoadOverheadPct float64 `json:"checkpointLoadOverheadPct"`
}

// RLHFPayload is the per-kind runtime config when kind is "rlhf".
type RLHFPayload struct {
	RolloutBatchSize  int32 `json:"rolloutBatchSize"`
	RewardBatchSize   int32 `json:"rewardBatchSize"`
	UpdateBatchSize   int32 `json:"updateBatchSize"`
	PolicyUpdateSteps int32 `json:"policyUpdateSteps"`
}

// EmbeddingPayload is the per-kind runtime config when kind is "embedding".
type EmbeddingPayload struct {
	BatchSize       int32 `json:"batchSize"`
	VectorDimension int32 `json:"vectorDimension"`
	AvgTokenLength  int32 `json:"avgTokenLength"`
}

// DataPreprocessPayload is the per-kind runtime config when kind is "data_preprocess".
type DataPreprocessPayload struct {
	InputBytes              int64   `json:"inputBytes"`
	TokenizationOverheadPct float64 `json:"tokenizationOverheadPct"`
	AugmentationOverheadPct float64 `json:"augmentationOverheadPct"`
}

// DistillationPayload is the per-kind runtime config when kind is "distillation".
type DistillationPayload struct {
	TeacherProfile            string  `json:"teacherProfile,omitempty"`
	BatchSize                 int32   `json:"batchSize"`
	TeacherForwardOverheadPct float64 `json:"teacherForwardOverheadPct"`
}

// ClaimableWorkloadMessage is the Kafka message schema for the claimable-workloads topic.
// It identifies a single GPUWorkload CR by namespace and name; workers fetch the latest
// object from the API before attempting to claim and run it.
type ClaimableWorkloadMessage struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
