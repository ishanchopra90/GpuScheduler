package scheduler

import (
	"reflect"
	"testing"
	"time"
)

func q(workloadID string, priority int32, queuedAt int64) QueuedWorkload {
	return QueuedWorkload{
		WorkloadID:   workloadID,
		Tenant:       "team-a",
		Priority:     priority,
		QueuedAt:     time.Unix(queuedAt, 0),
		GPUCount:     1,
		GPUMemoryMiB: 1000,
		Profile:      "h100_sxm",
		Tokens:       100,
		Kind:         WorkloadKindTraining,
	}
}

func TestOrderQueuedByPriority_SortsDescending(t *testing.T) {
	in := []QueuedWorkload{
		q("w-low", 1, 10),
		q("w-high", 10, 20),
		q("w-mid", 5, 30),
	}
	got := OrderQueued(in)
	wantIDs := []string{"w-high", "w-mid", "w-low"}
	gotIDs := []string{got[0].WorkloadID, got[1].WorkloadID, got[2].WorkloadID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("order mismatch:\n got: %v\nwant: %v", gotIDs, wantIDs)
	}
}

func TestOrderQueued_FIFOTieBreakWithinPriority(t *testing.T) {
	in := []QueuedWorkload{
		q("w-b", 5, 20),
		q("w-a", 5, 10),
		q("w-c", 5, 30),
	}
	got := OrderQueued(in)
	wantIDs := []string{"w-a", "w-b", "w-c"}
	gotIDs := []string{got[0].WorkloadID, got[1].WorkloadID, got[2].WorkloadID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("stable order mismatch:\n got: %v\nwant: %v", gotIDs, wantIDs)
	}
}

func TestOrderQueued_DoesNotMutateInput(t *testing.T) {
	in := []QueuedWorkload{
		q("w-low", 1, 10),
		q("w-high", 10, 20),
	}
	orig := append([]QueuedWorkload(nil), in...)
	_ = OrderQueued(in)
	if !reflect.DeepEqual(in, orig) {
		t.Fatalf("input slice mutated:\n got: %#v\nwant: %#v", in, orig)
	}
}

func TestOrderQueued_IsStableForFullyEqualKeys(t *testing.T) {
	in := []QueuedWorkload{
		q("w-a", 5, 10),
		q("w-b", 5, 10),
		q("w-c", 5, 10),
	}
	got := OrderQueued(in)
	wantIDs := []string{"w-a", "w-b", "w-c"}
	gotIDs := []string{got[0].WorkloadID, got[1].WorkloadID, got[2].WorkloadID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("stable order mismatch:\n got: %v\nwant: %v", gotIDs, wantIDs)
	}
}
