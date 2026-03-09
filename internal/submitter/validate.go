package submitter

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
)

// Valid workload kinds; must match api/v1alpha1.WorkloadKind and kafka message values.
var validKinds = map[string]bool{
	"training": true, "inference": true, "eval": true, "fine_tune": true,
	"rlhf": true, "embedding": true, "data_preprocess": true, "distillation": true,
}

// Validate checks the message and kind-specific payload coherence.
// Returns an error suitable for logging and DLQ.
func Validate(req *kafka.WorkloadRequest) error {
	if req == nil {
		return errors.New("message is nil")
	}
	if strings.TrimSpace(req.RequestID) == "" {
		return errors.New("request_id is required and must be non-empty")
	}
	if strings.TrimSpace(req.Tenant) == "" {
		return errors.New("tenant is required and must be non-empty")
	}
	if req.GPUCount < 1 {
		return errors.New("gpuCount must be at least 1")
	}
	if req.GPUMemoryMiB < 0 {
		return errors.New("gpuMemoryMiB must be non-negative")
	}
	if req.Tokens < 1 {
		return errors.New("tokens must be at least 1")
	}
	if req.MaxRuntimeSeconds != nil && *req.MaxRuntimeSeconds < 1 {
		return errors.New("maxRuntimeSeconds must be at least 1 when set")
	}
	if strings.TrimSpace(req.ModelProfile) == "" {
		return errors.New("modelProfile is required and must be non-empty")
	}
	kind := strings.TrimSpace(strings.ToLower(req.Kind))
	if kind == "" {
		return errors.New("kind is required")
	}
	if !validKinds[kind] {
		return fmt.Errorf("kind %q is not valid; must be one of: training, inference, eval, fine_tune, rlhf, embedding, data_preprocess, distillation", req.Kind)
	}
	// Per-kind: exactly the matching payload must be set (coherence).
	switch kind {
	case "training":
		if req.Training == nil {
			return errors.New("kind is training but training config is missing")
		}
	case "inference":
		if req.Inference == nil {
			return errors.New("kind is inference but inference config is missing")
		}
	case "eval":
		if req.Eval == nil {
			return errors.New("kind is eval but eval config is missing")
		}
	case "fine_tune":
		if req.FineTune == nil {
			return errors.New("kind is fine_tune but fineTune config is missing")
		}
	case "rlhf":
		if req.RLHF == nil {
			return errors.New("kind is rlhf but rlhf config is missing")
		}
	case "embedding":
		if req.Embedding == nil {
			return errors.New("kind is embedding but embedding config is missing")
		}
	case "data_preprocess":
		if req.DataPreprocess == nil {
			return errors.New("kind is data_preprocess but dataPreprocess config is missing")
		}
	case "distillation":
		if req.Distillation == nil {
			return errors.New("kind is distillation but distillation config is missing")
		}
	}
	return nil
}
