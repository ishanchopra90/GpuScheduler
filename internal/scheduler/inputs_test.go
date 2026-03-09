package scheduler

import (
	"strings"
	"testing"
	"time"
)

func validQueuedWorkload() QueuedWorkload {
	return QueuedWorkload{
		WorkloadID:   "w-1",
		Tenant:       "team-a",
		Priority:     10,
		QueuedAt:     time.Unix(1700000000, 0),
		GPUCount:     2,
		GPUMemoryMiB: 16000,
		Profile:      "h100_sxm",
		Tokens:       1000,
		Kind:         WorkloadKindTraining,
	}
}

func validRunningWorkload() RunningWorkload {
	return RunningWorkload{
		WorkloadID:   "r-1",
		Tenant:       "team-a",
		Priority:     10,
		StartedAt:    time.Unix(1700000100, 0),
		GPUCount:     2,
		GPUMemoryMiB: 16000,
		Profile:      "h100_sxm",
		Tokens:       1000,
		Kind:         WorkloadKindTraining,
	}
}

func TestSchedulingInputsValidate_AcceptsValidQueuedWorkloads(t *testing.T) {
	in := SchedulingInputs{
		QueuedWorkloads: []QueuedWorkload{
			validQueuedWorkload(),
			{
				WorkloadID:   "w-2",
				Tenant:       "team-b",
				Priority:     1,
				QueuedAt:     time.Unix(1700000001, 0),
				GPUCount:     1,
				GPUMemoryMiB: 8000,
				Profile:      "a100_80gb",
				Tokens:       500,
				Kind:         WorkloadKindInference,
			},
		},
		RunningWorkloads: []RunningWorkload{
			validRunningWorkload(),
		},
		FleetFreeCapacity: FleetFreeCapacity{
			TotalFreeDevices:   4,
			TotalFreeMemoryMiB: 64000,
			ByProfile: map[string]ProfileFreeCapacity{
				"h100_sxm":  {FreeDevices: 2, FreeMemoryMiB: 32000, MemoryMiBPerDevice: 16000},
				"a100_80gb": {FreeDevices: 2, FreeMemoryMiB: 32000, MemoryMiBPerDevice: 16000},
			},
		},
		TenantQuotas: map[string]TenantQuota{
			"team-a": {MaxGPUs: 8, MaxMemoryMiB: 128000, Weight: 1},
			"team-b": {MaxGPUs: 4, MaxMemoryMiB: 64000, Weight: 2},
		},
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSchedulingInputsValidate_RejectsInvalidQueuedWorkloads(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*QueuedWorkload)
		wantErr string
	}{
		{
			name: "missing workloadID",
			mutate: func(w *QueuedWorkload) {
				w.WorkloadID = ""
			},
			wantErr: "workloadID is required",
		},
		{
			name: "missing tenant",
			mutate: func(w *QueuedWorkload) {
				w.Tenant = ""
			},
			wantErr: "tenant is required",
		},
		{
			name: "missing queuedAt",
			mutate: func(w *QueuedWorkload) {
				w.QueuedAt = time.Time{}
			},
			wantErr: "queuedAt is required",
		},
		{
			name: "invalid gpuCount",
			mutate: func(w *QueuedWorkload) {
				w.GPUCount = 0
			},
			wantErr: "gpuCount must be positive",
		},
		{
			name: "invalid gpuMemoryMiB",
			mutate: func(w *QueuedWorkload) {
				w.GPUMemoryMiB = 0
			},
			wantErr: "gpuMemoryMiB must be positive",
		},
		{
			name: "missing profile",
			mutate: func(w *QueuedWorkload) {
				w.Profile = ""
			},
			wantErr: "profile is required",
		},
		{
			name: "missing kind",
			mutate: func(w *QueuedWorkload) {
				w.Kind = ""
			},
			wantErr: "unsupported kind",
		},
		{
			name: "invalid tokens",
			mutate: func(w *QueuedWorkload) {
				w.Tokens = 0
			},
			wantErr: "tokens must be positive",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := validQueuedWorkload()
			tc.mutate(&w)
			err := SchedulingInputs{QueuedWorkloads: []QueuedWorkload{w}}.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error:\n got: %q\nwant substring: %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSchedulingInputsValidate_RejectsDuplicateWorkloadID(t *testing.T) {
	w1 := validQueuedWorkload()
	w2 := validQueuedWorkload()
	w2.Tenant = "team-b"
	w2.QueuedAt = w2.QueuedAt.Add(time.Second)

	err := SchedulingInputs{
		QueuedWorkloads: []QueuedWorkload{w1, w2},
	}.Validate()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate workloadID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSchedulingInputsValidate_RejectsInvalidRunningWorkloads(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RunningWorkload)
		wantErr string
	}{
		{
			name: "missing workloadID",
			mutate: func(w *RunningWorkload) {
				w.WorkloadID = ""
			},
			wantErr: "workloadID is required",
		},
		{
			name: "missing tenant",
			mutate: func(w *RunningWorkload) {
				w.Tenant = ""
			},
			wantErr: "tenant is required",
		},
		{
			name: "missing startedAt",
			mutate: func(w *RunningWorkload) {
				w.StartedAt = time.Time{}
			},
			wantErr: "startedAt is required",
		},
		{
			name: "invalid gpuCount",
			mutate: func(w *RunningWorkload) {
				w.GPUCount = 0
			},
			wantErr: "gpuCount must be positive",
		},
		{
			name: "invalid gpuMemoryMiB",
			mutate: func(w *RunningWorkload) {
				w.GPUMemoryMiB = 0
			},
			wantErr: "gpuMemoryMiB must be positive",
		},
		{
			name: "missing profile",
			mutate: func(w *RunningWorkload) {
				w.Profile = ""
			},
			wantErr: "profile is required",
		},
		{
			name: "invalid tokens",
			mutate: func(w *RunningWorkload) {
				w.Tokens = 0
			},
			wantErr: "tokens must be positive",
		},
		{
			name: "missing kind",
			mutate: func(w *RunningWorkload) {
				w.Kind = ""
			},
			wantErr: "unsupported kind",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := validRunningWorkload()
			tc.mutate(&w)
			err := SchedulingInputs{RunningWorkloads: []RunningWorkload{w}}.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error:\n got: %q\nwant substring: %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSchedulingInputsValidate_RejectsDuplicateAcrossQueuedAndRunning(t *testing.T) {
	q := validQueuedWorkload()
	q.WorkloadID = "same-id"
	r := validRunningWorkload()
	r.WorkloadID = "same-id"

	err := SchedulingInputs{
		QueuedWorkloads:  []QueuedWorkload{q},
		RunningWorkloads: []RunningWorkload{r},
	}.Validate()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate workloadID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSchedulingInputsValidate_RejectsInvalidFleetFreeCapacity(t *testing.T) {
	cases := []struct {
		name    string
		input   SchedulingInputs
		wantErr string
	}{
		{
			name: "negative total free devices",
			input: SchedulingInputs{
				FleetFreeCapacity: FleetFreeCapacity{TotalFreeDevices: -1},
			},
			wantErr: "fleetFreeCapacity.totalFreeDevices must be >= 0",
		},
		{
			name: "negative by profile devices",
			input: SchedulingInputs{
				FleetFreeCapacity: FleetFreeCapacity{
					TotalFreeDevices:   0,
					TotalFreeMemoryMiB: 0,
					ByProfile: map[string]ProfileFreeCapacity{
						"h100_sxm": {FreeDevices: -1, FreeMemoryMiB: 0},
					},
				},
			},
			wantErr: "freeDevices must be >= 0",
		},
		{
			name: "missing per-device memory when devices free",
			input: SchedulingInputs{
				FleetFreeCapacity: FleetFreeCapacity{
					TotalFreeDevices:   1,
					TotalFreeMemoryMiB: 16000,
					ByProfile: map[string]ProfileFreeCapacity{
						"h100_sxm": {FreeDevices: 1, FreeMemoryMiB: 16000, MemoryMiBPerDevice: 0},
					},
				},
			},
			wantErr: "memoryMiBPerDevice must be > 0",
		},
		{
			name: "mismatch total devices",
			input: SchedulingInputs{
				FleetFreeCapacity: FleetFreeCapacity{
					TotalFreeDevices:   5,
					TotalFreeMemoryMiB: 32000,
					ByProfile: map[string]ProfileFreeCapacity{
						"h100_sxm": {FreeDevices: 2, FreeMemoryMiB: 32000, MemoryMiBPerDevice: 16000},
					},
				},
			},
			wantErr: "totalFreeDevices=5 but byProfile sums to 2",
		},
		{
			name: "mismatch total memory",
			input: SchedulingInputs{
				FleetFreeCapacity: FleetFreeCapacity{
					TotalFreeDevices:   2,
					TotalFreeMemoryMiB: 64000,
					ByProfile: map[string]ProfileFreeCapacity{
						"h100_sxm": {FreeDevices: 2, FreeMemoryMiB: 32000, MemoryMiBPerDevice: 16000},
					},
				},
			},
			wantErr: "totalFreeMemoryMiB=64000 but byProfile sums to 32000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error:\n got: %q\nwant substring: %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSchedulingInputsValidate_RejectsInvalidTenantQuotas(t *testing.T) {
	cases := []struct {
		name    string
		input   SchedulingInputs
		wantErr string
	}{
		{
			name: "empty tenant key",
			input: SchedulingInputs{
				TenantQuotas: map[string]TenantQuota{
					"": {MaxGPUs: 1},
				},
			},
			wantErr: "tenantQuotas has empty tenant key",
		},
		{
			name: "negative max gpus",
			input: SchedulingInputs{
				TenantQuotas: map[string]TenantQuota{
					"team-a": {MaxGPUs: -1},
				},
			},
			wantErr: "maxGPUs must be >= 0",
		},
		{
			name: "negative max memory",
			input: SchedulingInputs{
				TenantQuotas: map[string]TenantQuota{
					"team-a": {MaxMemoryMiB: -1},
				},
			},
			wantErr: "maxMemoryMiB must be >= 0",
		},
		{
			name: "negative weight",
			input: SchedulingInputs{
				TenantQuotas: map[string]TenantQuota{
					"team-a": {Weight: -1},
				},
			},
			wantErr: "weight must be >= 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error:\n got: %q\nwant substring: %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSchedulingInputsValidate_AcceptsAllSupportedKinds(t *testing.T) {
	kinds := []WorkloadKind{
		WorkloadKindTraining,
		WorkloadKindInference,
		WorkloadKindEval,
		WorkloadKindFineTune,
		WorkloadKindRLHF,
		WorkloadKindEmbedding,
		WorkloadKindDataPreprocess,
		WorkloadKindDistillation,
	}

	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			in := SchedulingInputs{
				QueuedWorkloads: []QueuedWorkload{
					{
						WorkloadID:   "q1",
						Tenant:       "team-a",
						Priority:     1,
						QueuedAt:     time.Unix(1, 0),
						GPUCount:     1,
						GPUMemoryMiB: 1000,
						Profile:      "h100_sxm",
						Tokens:       1,
						Kind:         k,
					},
				},
				RunningWorkloads: []RunningWorkload{
					{
						WorkloadID:   "r1",
						Tenant:       "team-a",
						Priority:     1,
						StartedAt:    time.Unix(2, 0),
						GPUCount:     1,
						GPUMemoryMiB: 1000,
						Profile:      "h100_sxm",
						Tokens:       1,
						Kind:         k,
					},
				},
				FleetFreeCapacity: FleetFreeCapacity{
					TotalFreeDevices:   1,
					TotalFreeMemoryMiB: 16000,
					ByProfile: map[string]ProfileFreeCapacity{
						"h100_sxm": {FreeDevices: 1, FreeMemoryMiB: 16000, MemoryMiBPerDevice: 16000},
					},
				},
			}
			if err := in.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestApplyVirtualAdmission(t *testing.T) {
	now := time.Unix(1700000200, 0)
	inputs := SchedulingInputs{
		QueuedWorkloads: []QueuedWorkload{
			{WorkloadID: "ns/w1", Tenant: "team-a", Priority: 10, QueuedAt: now, GPUCount: 1, GPUMemoryMiB: 16000, Profile: "h100_sxm", Tokens: 100, Kind: WorkloadKindTraining},
			{WorkloadID: "ns/w2", Tenant: "team-b", Priority: 5, QueuedAt: now, GPUCount: 1, GPUMemoryMiB: 16000, Profile: "h100_sxm", Tokens: 50, Kind: WorkloadKindInference},
		},
		RunningWorkloads: []RunningWorkload{},
		FleetFreeCapacity: FleetFreeCapacity{
			TotalFreeDevices:   2,
			TotalFreeMemoryMiB: 32000,
			ByProfile:          map[string]ProfileFreeCapacity{"h100_sxm": {FreeDevices: 2, FreeMemoryMiB: 32000, MemoryMiBPerDevice: 16000}},
		},
		TenantQuotas: map[string]TenantQuota{"team-a": {MaxGPUs: 4, Weight: 1}, "team-b": {MaxGPUs: 4, Weight: 1}},
	}
	admitted := inputs.QueuedWorkloads[0]
	out, err := ApplyVirtualAdmission(inputs, admitted, now)
	if err != nil {
		t.Fatalf("ApplyVirtualAdmission: %v", err)
	}
	if len(out.QueuedWorkloads) != 1 || out.QueuedWorkloads[0].WorkloadID != "ns/w2" {
		t.Errorf("expected one queued (w2), got %v", out.QueuedWorkloads)
	}
	if len(out.RunningWorkloads) != 1 || out.RunningWorkloads[0].WorkloadID != "ns/w1" {
		t.Errorf("expected one running (w1), got %v", out.RunningWorkloads)
	}
	if out.FleetFreeCapacity.TotalFreeDevices != 1 || out.FleetFreeCapacity.ByProfile["h100_sxm"].FreeDevices != 1 {
		t.Errorf("expected 1 free device after admission, got %d / %d", out.FleetFreeCapacity.TotalFreeDevices, out.FleetFreeCapacity.ByProfile["h100_sxm"].FreeDevices)
	}
}

// TestApplyVirtualAdmission_MixedProfiles verifies that admitting a workload for
// one profile only reduces that profile's capacity; other profiles are unchanged (29.3).
func TestApplyVirtualAdmission_MixedProfiles(t *testing.T) {
	now := time.Unix(1700000200, 0)
	inputs := SchedulingInputs{
		QueuedWorkloads: []QueuedWorkload{
			{WorkloadID: "ns/w-h100", Tenant: "team-a", Priority: 10, QueuedAt: now, GPUCount: 2, GPUMemoryMiB: 16000, Profile: "h100_sxm", Tokens: 100, Kind: WorkloadKindTraining},
		},
		RunningWorkloads: []RunningWorkload{},
		FleetFreeCapacity: FleetFreeCapacity{
			TotalFreeDevices:   4,
			TotalFreeMemoryMiB: 4 * 16000,
			ByProfile: map[string]ProfileFreeCapacity{
				"h100_sxm":  {FreeDevices: 2, FreeMemoryMiB: 2 * 16000, MemoryMiBPerDevice: 16000},
				"a100_80gb": {FreeDevices: 2, FreeMemoryMiB: 2 * 16000, MemoryMiBPerDevice: 16000},
			},
		},
		TenantQuotas: map[string]TenantQuota{"team-a": {MaxGPUs: 8, Weight: 1}},
	}
	if err := inputs.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	admitted := inputs.QueuedWorkloads[0]
	out, err := ApplyVirtualAdmission(inputs, admitted, now)
	if err != nil {
		t.Fatalf("ApplyVirtualAdmission: %v", err)
	}
	if out.FleetFreeCapacity.ByProfile["h100_sxm"].FreeDevices != 0 {
		t.Errorf("expected h100_sxm to have 0 free devices after admitting 2-GPU workload, got %d", out.FleetFreeCapacity.ByProfile["h100_sxm"].FreeDevices)
	}
	if out.FleetFreeCapacity.ByProfile["a100_80gb"].FreeDevices != 2 {
		t.Errorf("expected a100_80gb unchanged (2 free), got %d", out.FleetFreeCapacity.ByProfile["a100_80gb"].FreeDevices)
	}
	if out.FleetFreeCapacity.TotalFreeDevices != 2 {
		t.Errorf("expected total 2 free (only a100 left), got %d", out.FleetFreeCapacity.TotalFreeDevices)
	}
}

func TestQueuedWorkloadByID(t *testing.T) {
	inputs := SchedulingInputs{
		QueuedWorkloads: []QueuedWorkload{
			{WorkloadID: "ns/a", Tenant: "t", Priority: 1, QueuedAt: time.Now(), GPUCount: 1, GPUMemoryMiB: 1000, Profile: "p", Tokens: 1, Kind: WorkloadKindTraining},
		},
	}
	if got := QueuedWorkloadByID(inputs, "ns/a"); got == nil || got.WorkloadID != "ns/a" {
		t.Errorf("QueuedWorkloadByID(ns/a) = %v", got)
	}
	if got := QueuedWorkloadByID(inputs, "ns/missing"); got != nil {
		t.Errorf("QueuedWorkloadByID(ns/missing) = %v, want nil", got)
	}
}
