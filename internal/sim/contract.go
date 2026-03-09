package sim

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// This package defines the simulator contract (API) used by the operator and worker pool.
// The simulator is the source of truth for simulated device allocations and models
// accelerator contention, capacity constraints, and runtime estimation.

// RegisterFleet registers or updates one logical fleet pool in the simulator.
// poolKey uniquely identifies the pool (e.g. namespace/name of the GPUNodePool CR).
// Multiple pools can be registered; their devices are combined for allocation.
// Empty poolKey is treated as "_default" for backward compatibility.
//
// The operator calls this when a GPUNodePool CR is created or updated. Idempotent:
// calling again with the same poolKey updates that pool's capacity.
func (s *Simulator) RegisterFleet(poolKey string, spec GPUNodePoolSpec) error {
	if s == nil {
		return fmt.Errorf("simulator is nil")
	}
	if poolKey == "" {
		poolKey = "_default"
	}
	nodes, err := buildNodesWithPoolKey(spec, poolKey)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pools == nil {
		s.pools = make(map[string][]*Node)
	}
	s.pools[poolKey] = nodes
	if s.allocations == nil {
		s.allocations = make(map[string]*Allocation)
	}
	if s.runs == nil {
		s.runs = make(map[string]*runRecord)
	}
	return nil
}

// DeleteFleet removes a pool from the simulator. Call when a GPUNodePool CR is deleted.
// Allocations that reference devices from this pool become invalid; callers should
// release workloads before deleting the pool.
func (s *Simulator) DeleteFleet(poolKey string) {
	if s == nil || poolKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pools != nil {
		delete(s.pools, poolKey)
	}
}

// Allocate reserves simulated devices for a workload.
//
// The operator calls this after the scheduler admits a workload (passes priority,
// fairness, quota, and fit checks). Allocate finds free devices that satisfy
// gpuCount and memMiB (per-device memory requirement), reserves them, and
// returns an Allocation. If capacity is insufficient, returns an error.
//
// Parameters:
// - workloadID: unique identifier for the workload requesting devices.
// - gpuCount: number of devices to reserve (must be > 0).
// - memMiB: per-device memory requirement in MiB (must be > 0).
//
// The workload must not already have an allocation; use Release first if
// re-allocating. After Allocate, the operator typically calls Start to begin
// execution, or Release if the workload is cancelled before starting.
func (s *Simulator) Allocate(workloadID string, gpuCount int, memMiB int) (*Allocation, error) {
	return s.AllocateWithOptions(workloadID, gpuCount, memMiB, AllocationOptions{
		PlacementPolicy: PlacementPolicyFirstFit,
	})
}

// AllocateWithOptions reserves simulated devices for a workload using placement
// policy and constraints from AllocationOptions.
//
// Parameters:
// - workloadID: unique identifier for the workload requesting devices.
// - gpuCount: number of devices to reserve (must be > 0).
// - memMiB: per-device memory requirement in MiB (must be > 0).
// - opts: placement policy and additional constraints/filters (empty policy defaults to first_fit).
func (s *Simulator) AllocateWithOptions(workloadID string, gpuCount int, memMiB int, opts AllocationOptions) (*Allocation, error) {
	if s == nil {
		return nil, fmt.Errorf("simulator is nil")
	}
	if workloadID == "" {
		return nil, fmt.Errorf("workloadID is required")
	}
	if gpuCount <= 0 {
		return nil, fmt.Errorf("gpuCount must be positive, got %d", gpuCount)
	}
	if memMiB <= 0 {
		return nil, fmt.Errorf("memMiB must be positive, got %d", memMiB)
	}
	if s.allocations == nil {
		s.allocations = make(map[string]*Allocation)
	}
	if _, exists := s.allocations[workloadID]; exists {
		return nil, fmt.Errorf("workload %q already has an allocation", workloadID)
	}
	if s.pools == nil || len(s.pools) == 0 {
		return nil, fmt.Errorf("fleet is not registered")
	}
	if opts.MaxNodes < 0 {
		return nil, fmt.Errorf("maxNodes must be >= 0, got %d", opts.MaxNodes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	usedDevices := make(map[string]struct{})
	for _, existing := range s.allocations {
		for _, deviceID := range existing.DeviceIDs {
			usedDevices[deviceID] = struct{}{}
		}
	}

	nodes := s.allNodes()
	perNode := s.computeEligibleDevicesPerNode(nodes, usedDevices, memMiB, opts.PreferredDeviceType, opts.PreferredProfile)

	policy := opts.PlacementPolicy
	if policy == "" {
		policy = PlacementPolicyFirstFit
	}

	deviceIDs, err := pickDevices(perNode, gpuCount, policy, opts.RequireSameNode, opts.MaxNodes)
	if err != nil {
		return nil, fmt.Errorf("placement failed for workload %q: %w", workloadID, err)
	}

	if len(deviceIDs) < gpuCount {
		return nil, fmt.Errorf(
			"insufficient free devices for workload %q: need %d devices with >=%d MiB each, found %d",
			workloadID,
			gpuCount,
			memMiB,
			len(deviceIDs),
		)
	}

	allocation := &Allocation{
		WorkloadID: workloadID,
		DeviceIDs:  deviceIDs,
	}
	s.allocations[workloadID] = allocation
	return allocation, nil
}

type nodeDevices struct {
	nodeID    string
	deviceIDs []string
}

func (s *Simulator) computeEligibleDevicesPerNode(
	nodes []*Node,
	used map[string]struct{},
	memMiB int,
	preferredType string,
	preferredProfile string,
) []nodeDevices {
	out := make([]nodeDevices, 0, len(nodes))
	for _, node := range nodes {
		ids := make([]string, 0, len(node.Devices))
		for _, d := range node.Devices {
			if _, taken := used[d.ID]; taken {
				continue
			}
			if d.MemoryMiB < memMiB {
				continue
			}
			if preferredType != "" && d.DeviceType != preferredType {
				continue
			}
			if preferredProfile != "" && d.Profile != preferredProfile {
				continue
			}
			ids = append(ids, d.ID)
		}
		out = append(out, nodeDevices{nodeID: node.ID, deviceIDs: ids})
	}
	return out
}

func pickDevices(perNode []nodeDevices, need int, policy PlacementPolicy, requireSameNode bool, maxNodes int) ([]string, error) {
	if requireSameNode {
		best := -1
		for i := range perNode {
			if len(perNode[i].deviceIDs) < need {
				continue
			}
			if best == -1 || len(perNode[i].deviceIDs) > len(perNode[best].deviceIDs) {
				best = i
			}
		}
		if best == -1 {
			return nil, fmt.Errorf("requireSameNode=true but no node has %d eligible devices", need)
		}
		return append([]string(nil), perNode[best].deviceIDs[:need]...), nil
	}

	switch policy {
	case PlacementPolicyFirstFit:
		return selectFirstFit(perNode, need, maxNodes), nil
	case PlacementPolicyPack:
		return selectPack(perNode, need, maxNodes), nil
	case PlacementPolicySpread:
		return selectSpread(perNode, need, maxNodes), nil
	case PlacementPolicyLocalityAware:
		// Prefer single-node placement when possible, then fall back to pack.
		for _, n := range perNode {
			if len(n.deviceIDs) >= need {
				return append([]string(nil), n.deviceIDs[:need]...), nil
			}
		}
		return selectPack(perNode, need, maxNodes), nil
	default:
		return nil, fmt.Errorf("unknown placementPolicy %q", policy)
	}
}

func selectFirstFit(perNode []nodeDevices, need int, maxNodes int) []string {
	out := make([]string, 0, need)
	usedNodes := 0
	for _, n := range perNode {
		if len(n.deviceIDs) == 0 {
			continue
		}
		if maxNodes > 0 && usedNodes >= maxNodes {
			break
		}
		usedNodes++
		for _, id := range n.deviceIDs {
			out = append(out, id)
			if len(out) == need {
				return out
			}
		}
	}
	return out
}

func selectPack(perNode []nodeDevices, need int, maxNodes int) []string {
	nodes := append([]nodeDevices(nil), perNode...)
	sort.SliceStable(nodes, func(i, j int) bool {
		return len(nodes[i].deviceIDs) > len(nodes[j].deviceIDs)
	})
	return selectFirstFit(nodes, need, maxNodes)
}

func selectSpread(perNode []nodeDevices, need int, maxNodes int) []string {
	nodes := make([]nodeDevices, 0, len(perNode))
	for _, n := range perNode {
		if len(n.deviceIDs) > 0 {
			nodes = append(nodes, n)
		}
	}
	if maxNodes > 0 && len(nodes) > maxNodes {
		nodes = nodes[:maxNodes]
	}

	out := make([]string, 0, need)
	idx := make([]int, len(nodes))
	for len(out) < need {
		progress := false
		for i := range nodes {
			if idx[i] >= len(nodes[i].deviceIDs) {
				continue
			}
			out = append(out, nodes[i].deviceIDs[idx[i]])
			idx[i]++
			progress = true
			if len(out) == need {
				return out
			}
		}
		if !progress {
			break
		}
	}
	return out
}

// Start begins simulated execution of an allocated workload.
//
// The workload must already have devices reserved via Allocate. Start uses
// tokens (work units, e.g. tokens to process) and profile (hardware profile
// name such as "h100_sxm", "a100_80gb") to estimate runtime from vendor-backed
// specs (memory bandwidth, compute). The simulator models execution and
// completion internally.
//
// Parameters:
// - workloadID: identifier of the workload that already has an allocation.
// - tokens: amount of work to simulate (must be > 0).
// - profile: hardware profile name used for runtime estimation (required; must be known).
//
// Returns a runID used to poll GetStatus for Running|Succeeded|Failed|Preempted.
// The operator/worker calls GetStatus until the run completes, then calls Release
// to free the devices.
func (s *Simulator) Start(workloadID string, tokens int64, profile string) (runID string, err error) {
	return s.StartWithRuntimeInput(RuntimeInput{
		WorkloadID: workloadID,
		Tokens:     tokens,
		Profile:    profile,
		Kind:       WorkloadKindTraining,
	})
}

// StartWithRuntimeInput begins simulated execution using an explicit runtime model input.
// This is the preferred API for mixed workload kinds and runtime tuning knobs.
func (s *Simulator) StartWithRuntimeInput(input RuntimeInput) (runID string, err error) {
	if s == nil {
		return "", fmt.Errorf("simulator is nil")
	}
	if input.WorkloadID == "" {
		return "", fmt.Errorf("workloadID is required")
	}
	if input.Tokens <= 0 {
		return "", fmt.Errorf("tokens must be positive, got %d", input.Tokens)
	}
	if input.Profile == "" {
		return "", fmt.Errorf("profile is required")
	}
	if input.Kind == "" {
		input.Kind = WorkloadKindTraining
	}
	switch input.Kind {
	case WorkloadKindTraining,
		WorkloadKindInference,
		WorkloadKindEval,
		WorkloadKindFineTune,
		WorkloadKindRLHF,
		WorkloadKindEmbedding,
		WorkloadKindDataPreprocess,
		WorkloadKindDistillation:
	default:
		return "", fmt.Errorf("unsupported workload kind %q", input.Kind)
	}
	// Apply per-kind defaults needed by runtime estimators.
	switch input.Kind {
	case WorkloadKindInference:
		if input.Inference == nil {
			input.Inference = &InferenceRuntime{}
		}
		if input.Inference.BatchSize <= 0 {
			input.Inference.BatchSize = 1
		}
	case WorkloadKindEval:
		if input.Eval == nil {
			input.Eval = &EvalRuntime{}
		}
		if input.Eval.BatchSize <= 0 {
			input.Eval.BatchSize = 1
		}
	case WorkloadKindEmbedding:
		if input.Embedding == nil {
			input.Embedding = &EmbeddingRuntime{}
		}
		if input.Embedding.BatchSize <= 0 {
			input.Embedding.BatchSize = 1
		}
		if input.Embedding.VectorDimension <= 0 {
			input.Embedding.VectorDimension = 1024
		}
		if input.Embedding.AvgTokenLength <= 0 {
			input.Embedding.AvgTokenLength = 512
		}
	case WorkloadKindDataPreprocess:
		if input.DataPreprocess == nil {
			input.DataPreprocess = &DataPreprocessRuntime{}
		}
		if input.DataPreprocess.InputBytes <= 0 {
			// Fallback for token-driven callers that do not provide bytes explicitly.
			input.DataPreprocess.InputBytes = input.Tokens * 4
		}
	case WorkloadKindRLHF:
		if input.RLHF == nil {
			input.RLHF = &RLHFRuntime{}
		}
		if input.RLHF.RolloutBatchSize <= 0 {
			input.RLHF.RolloutBatchSize = 1
		}
		if input.RLHF.RewardBatchSize <= 0 {
			// Default reward scoring to rollout batching when not specified.
			input.RLHF.RewardBatchSize = input.RLHF.RolloutBatchSize
		}
		if input.RLHF.UpdateBatchSize <= 0 {
			input.RLHF.UpdateBatchSize = 1
		}
		if input.RLHF.PolicyUpdateSteps <= 0 {
			input.RLHF.PolicyUpdateSteps = 1
		}
	case WorkloadKindDistillation:
		if input.Distillation == nil {
			input.Distillation = &DistillationRuntime{}
		}
		if input.Distillation.BatchSize <= 0 {
			input.Distillation.BatchSize = 1
		}
		if strings.TrimSpace(input.Distillation.TeacherProfile) == "" {
			input.Distillation.TeacherProfile = input.Profile
		}
	}
	if input.Jitter != nil && input.Jitter.Enabled {
		if input.Jitter.Pct <= 0 || input.Jitter.Pct > 1 {
			return "", fmt.Errorf("jitter.pct must be in (0,1], got %v", input.Jitter.Pct)
		}
	}
	if input.Kind == WorkloadKindEval && input.Eval != nil {
		if input.Eval.MetricOverheadPct < 0 || input.Eval.MetricOverheadPct > 1 {
			return "", fmt.Errorf("eval.metricOverheadPct must be in [0,1], got %v", input.Eval.MetricOverheadPct)
		}
		if input.Eval.ValidationOverheadPct < 0 || input.Eval.ValidationOverheadPct > 1 {
			return "", fmt.Errorf("eval.validationOverheadPct must be in [0,1], got %v", input.Eval.ValidationOverheadPct)
		}
	}
	if input.Kind == WorkloadKindFineTune && input.FineTune != nil {
		if input.FineTune.WarmStartOverheadPct < 0 || input.FineTune.WarmStartOverheadPct > 1 {
			return "", fmt.Errorf("fineTune.warmStartOverheadPct must be in [0,1], got %v", input.FineTune.WarmStartOverheadPct)
		}
		if input.FineTune.CheckpointLoadOverheadPct < 0 || input.FineTune.CheckpointLoadOverheadPct > 1 {
			return "", fmt.Errorf("fineTune.checkpointLoadOverheadPct must be in [0,1], got %v", input.FineTune.CheckpointLoadOverheadPct)
		}
	}
	if input.Kind == WorkloadKindDataPreprocess && input.DataPreprocess != nil {
		if input.DataPreprocess.TokenizationOverheadPct < 0 || input.DataPreprocess.TokenizationOverheadPct > 1 {
			return "", fmt.Errorf("dataPreprocess.tokenizationOverheadPct must be in [0,1], got %v", input.DataPreprocess.TokenizationOverheadPct)
		}
		if input.DataPreprocess.AugmentationOverheadPct < 0 || input.DataPreprocess.AugmentationOverheadPct > 1 {
			return "", fmt.Errorf("dataPreprocess.augmentationOverheadPct must be in [0,1], got %v", input.DataPreprocess.AugmentationOverheadPct)
		}
	}
	if input.Kind == WorkloadKindDistillation && input.Distillation != nil {
		if input.Distillation.TeacherForwardOverheadPct < 0 || input.Distillation.TeacherForwardOverheadPct > 1 {
			return "", fmt.Errorf("distillation.teacherForwardOverheadPct must be in [0,1], got %v", input.Distillation.TeacherForwardOverheadPct)
		}
		if _, ok := hardwareProfiles[strings.ToLower(strings.TrimSpace(input.Distillation.TeacherProfile))]; !ok {
			return "", fmt.Errorf("unknown teacher profile %q for distillation", input.Distillation.TeacherProfile)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	allocation, ok := s.allocations[input.WorkloadID]
	if !ok {
		return "", fmt.Errorf("workload %q has no allocation; call Allocate first", input.WorkloadID)
	}
	if len(allocation.DeviceIDs) == 0 {
		return "", fmt.Errorf("workload %q allocation has no devices", input.WorkloadID)
	}
	for _, r := range s.runs {
		if r.WorkloadID == input.WorkloadID && r.Status == RunStatusRunning {
			return "", fmt.Errorf("workload %q already has a running run (%s)", input.WorkloadID, r.RunID)
		}
	}

	duration, err := estimateDuration(input, len(allocation.DeviceIDs))
	if err != nil {
		return "", err
	}

	s.nextRunID++
	runID = fmt.Sprintf("run-%d", s.nextRunID)
	startedAt := time.Now()
	s.runs[runID] = &runRecord{
		RunID:      runID,
		WorkloadID: input.WorkloadID,
		Profile:    input.Profile,
		Tokens:     input.Tokens,
		Runtime:    input,
		StartedAt:  startedAt,
		EndsAt:     startedAt.Add(duration),
		Status:     RunStatusRunning,
	}
	return runID, nil
}

// GetStatus returns the current status of a run started by Start.
//
// The operator polls this after calling Start to drive CR status transitions:
// Running -> update GPUWorkload status to Running; Succeeded/Failed/Preempted
// -> update to the corresponding CR status, then call Release to free devices.
// Preempted is returned after Preempt stops the run. Unknown runID returns an error.
//
// Parameters:
// - runID: identifier returned by Start (required).
func (s *Simulator) GetStatus(runID string) (RunStatus, error) {
	if s == nil {
		return "", fmt.Errorf("simulator is nil")
	}
	if runID == "" {
		return "", fmt.Errorf("runID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.runs[runID]
	if !ok {
		return "", fmt.Errorf("run %q not found", runID)
	}
	if run.Status == RunStatusRunning && !time.Now().Before(run.EndsAt) {
		run.Status = RunStatusSucceeded
		RecordRunDuration(string(run.Runtime.Kind), run.EndsAt.Sub(run.StartedAt).Seconds())
	}
	return run.Status, nil
}

// GetLatestRunStatusForWorkload returns the status of the most recently started
// run for the given workload. Run status is updated lazily on read: if the run
// is still Running but EndsAt has passed, it is marked Succeeded before returning.
func (s *Simulator) GetLatestRunStatusForWorkload(workloadID string) (RunStatus, error) {
	if s == nil {
		return "", fmt.Errorf("simulator is nil")
	}
	if workloadID == "" {
		return "", fmt.Errorf("workloadID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var latest *runRecord
	for _, run := range s.runs {
		if run.WorkloadID != workloadID {
			continue
		}
		if latest == nil || run.StartedAt.After(latest.StartedAt) {
			latest = run
		}
	}
	if latest == nil {
		return "", fmt.Errorf("run for workload %q not found", workloadID)
	}
	if latest.Status == RunStatusRunning && !time.Now().Before(latest.EndsAt) {
		latest.Status = RunStatusSucceeded
		RecordRunDuration(string(latest.Runtime.Kind), latest.EndsAt.Sub(latest.StartedAt).Seconds())
	}
	return latest.Status, nil
}

// Preempt stops a running workload and frees its devices for higher-priority work.
//
// The operator calls this when the scheduler selects a victim for preemption:
// a high-priority workload cannot fit, so the operator chooses the lowest-priority
// running workload, marks it Preempted in CR status, then calls Preempt to stop
// the run and release devices. After Preempt, GetStatus returns Preempted for
// that run, the victim's devices become available for Allocate, and the operator
// calls Release to clean up. Idempotent: no-op if the workload is not running.
//
// Parameters:
// - workloadID: identifier of the workload whose running run (if any) should be preempted (required).
func (s *Simulator) Preempt(workloadID string) error {
	if s == nil {
		return fmt.Errorf("simulator is nil")
	}
	if workloadID == "" {
		return fmt.Errorf("workloadID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, run := range s.runs {
		if run.WorkloadID == workloadID && run.Status == RunStatusRunning {
			run.Status = RunStatusPreempted
			RecordRunDuration(string(run.Runtime.Kind), time.Since(run.StartedAt).Seconds())
			return nil
		}
	}
	return nil
}

// Release frees a workload's device allocation.
//
// The operator calls this to return devices to the pool. Call after a run
// completes (Succeeded/Failed/Preempted from GetStatus), after Preempt, or when
// a workload is cancelled before Start (allocated but never started). Devices
// become available for Allocate. Idempotent: no-op if the workload has no
// allocation.
//
// Parameters:
// - workloadID: identifier of the workload whose allocation should be freed (required).
func (s *Simulator) Release(workloadID string) error {
	if s == nil {
		return fmt.Errorf("simulator is nil")
	}
	if workloadID == "" {
		return fmt.Errorf("workloadID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allocations == nil {
		return nil
	}
	delete(s.allocations, workloadID)
	return nil
}

var profileTokensPerSecond = map[string]float64{
	"h100_sxm":      12000,
	"a100_80gb":     7000,
	"tpu_v5e_chip":  6500,
	"trn1_instance": 5000,
}

var hardwareProfiles map[string]HardwareProfile
var hardwareProfileRuntimeRefs hardwareRuntimeReferences

type hardwareRuntimeReferences struct {
	memBandwidthGBps float64
	peakTFLOPSBF16   float64
	interconnectGBps float64
}

//go:embed hardware_profiles.json
var hardwareProfilesJSON []byte

func init() {
	hardwareProfiles = mustLoadHardwareProfilesFromJSON(hardwareProfilesJSON)
	hardwareProfileRuntimeRefs = deriveHardwareRuntimeReferences(hardwareProfiles)
}

func mustLoadHardwareProfilesFromJSON(b []byte) map[string]HardwareProfile {
	var list []HardwareProfile
	if err := json.Unmarshal(b, &list); err != nil {
		panic(fmt.Errorf("invalid embedded hardware_profiles.json: %w", err))
	}
	out := make(map[string]HardwareProfile, len(list))
	for _, p := range list {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if key == "" {
			panic(fmt.Errorf("invalid hardware profile: missing name"))
		}
		if _, exists := out[key]; exists {
			panic(fmt.Errorf("invalid hardware profile %q: duplicate name", key))
		}
		if p.MemoryMiB <= 0 {
			panic(fmt.Errorf("invalid hardware profile %q: memoryMiB must be > 0", key))
		}
		if p.MemBandwidthGBps <= 0 {
			panic(fmt.Errorf("invalid hardware profile %q: memBandwidthGBps must be > 0", key))
		}
		if strings.TrimSpace(p.Notes) == "" {
			panic(fmt.Errorf("invalid hardware profile %q: notes is required", key))
		}
		if strings.TrimSpace(p.SourceURL) == "" {
			panic(fmt.Errorf("invalid hardware profile %q: source_url is required", key))
		}
		for i, rawURL := range p.SourceURLs {
			if strings.TrimSpace(rawURL) == "" {
				panic(fmt.Errorf("invalid hardware profile %q: source_urls[%d] is empty", key, i))
			}
		}
		p.Name = key // normalize
		out[key] = p
	}
	return out
}

// HardwareProfiles returns a copy of known hardware profiles keyed by profile name.
func HardwareProfiles() map[string]HardwareProfile {
	out := make(map[string]HardwareProfile, len(hardwareProfiles))
	for k, v := range hardwareProfiles {
		out[k] = v
	}
	return out
}

func deriveHardwareRuntimeReferences(profiles map[string]HardwareProfile) hardwareRuntimeReferences {
	mem := make([]float64, 0, len(profiles))
	compute := make([]float64, 0, len(profiles))
	interconnect := make([]float64, 0, len(profiles))
	for _, p := range profiles {
		if p.MemBandwidthGBps > 0 {
			mem = append(mem, p.MemBandwidthGBps)
		}
		if p.PeakTFLOPSBF16 != nil && *p.PeakTFLOPSBF16 > 0 {
			compute = append(compute, *p.PeakTFLOPSBF16)
		}
		if p.InterconnectGBps != nil && *p.InterconnectGBps > 0 {
			interconnect = append(interconnect, *p.InterconnectGBps)
		}
	}
	return hardwareRuntimeReferences{
		memBandwidthGBps: medianFloat64(mem, 1.0),
		peakTFLOPSBF16:   medianFloat64(compute, 1.0),
		interconnectGBps: medianFloat64(interconnect, 1.0),
	}
}

func medianFloat64(values []float64, fallback float64) float64 {
	if len(values) == 0 {
		return fallback
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// estimateDuration converts a workload size (tokens) into a simulated runtime.
//
// It looks up a per-profile throughput (tokens/second), scales it linearly by the
// number of allocated devices (deviceCount), applies a bounded hardware-profile
// adjustment, then computes duration as:
// durationSeconds = ceil(max(1, tokens/(tps*deviceCount))).
//
// Returns an error if the profile is unknown or inputs are non-positive.
func estimateDuration(input RuntimeInput, deviceCount int) (time.Duration, error) {
	if input.Tokens <= 0 {
		return 0, fmt.Errorf("tokens must be positive, got %d", input.Tokens)
	}
	if deviceCount <= 0 {
		return 0, fmt.Errorf("deviceCount must be positive, got %d", deviceCount)
	}
	baseTPS, ok := profileTokensPerSecond[strings.ToLower(input.Profile)]
	if !ok {
		return 0, fmt.Errorf("unknown profile %q for runtime estimation", input.Profile)
	}
	profile, ok := hardwareProfiles[strings.ToLower(input.Profile)]
	if !ok {
		return 0, fmt.Errorf("unknown profile %q for hardware metadata", input.Profile)
	}

	_ = input.Kind
	_ = input.Eval
	_ = input.FineTune
	_ = input.RLHF
	_ = input.Embedding
	_ = input.DataPreprocess
	_ = input.Distillation
	_ = input.Jitter

	hardwareFactor := hardwareThroughputFactor(profile, deviceCount)
	effectiveTPS := baseTPS * hardwareFactor * float64(deviceCount)
	effectiveTPS *= inferenceBatchEfficiencyMultiplier(input)
	effectiveTPS *= evalBatchEfficiencyMultiplier(input)
	effectiveTPS *= embeddingBatchEfficiencyMultiplier(input)
	seconds := float64(input.Tokens) / effectiveTPS
	seconds *= trainingRuntimeSecondsMultiplier(input)
	seconds *= evalRuntimeSecondsMultiplier(input)
	seconds *= fineTuneRuntimeSecondsMultiplier(input)
	seconds *= rlhfRuntimeSecondsMultiplier(input)
	seconds *= embeddingRuntimeSecondsMultiplier(input)
	seconds *= dataPreprocessRuntimeSecondsMultiplier(input)
	seconds *= distillationRuntimeSecondsMultiplier(input)
	if seconds < 1 {
		seconds = 1
	}
	if input.Jitter != nil && input.Jitter.Enabled {
		secondsWithJitter, err := applyRuntimeJitter(seconds, input.Jitter)
		if err != nil {
			return 0, err
		}
		seconds = secondsWithJitter
	}
	return time.Duration(math.Ceil(seconds) * float64(time.Second)), nil
}

// applyRuntimeJitter perturbs a baseline runtime by a bounded random fraction.
//
// Behavior:
//   - Jitter is active only when jitter is non-nil and Enabled=true; otherwise the
//     input runtime is returned unchanged.
//   - Pct must be in (0,1], interpreted as a +/- fraction around baseline runtime.
//     Example: Pct=0.10 means uniform random jitter in [-10%, +10%].
//   - When Seed is non-zero, output is deterministic for identical inputs.
//   - When Seed is zero, a time-based seed is used so each call can vary.
//   - Runtime is floored to 1 second to preserve simulator minimum duration.
func applyRuntimeJitter(seconds float64, jitter *JitterConfig) (float64, error) {
	if jitter == nil || !jitter.Enabled {
		return seconds, nil
	}
	if jitter.Pct <= 0 || jitter.Pct > 1 {
		return 0, fmt.Errorf("jitter.pct must be in (0,1], got %v", jitter.Pct)
	}

	var r *rand.Rand
	if jitter.Seed != 0 {
		r = rand.New(rand.NewSource(jitter.Seed))
	} else {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	// Uniform jitter in [-Pct, +Pct].
	delta := (r.Float64()*2 - 1) * jitter.Pct
	jittered := seconds * (1 + delta)
	if jittered < 1 {
		jittered = 1
	}
	return jittered, nil
}

// inferenceBatchEfficiencyMultiplier models diminishing throughput gains from
// larger inference batch sizes. Batch size 1 yields 1.0x. Larger batches
// increase throughput logarithmically up to a conservative cap.
func inferenceBatchEfficiencyMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindInference {
		return 1.0
	}
	batchSize := 1
	if input.Inference != nil && input.Inference.BatchSize > 0 {
		batchSize = input.Inference.BatchSize
	}
	if batchSize <= 1 {
		return 1.0
	}
	const (
		gainSlope = 0.35
		maxGain   = 2.5
	)
	gain := 1.0 + gainSlope*math.Log2(float64(batchSize))
	if gain > maxGain {
		return maxGain
	}
	return gain
}

// evalBatchEfficiencyMultiplier models diminishing throughput gains from larger
// eval batch sizes. It uses a slightly smaller slope than inference because eval
// paths often include additional framework/metric work that dampens pure batching
// wins. Batch size 1 returns 1.0x.
func evalBatchEfficiencyMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindEval {
		return 1.0
	}
	batchSize := 1
	if input.Eval != nil && input.Eval.BatchSize > 0 {
		batchSize = input.Eval.BatchSize
	}
	if batchSize <= 1 {
		return 1.0
	}
	const (
		gainSlope = 0.25
		maxGain   = 2.0
	)
	gain := 1.0 + gainSlope*math.Log2(float64(batchSize))
	if gain > maxGain {
		return maxGain
	}
	return gain
}

// embeddingBatchEfficiencyMultiplier models diminishing throughput gains from
// larger embedding batches. Gains are conservative because embedding pipelines
// are often bandwidth-bound and can saturate quickly.
func embeddingBatchEfficiencyMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindEmbedding {
		return 1.0
	}
	batchSize := 1
	if input.Embedding != nil && input.Embedding.BatchSize > 0 {
		batchSize = input.Embedding.BatchSize
	}
	if batchSize <= 1 {
		return 1.0
	}
	const (
		gainSlope = 0.22
		maxGain   = 2.1
	)
	gain := 1.0 + gainSlope*math.Log2(float64(batchSize))
	if gain > maxGain {
		return maxGain
	}
	return gain
}

// trainingRuntimeSecondsMultiplier models first-order training runtime effects and
// returns a multiplicative factor applied to estimated seconds.
//
// Interpretation:
// - factor < 1.0 => faster than baseline runtime
// - factor > 1.0 => slower than baseline runtime
//
// Formulas and rationale:
//
//  1. Parallelism ratio:
//     parallelismRatio = globalBatch / (microBatch * gradAccum)
//     This approximates how much effective parallel work exists per update.
//     Higher ratio can improve throughput, so we convert it into:
//     parallelGain = 1 + 0.20*log2(parallelismRatio), capped at 1.8.
//     log2 enforces diminishing returns: each doubling helps less than linear.
//
//  2. Micro-batch gain:
//     microGain = 1 + 0.12*log2(microBatch), capped at 1.5.
//     Larger micro-batches often improve kernel utilization and amortize overhead,
//     but only up to a point; the cap prevents unrealistic speedups.
//
//  3. Throughput gain composition:
//     throughputGain = parallelGain * microGain, clamped to [1.0, 2.5].
//     Multiplication combines two independent "speed-up" contributors. Dividing
//     runtime by this gain makes larger gains reduce total seconds.
//
//  4. Sequence-length penalty:
//     seqPenalty = sqrt(sequenceLength / 2048), clamped to [0.75, 2.5].
//     Longer contexts increase compute and memory traffic. sqrt makes growth
//     sublinear to keep this first-pass model conservative.
//
//  5. Gradient accumulation penalty:
//     gradPenalty = 1 + 0.03*(gradAccum-1), capped at 1.5.
//     More accumulation steps typically add optimizer-step latency and extra pass
//     overhead, so this term increases runtime.
//
// Final combination:
//
//	factor = (seqPenalty * gradPenalty) / throughputGain
//
// then clamped to [0.35, 3.0] for stability.
func trainingRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindTraining {
		return 1.0
	}
	if input.Training == nil {
		return 1.0
	}

	globalBatch := maxInt(input.Training.GlobalBatchSize, 1)
	microBatch := maxInt(input.Training.MicroBatchSize, 1)
	gradAccum := maxInt(input.Training.GradAccumSteps, 1)
	seqLen := maxInt(input.Training.SequenceLength, 2048)

	throughputGain := batchThroughputGain(globalBatch, microBatch, gradAccum, batchThroughputGainConfig{
		parallelSlope:         0.20,
		parallelMax:           1.8,
		microSlope:            0.12,
		microMax:              1.5,
		combinedThroughputMax: 2.5,
	})

	// Longer sequence length and gradient accumulation add runtime overhead.
	seqPenalty := math.Sqrt(float64(seqLen) / 2048.0)
	if seqPenalty < 0.75 {
		seqPenalty = 0.75
	}
	if seqPenalty > 2.5 {
		seqPenalty = 2.5
	}
	gradPenalty := 1.0 + 0.03*float64(gradAccum-1)
	if gradPenalty > 1.5 {
		gradPenalty = 1.5
	}

	factor := (seqPenalty * gradPenalty) / throughputGain
	if factor < 0.35 {
		return 0.35
	}
	if factor > 3.0 {
		return 3.0
	}
	return factor
}

// evalRuntimeSecondsMultiplier models optional eval-only overheads and returns a
// multiplicative factor applied to estimated seconds.
//
// Interpretation:
// - factor = 1.0 means no eval overhead beyond baseline + batch effects.
// - factor > 1.0 slows runtime due to extra metric/validation work.
//
// Formula:
//
//	overheadPenalty = 1 + metricOverheadPct + validationOverheadPct
//
// where each overhead input is expected in [0,1] and validated by StartWithRuntimeInput.
//
// Rationale:
//   - Eval pipelines frequently do extra work after forward passes (metric
//     aggregation, validations/checks), which scales like additive overhead on top
//     of model execution time.
//   - Keeping this term additive and linear makes behavior predictable and easy to
//     calibrate from observed wall-clock runs.
//
// Final factor is clamped to [1.0, 3.0] to avoid extreme slowdowns from malformed
// or overly aggressive overhead inputs.
func evalRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindEval || input.Eval == nil {
		return 1.0
	}
	metric := input.Eval.MetricOverheadPct
	if metric < 0 {
		metric = 0
	}
	validation := input.Eval.ValidationOverheadPct
	if validation < 0 {
		validation = 0
	}
	factor := 1.0 + metric + validation
	if factor < 1.0 {
		return 1.0
	}
	if factor > 3.0 {
		return 3.0
	}
	return factor
}

type batchThroughputGainConfig struct {
	parallelSlope         float64
	parallelMax           float64
	microSlope            float64
	microMax              float64
	combinedThroughputMax float64
}

func batchThroughputGain(globalBatch int, microBatch int, gradAccum int, cfg batchThroughputGainConfig) float64 {
	parallelismRatio := float64(globalBatch) / float64(microBatch*gradAccum)
	if parallelismRatio < 1 {
		parallelismRatio = 1
	}
	parallelGain := 1.0 + cfg.parallelSlope*math.Log2(parallelismRatio)
	if parallelGain > cfg.parallelMax {
		parallelGain = cfg.parallelMax
	}
	microGain := 1.0 + cfg.microSlope*math.Log2(float64(microBatch))
	if microGain > cfg.microMax {
		microGain = cfg.microMax
	}
	throughputGain := parallelGain * microGain
	if throughputGain < 1 {
		throughputGain = 1
	}
	if throughputGain > cfg.combinedThroughputMax {
		throughputGain = cfg.combinedThroughputMax
	}
	return throughputGain
}

// fineTuneRuntimeSecondsMultiplier models fine-tune-specific runtime behavior and
// returns a multiplicative factor applied to estimated seconds.
//
// Interpretation:
// - factor < 1.0 => faster than baseline runtime
// - factor > 1.0 => slower than baseline runtime
//
// Formulas and rationale:
//
//  1. Effective parallelism and micro-batch gains:
//     parallelismRatio = globalBatch / (microBatch * gradAccum), floored at 1
//     parallelGain = 1 + 0.18*log2(parallelismRatio), capped at 1.7
//     microGain    = 1 + 0.10*log2(microBatch), capped at 1.4
//     Fine-tuning usually has less ideal scaling than full pretraining due to
//     smaller datasets and extra host-side orchestration, so slopes/caps are
//     slightly lower than training.
//
//  2. Gradient-accumulation overhead:
//     gradPenalty = 1 + 0.025*(gradAccum-1), capped at 1.4
//     More accumulation steps can improve memory fit but add per-step overhead.
//
//  3. Warm-start/checkpoint overhead:
//     warmPenalty = 1 + warmStartOverheadPct + checkpointLoadOverheadPct
//     These are additive overhead fractions representing startup/restore work.
//
// Final combination:
//
//	factor = (gradPenalty * warmPenalty) / (parallelGain * microGain)
//
// then clamped to [0.4, 3.0] for stability.
func fineTuneRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindFineTune || input.FineTune == nil {
		return 1.0
	}

	globalBatch := maxInt(input.FineTune.GlobalBatchSize, 1)
	microBatch := maxInt(input.FineTune.MicroBatchSize, 1)
	gradAccum := maxInt(input.FineTune.GradAccumSteps, 1)

	throughputGain := batchThroughputGain(globalBatch, microBatch, gradAccum, batchThroughputGainConfig{
		parallelSlope:         0.18,
		parallelMax:           1.7,
		microSlope:            0.10,
		microMax:              1.4,
		combinedThroughputMax: 2.2,
	})

	gradPenalty := 1.0 + 0.025*float64(gradAccum-1)
	if gradPenalty > 1.4 {
		gradPenalty = 1.4
	}

	warmPct := input.FineTune.WarmStartOverheadPct
	if warmPct < 0 {
		warmPct = 0
	}
	ckptPct := input.FineTune.CheckpointLoadOverheadPct
	if ckptPct < 0 {
		ckptPct = 0
	}
	warmPenalty := 1.0 + warmPct + ckptPct
	if warmPenalty > 2.5 {
		warmPenalty = 2.5
	}

	factor := (gradPenalty * warmPenalty) / throughputGain
	if factor < 0.4 {
		return 0.4
	}
	if factor > 3.0 {
		return 3.0
	}
	return factor
}

// rlhfRuntimeSecondsMultiplier models RLHF as three combined stages:
// rollout generation, reward scoring, and policy update. It returns a
// multiplicative factor applied to estimated seconds.
//
// Interpretation:
// - factor = 1.0 means baseline RLHF runtime (minimal stage overheads).
// - factor > 1.0 means slower runtime from heavier RLHF stage work.
//
// Stage formulas and rationale:
//
//  1. Rollout stage:
//     rolloutPenalty = 1 + 0.20/sqrt(rolloutBatch) + 0.015*sqrt(rolloutBatch)
//     Small rollout batches underutilize hardware (first term), while very large
//     batches add sampling/memory pressure (second term), yielding a shallow
//     U-shape with conservative scaling.
//
//  2. Reward scoring stage:
//     rewardPenalty = 1 + 0.22/sqrt(rewardBatch)
//     Reward model scoring is mostly inference-like; larger batches improve
//     utilization with diminishing returns.
//
//  3. Policy update stage:
//     updatePenalty = (1 + 0.18/sqrt(updateBatch)) * (1 + 0.06*(policySteps-1))
//     Larger update batches improve efficiency, while more policy update steps
//     add roughly linear extra work per RLHF cycle.
//
// Final combination:
//
//	factor = 0.40*rolloutPenalty + 0.25*rewardPenalty + 0.35*updatePenalty
//
// then clamped to [0.8, 4.0] to keep this first-pass model stable.
func rlhfRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindRLHF || input.RLHF == nil {
		return 1.0
	}
	rolloutBatch := maxInt(input.RLHF.RolloutBatchSize, 1)
	rewardBatch := maxInt(input.RLHF.RewardBatchSize, 1)
	updateBatch := maxInt(input.RLHF.UpdateBatchSize, 1)
	policySteps := maxInt(input.RLHF.PolicyUpdateSteps, 1)

	rolloutPenalty := 1.0 + 0.20/math.Sqrt(float64(rolloutBatch)) + 0.015*math.Sqrt(float64(rolloutBatch))
	rewardPenalty := 1.0 + 0.22/math.Sqrt(float64(rewardBatch))
	updatePenalty := (1.0 + 0.18/math.Sqrt(float64(updateBatch))) * (1.0 + 0.06*float64(policySteps-1))

	factor := 0.40*rolloutPenalty + 0.25*rewardPenalty + 0.35*updatePenalty
	if factor < 0.8 {
		return 0.8
	}
	if factor > 4.0 {
		return 4.0
	}
	return factor
}

// embeddingRuntimeSecondsMultiplier models embedding-specific scaling from vector
// dimension and average token length, and returns a multiplicative factor applied
// to estimated seconds.
//
// Interpretation:
// - factor = 1.0 corresponds to reference embedding workload settings.
// - factor > 1.0 means slower runtime for larger vectors/longer inputs.
//
// Formula:
//
//	vectorPenalty = sqrt(vectorDimension / 1024), clamped to [0.7, 2.5]
//	tokenPenalty  = sqrt(avgTokenLength / 512), clamped to [0.7, 2.5]
//	factor        = vectorPenalty * tokenPenalty, clamped to [0.6, 3.5]
//
// Rationale:
//   - Larger output vectors require more projection/write bandwidth.
//   - Longer text increases tokenization/encoder work per item.
//   - sqrt keeps this first-pass model conservative while still reflecting
//     monotonic scaling in both dimensions.
func embeddingRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindEmbedding || input.Embedding == nil {
		return 1.0
	}
	vectorDim := maxInt(input.Embedding.VectorDimension, 1024)
	tokenLen := maxInt(input.Embedding.AvgTokenLength, 512)

	vectorPenalty := math.Sqrt(float64(vectorDim) / 1024.0)
	if vectorPenalty < 0.7 {
		vectorPenalty = 0.7
	}
	if vectorPenalty > 2.5 {
		vectorPenalty = 2.5
	}

	tokenPenalty := math.Sqrt(float64(tokenLen) / 512.0)
	if tokenPenalty < 0.7 {
		tokenPenalty = 0.7
	}
	if tokenPenalty > 2.5 {
		tokenPenalty = 2.5
	}

	factor := vectorPenalty * tokenPenalty
	if factor < 0.6 {
		return 0.6
	}
	if factor > 3.5 {
		return 3.5
	}
	return factor
}

// dataPreprocessRuntimeSecondsMultiplier models preprocessing runtime from input
// bytes throughput and optional tokenization/augmentation overhead.
//
// Interpretation:
// - factor = 1.0 corresponds to reference preprocessing volume with no extra overhead.
// - factor > 1.0 means slower runtime due to larger input volume and/or extra transforms.
//
// Formula:
//
//	bytesPenalty    = sqrt(inputBytes / 4_000_000), clamped to [0.6, 4.0]
//	overheadPenalty = 1 + tokenizationOverheadPct + augmentationOverheadPct
//	factor          = bytesPenalty * overheadPenalty, clamped to [0.6, 6.0]
//
// Rationale:
//   - Input-bytes cost is modeled as sublinear sqrt scaling to capture improved
//     batching/I-O amortization while preserving monotonic growth with dataset size.
//   - Tokenization and augmentation are additive overhead classes; linear addition
//     keeps calibration intuitive and mirrors how these pipeline stages stack.
func dataPreprocessRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindDataPreprocess || input.DataPreprocess == nil {
		return 1.0
	}
	inputBytes := input.DataPreprocess.InputBytes
	if inputBytes <= 0 {
		inputBytes = input.Tokens * 4
	}
	if inputBytes <= 0 {
		inputBytes = 4_000_000
	}
	bytesPenalty := math.Sqrt(float64(inputBytes) / 4_000_000.0)
	if bytesPenalty < 0.6 {
		bytesPenalty = 0.6
	}
	if bytesPenalty > 4.0 {
		bytesPenalty = 4.0
	}

	tokenization := input.DataPreprocess.TokenizationOverheadPct
	if tokenization < 0 {
		tokenization = 0
	}
	augmentation := input.DataPreprocess.AugmentationOverheadPct
	if augmentation < 0 {
		augmentation = 0
	}
	overheadPenalty := 1.0 + tokenization + augmentation

	factor := bytesPenalty * overheadPenalty
	if factor < 0.6 {
		return 0.6
	}
	if factor > 6.0 {
		return 6.0
	}
	return factor
}

// distillationRuntimeSecondsMultiplier models distillation-specific runtime from
// teacher forward-pass overhead and teacher-profile capability influence.
//
// Interpretation:
// - factor = 1.0 is baseline distillation runtime.
// - factor > 1.0 means slower runtime from extra teacher work and/or weaker teacher profile.
//
// Formula:
//
//	batchGain       = 1 + 0.20*log2(batchSize), capped at 2.0
//	teacherPenalty  = 1 + teacherForwardOverheadPct
//	profilePenalty  = sqrt(studentTPS / teacherTPS), clamped to [0.75, 1.5]
//	factor          = (teacherPenalty * profilePenalty) / batchGain, clamped to [0.6, 3.5]
//
// Rationale:
//   - Distillation performs extra teacher forward passes, so teacherPenalty captures
//     explicit additional work per step.
//   - If teacher hardware is slower than student hardware, teacher-side latency can
//     bottleneck the pipeline; profilePenalty models this relative effect.
//   - Batch gain provides diminishing throughput improvement on the student side.
func distillationRuntimeSecondsMultiplier(input RuntimeInput) float64 {
	if input.Kind != WorkloadKindDistillation || input.Distillation == nil {
		return 1.0
	}

	batchSize := maxInt(input.Distillation.BatchSize, 1)
	batchGain := 1.0 + 0.20*math.Log2(float64(batchSize))
	if batchGain > 2.0 {
		batchGain = 2.0
	}

	teacherPenalty := 1.0 + input.Distillation.TeacherForwardOverheadPct

	studentTPS, studentOK := profileTokensPerSecond[strings.ToLower(strings.TrimSpace(input.Profile))]
	teacherTPS, teacherOK := profileTokensPerSecond[strings.ToLower(strings.TrimSpace(input.Distillation.TeacherProfile))]
	profilePenalty := 1.0
	if studentOK && teacherOK && teacherTPS > 0 {
		profilePenalty = math.Sqrt(studentTPS / teacherTPS)
		if profilePenalty < 0.75 {
			profilePenalty = 0.75
		}
		if profilePenalty > 1.5 {
			profilePenalty = 1.5
		}
	}

	factor := (teacherPenalty * profilePenalty) / batchGain
	if factor < 0.6 {
		return 0.6
	}
	if factor > 3.5 {
		return 3.5
	}
	return factor
}

func maxInt(v int, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

// hardwareThroughputFactor converts hardware metadata into a bounded multiplicative
// throughput adjustment. The baseline profile TPS table remains the primary
// calibration source; this function only nudges that baseline based on relative
// hardware capability.
//
// Method:
//   - Normalize each metric by the dataset median across known profiles.
//   - Apply sqrt damping so large raw ratio differences do not over-amplify runtime.
//   - Use weighted averaging: memory bandwidth (0.60), BF16 peak compute (0.25),
//     interconnect (0.15, only when deviceCount > 1).
//   - Clamp to [0.75, 1.25] to keep behavior stable until richer fitted models exist.
//
// Rationale for weights:
//   - Memory bandwidth is weighted highest because this simulator's token-throughput
//     abstraction is typically sensitive to memory movement.
//   - Compute is secondary and optional because some profiles omit BF16 peak.
//   - Interconnect is only relevant for multi-device scaling scenarios.
func hardwareThroughputFactor(profile HardwareProfile, deviceCount int) float64 {
	const (
		minFactor = 0.75
		maxFactor = 1.25
	)

	weighted := 0.0
	weights := 0.0

	memRatio := profile.MemBandwidthGBps / hardwareProfileRuntimeRefs.memBandwidthGBps
	if memRatio < 0.1 {
		memRatio = 0.1
	}
	weighted += 0.60 * math.Sqrt(memRatio)
	weights += 0.60

	if profile.PeakTFLOPSBF16 != nil && *profile.PeakTFLOPSBF16 > 0 {
		computeRatio := *profile.PeakTFLOPSBF16 / hardwareProfileRuntimeRefs.peakTFLOPSBF16
		if computeRatio < 0.1 {
			computeRatio = 0.1
		}
		weighted += 0.25 * math.Sqrt(computeRatio)
		weights += 0.25
	}

	if deviceCount > 1 && profile.InterconnectGBps != nil && *profile.InterconnectGBps > 0 {
		interconnectRatio := *profile.InterconnectGBps / hardwareProfileRuntimeRefs.interconnectGBps
		if interconnectRatio < 0.1 {
			interconnectRatio = 0.1
		}
		weighted += 0.15 * math.Sqrt(interconnectRatio)
		weights += 0.15
	}

	if weights <= 0 {
		return 1.0
	}
	factor := weighted / weights
	if factor < minFactor {
		return minFactor
	}
	if factor > maxFactor {
		return maxFactor
	}
	return factor
}
