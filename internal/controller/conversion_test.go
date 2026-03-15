package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/scheduler"
)

func TestMapWorkloadKind_AllKinds(t *testing.T) {
	cases := []struct {
		apiKind schedulerv1alpha1.WorkloadKind
		want    scheduler.WorkloadKind
	}{
		{schedulerv1alpha1.WorkloadKindTraining, scheduler.WorkloadKindTraining},
		{schedulerv1alpha1.WorkloadKindInference, scheduler.WorkloadKindInference},
		{schedulerv1alpha1.WorkloadKindEval, scheduler.WorkloadKindEval},
		{schedulerv1alpha1.WorkloadKindFineTune, scheduler.WorkloadKindFineTune},
		{schedulerv1alpha1.WorkloadKindRLHF, scheduler.WorkloadKindRLHF},
		{schedulerv1alpha1.WorkloadKindEmbedding, scheduler.WorkloadKindEmbedding},
		{schedulerv1alpha1.WorkloadKindDataPreprocess, scheduler.WorkloadKindDataPreprocess},
		{schedulerv1alpha1.WorkloadKindDistillation, scheduler.WorkloadKindDistillation},
	}
	for _, tc := range cases {
		t.Run(string(tc.apiKind), func(t *testing.T) {
			got := MapWorkloadKind(tc.apiKind)
			if got != tc.want {
				t.Errorf("MapWorkloadKind(%q) = %q, want %q", tc.apiKind, got, tc.want)
			}
		})
	}
}

func TestMapWorkloadKind_UnknownPassThrough(t *testing.T) {
	got := MapWorkloadKind(schedulerv1alpha1.WorkloadKind("custom"))
	if got != scheduler.WorkloadKind("custom") {
		t.Errorf("MapWorkloadKind(custom) = %q, want custom pass-through", got)
	}
}

func TestGPUWorkloadToQueuedWorkload(t *testing.T) {
	queuedAt := time.Unix(1700000000, 0)
	w := &schedulerv1alpha1.GPUWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "wl", Namespace: "default"},
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant:       "team-a",
			Priority:     10,
			GPUCount:     2,
			GPUMemoryMiB: 16000,
			Profile:      "H100_SXM",
			Tokens:       1000,
			Kind:         schedulerv1alpha1.WorkloadKindTraining,
		},
	}
	workloadID := "default/wl"
	got := GPUWorkloadToQueuedWorkload(w, workloadID, queuedAt)
	if got.WorkloadID != workloadID {
		t.Errorf("WorkloadID = %q, want %q", got.WorkloadID, workloadID)
	}
	if got.Tenant != "team-a" {
		t.Errorf("Tenant = %q, want team-a", got.Tenant)
	}
	if got.Priority != 10 {
		t.Errorf("Priority = %d, want 10", got.Priority)
	}
	if !got.QueuedAt.Equal(queuedAt) {
		t.Errorf("QueuedAt = %v, want %v", got.QueuedAt, queuedAt)
	}
	if got.GPUCount != 2 || got.GPUMemoryMiB != 16000 {
		t.Errorf("GPUCount=%d GPUMemoryMiB=%d, want 2, 16000", got.GPUCount, got.GPUMemoryMiB)
	}
	if got.Profile != "h100_sxm" {
		t.Errorf("Profile = %q, want h100_sxm (normalized)", got.Profile)
	}
	if got.Tokens != 1000 {
		t.Errorf("Tokens = %d, want 1000", got.Tokens)
	}
	if got.Kind != scheduler.WorkloadKindTraining {
		t.Errorf("Kind = %q, want training", got.Kind)
	}
}

func TestGPUWorkloadToQueuedWorkload_AllKinds(t *testing.T) {
	kinds := []schedulerv1alpha1.WorkloadKind{
		schedulerv1alpha1.WorkloadKindTraining,
		schedulerv1alpha1.WorkloadKindInference,
		schedulerv1alpha1.WorkloadKindEval,
		schedulerv1alpha1.WorkloadKindFineTune,
		schedulerv1alpha1.WorkloadKindRLHF,
		schedulerv1alpha1.WorkloadKindEmbedding,
		schedulerv1alpha1.WorkloadKindDataPreprocess,
		schedulerv1alpha1.WorkloadKindDistillation,
	}
	queuedAt := time.Unix(1, 0)
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			w := &schedulerv1alpha1.GPUWorkload{
				Spec: schedulerv1alpha1.GPUWorkloadSpec{
					Tenant:       "t",
					Priority:     1,
					GPUCount:     1,
					GPUMemoryMiB: 1000,
					Profile:      "a100_80gb",
					Tokens:       1,
					Kind:         k,
				},
			}
			got := GPUWorkloadToQueuedWorkload(w, "ns/name", queuedAt)
			if got.Kind != scheduler.WorkloadKind(k) {
				t.Errorf("Kind = %q, want %q", got.Kind, k)
			}
		})
	}
}

func TestGPUWorkloadToRunningWorkload(t *testing.T) {
	startedAt := time.Unix(1700000100, 0)
	w := &schedulerv1alpha1.GPUWorkload{
		Spec: schedulerv1alpha1.GPUWorkloadSpec{
			Tenant:       "team-b",
			Priority:     5,
			GPUCount:     4,
			GPUMemoryMiB: 80000,
			Profile:      "a100_80gb",
			Tokens:       500,
			Kind:         schedulerv1alpha1.WorkloadKindInference,
		},
	}
	workloadID := "default/infer-1"
	got := GPUWorkloadToRunningWorkload(w, workloadID, startedAt)
	if got.WorkloadID != workloadID {
		t.Errorf("WorkloadID = %q, want %q", got.WorkloadID, workloadID)
	}
	if got.Tenant != "team-b" || got.Priority != 5 || got.GPUCount != 4 {
		t.Errorf("Tenant=%q Priority=%d GPUCount=%d", got.Tenant, got.Priority, got.GPUCount)
	}
	if !got.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, startedAt)
	}
	if got.Kind != scheduler.WorkloadKindInference {
		t.Errorf("Kind = %q, want inference", got.Kind)
	}
}
