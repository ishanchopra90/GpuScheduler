package sim

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Simulator holds mutable simulator state for one or more logical fleets (pools).
// Each pool is keyed by a stable identifier (e.g. namespace/name of GPUNodePool CR).
// Use NewSimulator to create an isolated instance.
type Simulator struct {
	mu          sync.RWMutex
	pools       map[string][]*Node // poolKey -> nodes for that pool
	allocations map[string]*Allocation
	runs        map[string]*runRecord
	nextRunID   int64
}

// FleetUsage summarizes current simulator fleet capacity and utilization.
type FleetUsage struct {
	TotalDevices     int
	AllocatedDevices int
	AvailableDevices int
	// Utilization is in [0,1] when TotalDevices > 0.
	Utilization float64
}

// NewSimulator creates an empty simulator instance.
func NewSimulator() *Simulator {
	return &Simulator{
		pools:       make(map[string][]*Node),
		allocations: make(map[string]*Allocation),
		runs:        make(map[string]*runRecord),
	}
}

// allNodes returns a flattened slice of all nodes across all pools (read lock must be held).
func (s *Simulator) allNodes() []*Node {
	cap := 0
	for _, nodes := range s.pools {
		cap += len(nodes)
	}
	out := make([]*Node, 0, cap)
	for _, nodes := range s.pools {
		out = append(out, nodes...)
	}
	return out
}

// FleetUsage returns a snapshot of current device allocation utilization across all pools.
func (s *Simulator) FleetUsage() FleetUsage {
	if s == nil {
		return FleetUsage{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := s.allNodes()
	total := 0
	for _, n := range nodes {
		total += len(n.Devices)
	}
	allocated := 0
	for _, a := range s.allocations {
		allocated += len(a.DeviceIDs)
	}
	available := total - allocated
	utilization := 0.0
	if total > 0 {
		utilization = float64(allocated) / float64(total)
	}

	return FleetUsage{
		TotalDevices:     total,
		AllocatedDevices: allocated,
		AvailableDevices: available,
		Utilization:      utilization,
	}
}

// FleetUsageForPool returns utilization for the given pool only. ok is false if the pool is not registered.
func (s *Simulator) FleetUsageForPool(poolKey string) (FleetUsage, bool) {
	if s == nil {
		return FleetUsage{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes, ok := s.pools[poolKey]
	if !ok || len(nodes) == 0 {
		return FleetUsage{}, ok
	}
	total := 0
	for _, n := range nodes {
		total += len(n.Devices)
	}
	// Count allocations that use device IDs from this pool.
	// DefaultPoolKey uses legacy IDs (one slash: "node-N/gpu-M"); other pools use "poolKey/node-N/gpu-M".
	allocated := 0
	for _, a := range s.allocations {
		for _, id := range a.DeviceIDs {
			if poolKey == DefaultPoolKey {
				if strings.Count(id, "/") == 1 {
					allocated++
				}
			} else {
				prefix := poolKey + "/"
				if len(id) >= len(prefix) && id[:len(prefix)] == prefix {
					allocated++
				}
			}
		}
	}
	available := total - allocated
	utilization := 0.0
	if total > 0 {
		utilization = float64(allocated) / float64(total)
	}
	return FleetUsage{
		TotalDevices:     total,
		AllocatedDevices: allocated,
		AvailableDevices: available,
		Utilization:      utilization,
	}, true
}

type runRecord struct {
	RunID      string
	WorkloadID string
	Profile    string
	Tokens     int64
	Runtime    RuntimeInput
	StartedAt  time.Time
	EndsAt     time.Time
	Status     RunStatus
}

// buildNodes creates logical nodes with devices from the given spec.
// Node and device IDs are not prefixed (used when poolKey is empty for backward compat).
func buildNodes(spec GPUNodePoolSpec) ([]*Node, error) {
	return buildNodesWithPoolKey(spec, "")
}

// buildNodesWithPoolKey creates logical nodes with device IDs prefixed by poolKey
// so multiple pools can coexist without ID collisions. When poolKey is empty, IDs
// are "node-0/gpu-0" etc. When poolKey is "default/e2e-pool", IDs are
// "default/e2e-pool/node-0/gpu-0", etc.
func buildNodesWithPoolKey(spec GPUNodePoolSpec, poolKey string) ([]*Node, error) {
	if spec.NodeCount <= 0 {
		return nil, fmt.Errorf("NodeCount must be positive, got %d", spec.NodeCount)
	}
	if spec.DevicesPerNode <= 0 {
		return nil, fmt.Errorf("DevicesPerNode must be positive, got %d", spec.DevicesPerNode)
	}
	if spec.MemoryMiBPerDevice <= 0 {
		return nil, fmt.Errorf("MemoryMiBPerDevice must be positive, got %d", spec.MemoryMiBPerDevice)
	}
	// Use no prefix for "" or DefaultPoolKey so single-pool / tests keep legacy "node-0/gpu-0" IDs.
	prefix := ""
	if poolKey != "" && poolKey != DefaultPoolKey {
		prefix = poolKey + "/"
	}
	nodes := make([]*Node, spec.NodeCount)
	for i := 0; i < spec.NodeCount; i++ {
		nodeID := prefix + fmt.Sprintf("node-%d", i)
		devices := make([]*Device, spec.DevicesPerNode)
		for j := 0; j < spec.DevicesPerNode; j++ {
			devices[j] = &Device{
				ID:         fmt.Sprintf("%s/gpu-%d", nodeID, j),
				MemoryMiB:  spec.MemoryMiBPerDevice,
				DeviceType: spec.DeviceType,
				Profile:    spec.Profile,
			}
		}
		nodes[i] = &Node{ID: nodeID, Devices: devices}
	}
	return nodes, nil
}
