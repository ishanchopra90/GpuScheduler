package scheduler

import "testing"

func TestTenantGPUsUsed(t *testing.T) {
	running := []RunningWorkload{
		{Tenant: "a", GPUCount: 2},
		{Tenant: "b", GPUCount: 1},
		{Tenant: "a", GPUCount: 3},
	}
	got := TenantGPUsUsed(running)
	if got["a"] != 5 {
		t.Fatalf("tenant a usage mismatch: got %d want %d", got["a"], 5)
	}
	if got["b"] != 1 {
		t.Fatalf("tenant b usage mismatch: got %d want %d", got["b"], 1)
	}
}

func TestExceedsTenantGPUCap(t *testing.T) {
	usage := map[string]int{"a": 6}
	quotas := map[string]TenantQuota{
		"a": {MaxGPUs: 8},
	}
	queued := QueuedWorkload{Tenant: "a", GPUCount: 2}
	if ExceedsTenantGPUCap(queued, usage, quotas) {
		t.Fatalf("expected to be within cap")
	}
	queued.GPUCount = 3
	if !ExceedsTenantGPUCap(queued, usage, quotas) {
		t.Fatalf("expected to exceed cap")
	}
}

func TestExceedsTenantGPUCap_NoQuotaOrZeroMeansNoCap(t *testing.T) {
	usage := map[string]int{"a": 100}
	queued := QueuedWorkload{Tenant: "a", GPUCount: 100}

	if ExceedsTenantGPUCap(queued, usage, map[string]TenantQuota{}) {
		t.Fatalf("expected no quota to mean no cap")
	}
	if ExceedsTenantGPUCap(queued, usage, map[string]TenantQuota{"a": {MaxGPUs: 0}}) {
		t.Fatalf("expected MaxGPUs=0 to mean no cap")
	}
}
