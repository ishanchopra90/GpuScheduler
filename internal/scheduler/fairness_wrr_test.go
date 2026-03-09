package scheduler

import (
	"reflect"
	"testing"
	"time"
)

func qw(id, tenant string, priority int32, queuedAt int64) QueuedWorkload {
	return QueuedWorkload{
		WorkloadID:   id,
		Tenant:       tenant,
		Priority:     priority,
		QueuedAt:     time.Unix(queuedAt, 0),
		GPUCount:     1,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}
}

func TestWeightedRoundRobinByTenant_PreservesFIFOWithinTenant(t *testing.T) {
	in := []QueuedWorkload{
		qw("a1", "a", 10, 1),
		qw("a2", "a", 9, 2),
		qw("b1", "b", 8, 3),
		qw("a3", "a", 7, 4),
	}
	got := WeightedRoundRobinByTenant(in, map[string]TenantQuota{
		"a": {Weight: 1},
		"b": {Weight: 1},
	})

	// For equal weights, expect alternating per round but FIFO within tenant.
	ids := []string{got[0].WorkloadID, got[1].WorkloadID, got[2].WorkloadID, got[3].WorkloadID}
	want := []string{"a1", "b1", "a2", "a3"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order mismatch:\n got: %v\nwant: %v", ids, want)
	}
}

func TestWeightedRoundRobinByTenant_RespectsWeights(t *testing.T) {
	in := []QueuedWorkload{
		qw("a1", "a", 10, 1),
		qw("a2", "a", 9, 2),
		qw("a3", "a", 8, 3),
		qw("b1", "b", 7, 4),
		qw("b2", "b", 6, 5),
		qw("b3", "b", 5, 6),
	}
	got := WeightedRoundRobinByTenant(in, map[string]TenantQuota{
		"a": {Weight: 2},
		"b": {Weight: 1},
	})
	ids := []string{}
	for _, w := range got {
		ids = append(ids, w.WorkloadID)
	}
	// With weights 2:1, each round should take 2 from a (if available) then 1 from b.
	wantPrefix := []string{"a1", "a2", "b1", "a3", "b2"}
	if len(ids) < len(wantPrefix) || !reflect.DeepEqual(ids[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("weighted prefix mismatch:\n got: %v\nwant prefix: %v", ids, wantPrefix)
	}
}

func TestWeightedRoundRobinByTenant_DefaultsWeightToOne(t *testing.T) {
	in := []QueuedWorkload{
		qw("a1", "a", 10, 1),
		qw("b1", "b", 9, 2),
		qw("a2", "a", 8, 3),
		qw("b2", "b", 7, 4),
	}
	got := WeightedRoundRobinByTenant(in, map[string]TenantQuota{
		"a": {Weight: 0},
		// b missing => defaults to 1
	})
	ids := []string{got[0].WorkloadID, got[1].WorkloadID, got[2].WorkloadID, got[3].WorkloadID}
	want := []string{"a1", "b1", "a2", "b2"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order mismatch:\n got: %v\nwant: %v", ids, want)
	}
}
