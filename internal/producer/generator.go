package producer

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
)

// Kinds and profiles for variety.
var (
	Kinds    = []string{"training", "inference", "eval", "fine_tune", "rlhf", "embedding", "data_preprocess", "distillation"}
	Profiles = []string{"h100_sxm", "a100_80gb", "tpu_v5e_chip"}
)

// Generator produces WorkloadRequest messages with varied tenants, priorities, tokens, and kinds.
type Generator struct {
	Tenants  []string
	Profiles []string
	Kinds    []string
	Counter  int64
	Rand     *rand.Rand
}

// NewGenerator returns a generator that uses the given tenants (at least one) and optional seed.
func NewGenerator(tenants []string, seed int64) *Generator {
	if len(tenants) == 0 {
		tenants = []string{"tenant-a", "tenant-b", "tenant-c"}
	}
	return &Generator{
		Tenants:  tenants,
		Profiles: append([]string(nil), Profiles...),
		Kinds:    append([]string(nil), Kinds...),
		Counter:  0,
		Rand:     rand.New(rand.NewSource(seed)),
	}
}

// SetProfiles overrides the profiles sampled for ModelProfile. Empty or all-blank
// input is ignored and the existing profiles are preserved.
func (g *Generator) SetProfiles(profiles []string) {
	var cleaned []string
	for _, p := range profiles {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		return
	}
	g.Profiles = cleaned
}

// SetKinds overrides the workload kinds sampled for Kind. Empty or all-blank
// input is ignored and the existing kinds are preserved.
func (g *Generator) SetKinds(kinds []string) {
	var cleaned []string
	for _, k := range kinds {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		cleaned = append(cleaned, k)
	}
	if len(cleaned) == 0 {
		return
	}
	g.Kinds = cleaned
}

// Next produces one WorkloadRequest with varied fields.
func (g *Generator) Next() *kafka.WorkloadRequest {
	g.Counter++
	req := &kafka.WorkloadRequest{
		RequestID:    fmt.Sprintf("loadgen-%d-%d", g.Counter, g.Rand.Int63()),
		Tenant:       g.Tenants[g.Rand.Intn(len(g.Tenants))],
		Priority:     int32(g.Rand.Intn(10) + 1),
		GPUCount:     int32(g.Rand.Intn(4) + 1),
		GPUMemoryMiB: []int32{4096, 8192, 16384, 32768, 40960, 81920}[g.Rand.Intn(6)],
		Tokens:       g.Rand.Int63n(5_000_000) + 10_000,
		ModelProfile: g.Profiles[g.Rand.Intn(len(g.Profiles))],
		Kind:         g.Kinds[g.Rand.Intn(len(g.Kinds))],
	}
	// Optional max runtime for some
	if g.Rand.Float32() < 0.3 {
		s := int64(g.Rand.Intn(3600) + 60)
		req.MaxRuntimeSeconds = &s
	}
	setKindPayload(g, req)
	return req
}

func setKindPayload(g *Generator, req *kafka.WorkloadRequest) {
	switch req.Kind {
	case "training":
		req.Training = &kafka.TrainingPayload{
			GlobalBatchSize: int32(gRand(g, 8, 256)),
			MicroBatchSize:  int32(gRand(g, 1, 32)),
			GradAccumSteps:  int32(gRand(g, 1, 8)),
			SequenceLength:  int32(gRand(g, 512, 4096)),
		}
	case "inference":
		req.Inference = &kafka.InferencePayload{
			BatchSize: int32(gRand(g, 1, 64)),
		}
	case "eval":
		req.Eval = &kafka.EvalPayload{
			BatchSize:             int32(gRand(g, 1, 32)),
			MetricOverheadPct:     gRandFloat(g, 0, 0.2),
			ValidationOverheadPct: gRandFloat(g, 0, 0.15),
		}
	case "fine_tune":
		req.FineTune = &kafka.FineTunePayload{
			GlobalBatchSize:           int32(gRand(g, 4, 128)),
			MicroBatchSize:            int32(gRand(g, 1, 16)),
			GradAccumSteps:            int32(gRand(g, 1, 8)),
			WarmStartOverheadPct:      gRandFloat(g, 0, 0.1),
			CheckpointLoadOverheadPct: gRandFloat(g, 0, 0.05),
		}
	case "rlhf":
		req.RLHF = &kafka.RLHFPayload{
			RolloutBatchSize:  int32(gRand(g, 4, 32)),
			RewardBatchSize:   int32(gRand(g, 4, 64)),
			UpdateBatchSize:   int32(gRand(g, 2, 16)),
			PolicyUpdateSteps: int32(gRand(g, 1, 10)),
		}
	case "embedding":
		req.Embedding = &kafka.EmbeddingPayload{
			BatchSize:       int32(gRand(g, 8, 128)),
			VectorDimension: []int32{384, 768, 1024, 1536}[g.Rand.Intn(4)],
			AvgTokenLength:  int32(gRand(g, 32, 512)),
		}
	case "data_preprocess":
		req.DataPreprocess = &kafka.DataPreprocessPayload{
			InputBytes:              g.Rand.Int63n(10_000_000_000) + 1_000_000,
			TokenizationOverheadPct: gRandFloat(g, 0, 0.2),
			AugmentationOverheadPct: gRandFloat(g, 0, 0.1),
		}
	case "distillation":
		req.Distillation = &kafka.DistillationPayload{
			TeacherProfile:            "a100_80gb",
			BatchSize:                 int32(gRand(g, 2, 32)),
			TeacherForwardOverheadPct: gRandFloat(g, 0, 0.3),
		}
	}
}

// gRand returns a random int in [min, max] using the generator's rand.
func gRand(g *Generator, min, max int) int {
	if max <= min {
		return min
	}
	return min + g.Rand.Intn(max-min+1)
}

// gRandFloat returns a random float in [min, max].
//
//nolint:unparam // min is always 0 at call sites but kept for API clarity.
func gRandFloat(g *Generator, min, max float64) float64 {
	return min + g.Rand.Float64()*(max-min)
}
