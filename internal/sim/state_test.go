package sim

import (
	"fmt"
	"testing"
)

func TestNewSimulator_InitializesMaps(t *testing.T) {
	s := NewSimulator()
	if s == nil {
		t.Fatalf("NewSimulator() returned nil")
	}
	if s.allocations == nil {
		t.Fatalf("allocations map is nil")
	}
	if s.runs == nil {
		t.Fatalf("runs map is nil")
	}
}

func TestBuildNodes_Validation(t *testing.T) {
	cases := []struct {
		name string
		spec GPUNodePoolSpec
	}{
		{"NodeCount invalid", GPUNodePoolSpec{NodeCount: 0, DevicesPerNode: 1, MemoryMiBPerDevice: 1}},
		{"DevicesPerNode invalid", GPUNodePoolSpec{NodeCount: 1, DevicesPerNode: 0, MemoryMiBPerDevice: 1}},
		{"MemoryMiBPerDevice invalid", GPUNodePoolSpec{NodeCount: 1, DevicesPerNode: 1, MemoryMiBPerDevice: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildNodes(tc.spec); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestBuildNodes_ShapesAndIDs(t *testing.T) {
	spec := GPUNodePoolSpec{
		NodeCount:          2,
		DevicesPerNode:     3,
		MemoryMiBPerDevice: 16000,
		DeviceType:         "nvidia",
		Profile:            "h100_sxm",
	}
	nodes, err := buildNodes(spec)
	if err != nil {
		t.Fatalf("buildNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].ID != "node-0" || nodes[1].ID != "node-1" {
		t.Fatalf("unexpected node IDs: %q, %q", nodes[0].ID, nodes[1].ID)
	}
	for i, n := range nodes {
		if len(n.Devices) != 3 {
			t.Fatalf("expected 3 devices on %s, got %d", n.ID, len(n.Devices))
		}
		for j, d := range n.Devices {
			wantID := fmt.Sprintf("node-%d/gpu-%d", i, j)
			if d.ID != wantID {
				t.Fatalf("unexpected device ID at [%d][%d]: got %q want %q", i, j, d.ID, wantID)
			}
			if d.MemoryMiB != spec.MemoryMiBPerDevice {
				t.Fatalf("unexpected MemoryMiB at [%d][%d]: got %d want %d", i, j, d.MemoryMiB, spec.MemoryMiBPerDevice)
			}
			if d.DeviceType != spec.DeviceType {
				t.Fatalf("unexpected DeviceType at [%d][%d]: got %q want %q", i, j, d.DeviceType, spec.DeviceType)
			}
			if d.Profile != spec.Profile {
				t.Fatalf("unexpected Profile at [%d][%d]: got %q want %q", i, j, d.Profile, spec.Profile)
			}
		}
	}
}
