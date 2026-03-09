package submitter

import (
	"testing"

	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
)

func TestValidate(t *testing.T) {
	validReq := func() *kafka.WorkloadRequest {
		return &kafka.WorkloadRequest{
			RequestID:    "req-1",
			Tenant:       "tenant-a",
			Priority:     5,
			GPUCount:     2,
			GPUMemoryMiB: 8192,
			Tokens:       1000,
			ModelProfile: "h100_sxm",
			Kind:         "inference",
			Inference:    &kafka.InferencePayload{BatchSize: 4},
		}
	}
	t.Run("valid", func(t *testing.T) {
		if err := Validate(validReq()); err != nil {
			t.Errorf("Validate(valid) = %v", err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		if err := Validate(nil); err == nil {
			t.Error("Validate(nil) expected error")
		}
	})
	t.Run("empty request_id", func(t *testing.T) {
		r := validReq()
		r.RequestID = ""
		if err := Validate(r); err == nil {
			t.Error("Validate(empty request_id) expected error")
		}
	})
	t.Run("empty tenant", func(t *testing.T) {
		r := validReq()
		r.Tenant = ""
		if err := Validate(r); err == nil {
			t.Error("Validate(empty tenant) expected error")
		}
	})
	t.Run("invalid kind", func(t *testing.T) {
		r := validReq()
		r.Kind = "unknown"
		if err := Validate(r); err == nil {
			t.Error("Validate(invalid kind) expected error")
		}
	})
	t.Run("kind inference but missing inference config", func(t *testing.T) {
		r := validReq()
		r.Inference = nil
		if err := Validate(r); err == nil {
			t.Error("Validate(kind inference, no config) expected error")
		}
	})
}
