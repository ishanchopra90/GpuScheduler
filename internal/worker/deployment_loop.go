package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ishanchopra/gpu-scheduler/internal/sim"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
)

type workloadClaimPatch struct {
	Metadata *struct {
		Annotations map[string]string `json:"annotations,omitempty"`
	} `json:"metadata,omitempty"`
	Status *struct {
		Phase schedulerv1alpha1.GPUWorkloadPhase `json:"phase,omitempty"`
	} `json:"status,omitempty"`
}

// DeploymentLoopConfig configures the scale-focused worker deployment loop.
type DeploymentLoopConfig struct {
	Client       client.Client
	SimClient    SimulatorClient
	Namespace    string
	WorkerID     string
	PollInterval time.Duration
	// MaxConcurrentWorkloads is the max number of workloads to run concurrently in this pod.
	// Only 1 is supported (one workload at a time). Scale horizontally by adding pods.
	MaxConcurrentWorkloads int
	// LogCompletionTimestamps when true logs "Workload <id> completed at <RFC3339> status=..." for each completion.
	// Enable only for demos/validation (e.g. mixed-kinds) to avoid log volume at scale.
	LogCompletionTimestamps bool
}

// RunDeploymentLoop lists GPUWorkloads in Phase=Scheduled, claims one safely (annotation + status
// update), runs Allocate/Start and polls until completion, then updates workload Phase to
// Succeeded or Failed. Loops until ctx is done.
// Concurrency model: one workload at a time per pod (MaxConcurrentWorkloads=1).
func RunDeploymentLoop(ctx context.Context, cfg DeploymentLoopConfig) {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.MaxConcurrentWorkloads <= 0 {
		cfg.MaxConcurrentWorkloads = DefaultMaxConcurrentWorkloads
	}
	log.Printf("Worker %s started, polling namespace %s for Phase=Scheduled workloads every %v", cfg.WorkerID, cfg.Namespace, cfg.PollInterval)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		workload, err := findAndClaimScheduledWorkload(ctx, cfg)
		if err != nil {
			log.Printf("Find/claim workload: %v", err)
			sleepOrDone(ctx, cfg.PollInterval)
			continue
		}
		if workload == nil {
			sleepOrDone(ctx, cfg.PollInterval)
			continue
		}
		runOneWorkload(ctx, cfg, workload)
	}
}

func findAndClaimScheduledWorkload(ctx context.Context, cfg DeploymentLoopConfig) (*schedulerv1alpha1.GPUWorkload, error) {
	var list schedulerv1alpha1.GPUWorkloadList
	listOpts := []client.ListOption{client.InNamespace(cfg.Namespace)}
	if err := cfg.Client.List(ctx, &list, listOpts...); err != nil {
		return nil, err
	}
	for i := range list.Items {
		w := &list.Items[i]
		if w.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseScheduled {
			continue
		}
		if _, claimed := w.Annotations[AnnotationClaimedBy]; claimed {
			continue
		}
		if err := claimWorkload(ctx, cfg, w); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return nil, err
		}
		return w, nil
	}
	return nil, nil
}

func claimWorkload(ctx context.Context, cfg DeploymentLoopConfig, workload *schedulerv1alpha1.GPUWorkload) error {
	// Re-fetch to get latest resource version before patch.
	key := client.ObjectKeyFromObject(workload)
	if err := cfg.Client.Get(ctx, key, workload); err != nil {
		return err
	}
	if workload.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseScheduled {
		return nil
	}
	annotations := make(map[string]string)
	for k, v := range workload.Annotations {
		annotations[k] = v
	}
	annotations[AnnotationClaimedBy] = cfg.WorkerID
	patch := workloadClaimPatch{
		Metadata: &struct {
			Annotations map[string]string `json:"annotations,omitempty"`
		}{Annotations: annotations},
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if err := cfg.Client.Patch(ctx, workload, client.RawPatch(types.MergePatchType, data)); err != nil {
		return err
	}
	// GPUWorkload has status subresource; PATCH to the main resource does not update status.
	// Set Phase=Running via status update so the object reflects the claim.
	if err := cfg.Client.Get(ctx, key, workload); err != nil {
		return err
	}
	workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseRunning
	return cfg.Client.Status().Update(ctx, workload)
}

func runOneWorkload(ctx context.Context, cfg DeploymentLoopConfig, workload *schedulerv1alpha1.GPUWorkload) {
	workloadID := workload.Namespace + "/" + workload.Name
	input := WorkloadToRuntimeInput(workload)
	gpuCount := int(workload.Spec.GPUCount)
	memMiB := int(workload.Spec.GPUMemoryMiB)

	profile := workload.Spec.Profile
	if err := cfg.SimClient.Allocate(workloadID, gpuCount, memMiB, profile); err != nil {
		log.Printf("Allocate %s: %v", workloadID, err)
		setWorkloadPhase(ctx, cfg.Client, workload, schedulerv1alpha1.GPUWorkloadPhaseFailed)
		sleepOrDone(ctx, AllocateStartFailureBackoff)
		return
	}
	runID, err := cfg.SimClient.Start(input)
	if err != nil {
		log.Printf("Start %s: %v", workloadID, err)
		setWorkloadPhase(ctx, cfg.Client, workload, schedulerv1alpha1.GPUWorkloadPhaseFailed)
		releaseRemoteAllocation(cfg.SimClient, workloadID)
		sleepOrDone(ctx, AllocateStartFailureBackoff)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		status, err := cfg.SimClient.GetRunStatus(runID)
		if err != nil {
			log.Printf("GetRunStatus %s: %v", runID, err)
			time.Sleep(2 * time.Second)
			continue
		}
		switch status {
		case sim.RunStatusRunning:
			time.Sleep(2 * time.Second)
			continue
		case sim.RunStatusSucceeded:
			if cfg.LogCompletionTimestamps {
				log.Printf("Workload %s completed at %s status=Succeeded", workloadID, time.Now().UTC().Format(time.RFC3339))
			}
			setWorkloadPhase(ctx, cfg.Client, workload, schedulerv1alpha1.GPUWorkloadPhaseSucceeded)
			releaseRemoteAllocation(cfg.SimClient, workloadID)
			return
		case sim.RunStatusFailed:
			if cfg.LogCompletionTimestamps {
				log.Printf("Workload %s completed at %s status=Failed", workloadID, time.Now().UTC().Format(time.RFC3339))
			}
			setWorkloadPhase(ctx, cfg.Client, workload, schedulerv1alpha1.GPUWorkloadPhaseFailed)
			releaseRemoteAllocation(cfg.SimClient, workloadID)
			return
		case sim.RunStatusPreempted:
			if cfg.LogCompletionTimestamps {
				log.Printf("Workload %s completed at %s status=Preempted", workloadID, time.Now().UTC().Format(time.RFC3339))
			}
			releaseRemoteAllocation(cfg.SimClient, workloadID)
			return
		default:
			time.Sleep(2 * time.Second)
		}
	}
}

// releaseRemoteAllocation frees the workload's devices in the remote simulator so the next workload can allocate.
// Logs and ignores errors so phase update is not blocked.
func releaseRemoteAllocation(simClient SimulatorClient, workloadID string) {
	if simClient == nil {
		return
	}
	if err := simClient.Release(workloadID); err != nil {
		log.Printf("Release %s on remote simulator: %v (devices may stay allocated)", workloadID, err)
	}
}

func setWorkloadPhase(ctx context.Context, c client.Client, workload *schedulerv1alpha1.GPUWorkload, phase schedulerv1alpha1.GPUWorkloadPhase) {
	key := client.ObjectKeyFromObject(workload)
	latest := &schedulerv1alpha1.GPUWorkload{}
	if err := c.Get(ctx, key, latest); err != nil {
		log.Printf("Get workload for phase update: %v", err)
		return
	}
	latest.Status.Phase = phase
	if err := c.Status().Update(ctx, latest); err != nil {
		log.Printf("Update workload phase to %s: %v", phase, err)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
