package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/metrics"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
	"github.com/ishanchopra/gpu-scheduler/internal/worker"
)

// GPUWorkloadReconciler reconciles a GPUWorkload object
type GPUWorkloadReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	RuntimeSimulator  WorkloadRuntimeSimulator
	Recorder          record.EventRecorder
	UseWorkerPool     bool
	SimulatorURL      string
	WorkerJobTemplate worker.WorkerJobTemplate
}

const gpuWorkloadFinalizer = "scheduler.ishanchopra.dev/gpuworkload-finalizer"
const conditionTypeAdmitted = "Admitted"
const annotationCompletionCounted = "scheduler.ishanchopra.dev/completion-counted"
const annotationValueTrue = "true"

func isTerminalPhase(phase schedulerv1alpha1.GPUWorkloadPhase) bool {
	switch phase {
	case schedulerv1alpha1.GPUWorkloadPhaseSucceeded,
		schedulerv1alpha1.GPUWorkloadPhaseFailed,
		schedulerv1alpha1.GPUWorkloadPhasePreempted:
		return true
	default:
		return false
	}
}

//go:generate mockgen -source=gpuworkload_controller.go -destination=../../mocks/mock_workload_runtime_simulator.go -package=mocks

// WorkloadRuntimeSimulator is the simulator contract needed for admission execution.
type WorkloadRuntimeSimulator interface {
	Allocate(workloadID string, gpuCount int, memMiB int) (*sim.Allocation, error)
	AllocateWithOptions(workloadID string, gpuCount int, memMiB int, opts sim.AllocationOptions) (*sim.Allocation, error)
	StartWithRuntimeInput(input sim.RuntimeInput) (runID string, err error)
	GetLatestRunStatusForWorkload(workloadID string) (sim.RunStatus, error)
	Preempt(workloadID string) error
	Release(workloadID string) error
}

// +kubebuilder:rbac:groups=scheduler.ishanchopra.dev,resources=gpuworkloads,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduler.ishanchopra.dev,resources=gpuworkloads/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scheduler.ishanchopra.dev,resources=gpuworkloads/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GPUWorkload object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
//
//nolint:gocyclo // Reconcile branches over phase and simulator state; splitting would obscure flow.
func (r *GPUWorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	workload := &schedulerv1alpha1.GPUWorkload{}
	if err := r.Get(ctx, req.NamespacedName, workload); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to fetch GPUWorkload")
		return ctrl.Result{}, err
	}

	// When phase is terminal (worker-pool or in-process), record completion metric
	// once (e.g. when worker set Succeeded) and release the local simulator allocation.
	if workload.DeletionTimestamp.IsZero() && isTerminalPhase(workload.Status.Phase) {
		needRecordCompletion := workload.Status.Phase == schedulerv1alpha1.GPUWorkloadPhaseSucceeded &&
			(workload.Annotations == nil || workload.Annotations[annotationCompletionCounted] != annotationValueTrue)
		if needRecordCompletion {
			metrics.WorkloadCompletionsCounter.Inc()
			if !workload.CreationTimestamp.IsZero() {
				metrics.E2ELatencySeconds.Observe(time.Since(workload.CreationTimestamp.Time).Seconds())
			}
			if workload.Annotations == nil {
				workload.Annotations = make(map[string]string)
			}
			workload.Annotations[annotationCompletionCounted] = annotationValueTrue
			if err := r.Update(ctx, workload); err != nil {
				log.V(1).Info("Failed to set completion-counted annotation", "error", err)
			}
		}
		workloadID := fmt.Sprintf("%s/%s", workload.Namespace, workload.Name)
		if r.RuntimeSimulator != nil {
			if err := r.RuntimeSimulator.Release(workloadID); err != nil {
				log.V(1).Info("Release workload in local simulator (may already be released)", "workload", workloadID, "error", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is present early so later delete handling can perform cleanup.
	if workload.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(workload, gpuWorkloadFinalizer) {
		controllerutil.AddFinalizer(workload, gpuWorkloadFinalizer)
		if err := r.Update(ctx, workload); err != nil {
			log.Error(err, "Failed to add GPUWorkload finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Initialize queue state exactly once for newly observed workloads.
	if workload.DeletionTimestamp.IsZero() && workload.Status.Phase == "" {
		now := metav1.Now()
		workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseQueued
		workload.Status.QueuedAt = &now
		if err := r.Status().Update(ctx, workload); err != nil {
			log.Error(err, "Failed to initialize GPUWorkload queue status")
			return ctrl.Result{}, err
		}
		if r.Recorder != nil {
			r.Recorder.Event(workload, "Normal", "Queued", "Workload queued for scheduling")
			metrics.EventsEmittedTotal.WithLabelValues("Queued").Inc()
		}
	}

	// Actuation only: admission is owned by the scheduler loop. When Phase=Scheduled,
	// either leave for worker-deployment pods to claim (UseWorkerPool) or run allocation/start in-process.
	if workload.DeletionTimestamp.IsZero() && workload.Status.Phase == schedulerv1alpha1.GPUWorkloadPhaseScheduled {
		if r.UseWorkerPool {
			// Worker-deployment mode: do nothing; long-lived workers will claim and run.
			return ctrl.Result{}, nil
		}
		if err := r.executeAdmission(ctx, workload); err != nil {
			log.Error(err, "Failed to execute admission for scheduled workload")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if workload.DeletionTimestamp.IsZero() && workload.Status.Phase == schedulerv1alpha1.GPUWorkloadPhaseRunning {
		if r.UseWorkerPool {
			// Worker-deployment mode: workers update status to Succeeded/Failed; nothing to do here.
			return ctrl.Result{}, nil
		}
		done, err := r.pollRunningStatus(ctx, workload)
		if err != nil {
			log.Error(err, "Failed to poll running workload status")
			return ctrl.Result{}, err
		}
		if !done {
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	}

	// On delete: release allocation in simulator and remove finalizer so the object can be removed.
	if !workload.DeletionTimestamp.IsZero() {
		if r.RuntimeSimulator != nil {
			workloadID := fmt.Sprintf("%s/%s", workload.Namespace, workload.Name)
			if err := r.RuntimeSimulator.Release(workloadID); err != nil {
				log.Error(err, "Failed to release workload allocation on delete")
				return ctrl.Result{}, err
			}
			if r.Recorder != nil {
				r.Recorder.Event(workload, "Normal", "Released", "Workload released on delete")
				metrics.EventsEmittedTotal.WithLabelValues("Released").Inc()
			}
		}
		controllerutil.RemoveFinalizer(workload, gpuWorkloadFinalizer)
		if err := r.Update(ctx, workload); err != nil {
			log.Error(err, "Failed to remove GPUWorkload finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *GPUWorkloadReconciler) executeAdmission(ctx context.Context, workload *schedulerv1alpha1.GPUWorkload) error {
	if r.RuntimeSimulator == nil {
		return fmt.Errorf("runtime simulator is not configured")
	}

	if workload.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseScheduled {
		workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseScheduled
		if err := r.Status().Update(ctx, workload); err != nil {
			return err
		}
		if r.Recorder != nil {
			r.Recorder.Event(workload, "Normal", "Scheduled", "Workload scheduled, allocation started")
			metrics.EventsEmittedTotal.WithLabelValues("Scheduled").Inc()
		}
	}

	workloadID := fmt.Sprintf("%s/%s", workload.Namespace, workload.Name)
	profile := strings.ToLower(strings.TrimSpace(workload.Spec.Profile))
	opts := sim.AllocationOptions{PlacementPolicy: sim.PlacementPolicyFirstFit, PreferredProfile: profile}
	if _, err := r.RuntimeSimulator.AllocateWithOptions(workloadID, int(workload.Spec.GPUCount), int(workload.Spec.GPUMemoryMiB), opts); err != nil &&
		!isAlreadyAllocatedErr(err) {
		return err
	}

	runtimeInput := buildRuntimeInput(workloadID, workload)
	if _, err := r.RuntimeSimulator.StartWithRuntimeInput(runtimeInput); err != nil && !isAlreadyRunningErr(err) {
		return err
	}

	if workload.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseRunning {
		workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseRunning
		if err := r.Status().Update(ctx, workload); err != nil {
			return err
		}
		if r.Recorder != nil {
			r.Recorder.Event(workload, "Normal", "Running", "Workload running")
			metrics.EventsEmittedTotal.WithLabelValues("Running").Inc()
		}
	}

	return nil
}

//nolint:unused // Used when USE_WORKER_POOL with legacy Job-per-workload path.
func (r *GPUWorkloadReconciler) ensureWorkerJob(ctx context.Context, workload *schedulerv1alpha1.GPUWorkload) error {
	jobName := workload.Name + "-job"
	existing := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Namespace: workload.Namespace, Name: jobName}, existing)
	if err == nil {
		if workload.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseRunning {
			workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseRunning
			if err := r.Status().Update(ctx, workload); err != nil {
				return err
			}
			if r.Recorder != nil {
				r.Recorder.Event(workload, "Normal", "Running", "Worker Job running")
				metrics.EventsEmittedTotal.WithLabelValues("Running").Inc()
			}
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	tpl := r.WorkerJobTemplate
	if tpl.Image == "" {
		tpl = worker.DefaultWorkerJobTemplate()
	}
	job := worker.BuildWorkerJob(workload, r.SimulatorURL, tpl)
	if err := controllerutil.SetControllerReference(workload, job, r.Scheme); err != nil {
		return fmt.Errorf("set Job owner reference: %w", err)
	}
	if err := r.Create(ctx, job); err != nil {
		return err
	}
	if r.Recorder != nil {
		r.Recorder.Event(workload, "Normal", "WorkerJobCreated", "Created worker Job for workload")
		metrics.EventsEmittedTotal.WithLabelValues("WorkerJobCreated").Inc()
	}
	workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseRunning
	if err := r.Status().Update(ctx, workload); err != nil {
		return err
	}
	if r.Recorder != nil {
		r.Recorder.Event(workload, "Normal", "Running", "Worker Job created, workload running")
		metrics.EventsEmittedTotal.WithLabelValues("Running").Inc()
	}
	return nil
}

// syncWorkloadPhaseFromWorkerJob checks the worker Job status and updates the workload Phase to
// Succeeded or Failed when the Job completes. Returns true when the workload phase was updated
// (or already terminal), false when the Job is still running.
//
//nolint:unused // Used when USE_WORKER_POOL with legacy Job-per-workload path.
func (r *GPUWorkloadReconciler) syncWorkloadPhaseFromWorkerJob(ctx context.Context, workload *schedulerv1alpha1.GPUWorkload) (bool, error) {
	jobName := workload.Name + "-job"
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: workload.Namespace, Name: jobName}, job); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	var targetPhase schedulerv1alpha1.GPUWorkloadPhase
	if job.Status.Succeeded >= 1 {
		targetPhase = schedulerv1alpha1.GPUWorkloadPhaseSucceeded
	} else if job.Status.Failed >= 1 || isJobFailed(job) {
		targetPhase = schedulerv1alpha1.GPUWorkloadPhaseFailed
	} else {
		return false, nil
	}
	if workload.Status.Phase == targetPhase {
		return true, nil
	}
	workload.Status.Phase = targetPhase
	if targetPhase == schedulerv1alpha1.GPUWorkloadPhaseSucceeded {
		if workload.Annotations == nil {
			workload.Annotations = make(map[string]string)
		}
		workload.Annotations[annotationCompletionCounted] = annotationValueTrue
	}
	if err := r.Status().Update(ctx, workload); err != nil {
		return false, err
	}
	if targetPhase == schedulerv1alpha1.GPUWorkloadPhaseSucceeded {
		metrics.WorkloadCompletionsCounter.Inc()
		if !workload.CreationTimestamp.IsZero() {
			metrics.E2ELatencySeconds.Observe(time.Since(workload.CreationTimestamp.Time).Seconds())
		}
		if err := r.Update(ctx, workload); err != nil {
			log := logf.FromContext(ctx)
			log.V(1).Info("Failed to persist completion-counted annotation", "error", err)
		}
	}
	// Release the local simulator allocation so fleet capacity and pool status
	// reflect freed devices; the scheduler can then admit more queued workloads.
	workloadID := fmt.Sprintf("%s/%s", workload.Namespace, workload.Name)
	if r.RuntimeSimulator != nil {
		if err := r.RuntimeSimulator.Release(workloadID); err != nil {
			log := logf.FromContext(ctx)
			log.Error(err, "Failed to release workload allocation in local simulator (capacity may stay stale)", "workload", workloadID)
		}
	}
	if r.Recorder != nil {
		switch targetPhase {
		case schedulerv1alpha1.GPUWorkloadPhaseSucceeded:
			r.Recorder.Event(workload, "Normal", "Succeeded", "Worker Job completed successfully")
			metrics.EventsEmittedTotal.WithLabelValues("Succeeded").Inc()
		case schedulerv1alpha1.GPUWorkloadPhaseFailed:
			r.Recorder.Event(workload, "Warning", "Failed", "Worker Job failed")
			metrics.EventsEmittedTotal.WithLabelValues("Failed").Inc()
		}
	}
	return true, nil
}

//nolint:unused // Used by syncWorkloadPhaseFromWorkerJob (legacy worker Job path).
func isJobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func buildRuntimeInput(workloadID string, workload *schedulerv1alpha1.GPUWorkload) sim.RuntimeInput {
	input := sim.RuntimeInput{
		WorkloadID: workloadID,
		Tokens:     workload.Spec.Tokens,
		Profile:    strings.ToLower(strings.TrimSpace(workload.Spec.Profile)),
		Kind:       sim.WorkloadKind(MapWorkloadKind(workload.Spec.Kind)),
	}

	if workload.Spec.Training != nil {
		input.Training = &sim.TrainingRuntime{
			GlobalBatchSize: int(workload.Spec.Training.GlobalBatchSize),
			MicroBatchSize:  int(workload.Spec.Training.MicroBatchSize),
			GradAccumSteps:  int(workload.Spec.Training.GradAccumSteps),
			SequenceLength:  int(workload.Spec.Training.SequenceLength),
		}
	}
	if workload.Spec.Inference != nil {
		input.Inference = &sim.InferenceRuntime{
			BatchSize: int(workload.Spec.Inference.BatchSize),
		}
	}
	if workload.Spec.Eval != nil {
		input.Eval = &sim.EvalRuntime{
			BatchSize:             int(workload.Spec.Eval.BatchSize),
			MetricOverheadPct:     workload.Spec.Eval.MetricOverheadPct,
			ValidationOverheadPct: workload.Spec.Eval.ValidationOverheadPct,
		}
	}
	if workload.Spec.FineTune != nil {
		input.FineTune = &sim.FineTuneRuntime{
			GlobalBatchSize:           int(workload.Spec.FineTune.GlobalBatchSize),
			MicroBatchSize:            int(workload.Spec.FineTune.MicroBatchSize),
			GradAccumSteps:            int(workload.Spec.FineTune.GradAccumSteps),
			WarmStartOverheadPct:      workload.Spec.FineTune.WarmStartOverheadPct,
			CheckpointLoadOverheadPct: workload.Spec.FineTune.CheckpointLoadOverheadPct,
		}
	}
	if workload.Spec.RLHF != nil {
		input.RLHF = &sim.RLHFRuntime{
			RolloutBatchSize:  int(workload.Spec.RLHF.RolloutBatchSize),
			RewardBatchSize:   int(workload.Spec.RLHF.RewardBatchSize),
			UpdateBatchSize:   int(workload.Spec.RLHF.UpdateBatchSize),
			PolicyUpdateSteps: int(workload.Spec.RLHF.PolicyUpdateSteps),
		}
	}
	if workload.Spec.Embedding != nil {
		input.Embedding = &sim.EmbeddingRuntime{
			BatchSize:       int(workload.Spec.Embedding.BatchSize),
			VectorDimension: int(workload.Spec.Embedding.VectorDimension),
			AvgTokenLength:  int(workload.Spec.Embedding.AvgTokenLength),
		}
	}
	if workload.Spec.DataPreprocess != nil {
		input.DataPreprocess = &sim.DataPreprocessRuntime{
			InputBytes:              workload.Spec.DataPreprocess.InputBytes,
			TokenizationOverheadPct: workload.Spec.DataPreprocess.TokenizationOverheadPct,
			AugmentationOverheadPct: workload.Spec.DataPreprocess.AugmentationOverheadPct,
		}
	}
	if workload.Spec.Distillation != nil {
		input.Distillation = &sim.DistillationRuntime{
			TeacherProfile:            strings.ToLower(strings.TrimSpace(workload.Spec.Distillation.TeacherProfile)),
			BatchSize:                 int(workload.Spec.Distillation.BatchSize),
			TeacherForwardOverheadPct: workload.Spec.Distillation.TeacherForwardOverheadPct,
		}
	}
	if workload.Spec.Jitter != nil {
		seed := int64(0)
		if workload.Spec.Jitter.Seed != nil {
			seed = *workload.Spec.Jitter.Seed
		}
		input.Jitter = &sim.JitterConfig{
			Enabled: workload.Spec.Jitter.Enabled,
			Pct:     workload.Spec.Jitter.Pct,
			Seed:    seed,
		}
	}

	return input
}

func isAlreadyAllocatedErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already has an allocation")
}

func isAlreadyRunningErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already has a running run")
}

func (r *GPUWorkloadReconciler) pollRunningStatus(ctx context.Context, workload *schedulerv1alpha1.GPUWorkload) (bool, error) {
	if r.RuntimeSimulator == nil {
		return false, fmt.Errorf("runtime simulator is not configured")
	}
	workloadID := fmt.Sprintf("%s/%s", workload.Namespace, workload.Name)
	status, err := r.RuntimeSimulator.GetLatestRunStatusForWorkload(workloadID)
	if err != nil {
		// Run not found yet; continue polling.
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return false, nil
		}
		return false, err
	}

	var targetPhase schedulerv1alpha1.GPUWorkloadPhase
	switch status {
	case sim.RunStatusRunning:
		return false, nil
	case sim.RunStatusSucceeded:
		targetPhase = schedulerv1alpha1.GPUWorkloadPhaseSucceeded
	case sim.RunStatusFailed:
		targetPhase = schedulerv1alpha1.GPUWorkloadPhaseFailed
	case sim.RunStatusPreempted:
		targetPhase = schedulerv1alpha1.GPUWorkloadPhasePreempted
	default:
		return false, nil
	}
	if workload.Status.Phase != targetPhase {
		workload.Status.Phase = targetPhase
		if targetPhase == schedulerv1alpha1.GPUWorkloadPhaseSucceeded {
			if workload.Annotations == nil {
				workload.Annotations = make(map[string]string)
			}
			workload.Annotations[annotationCompletionCounted] = annotationValueTrue
		}
		if err := r.Status().Update(ctx, workload); err != nil {
			return false, err
		}
		if targetPhase == schedulerv1alpha1.GPUWorkloadPhaseSucceeded {
			metrics.WorkloadCompletionsCounter.Inc()
			if !workload.CreationTimestamp.IsZero() {
				metrics.E2ELatencySeconds.Observe(time.Since(workload.CreationTimestamp.Time).Seconds())
			}
			if err := r.Update(ctx, workload); err != nil {
				log := logf.FromContext(ctx)
				log.V(1).Info("Failed to persist completion-counted annotation", "error", err)
			}
		}
		// Release the allocation in the local simulator so capacity is freed.
		if r.RuntimeSimulator != nil {
			if err := r.RuntimeSimulator.Release(workloadID); err != nil {
				log := logf.FromContext(ctx)
				log.Error(err, "Failed to release workload allocation in local simulator", "workload", workloadID)
			}
		}
		if r.Recorder != nil {
			switch targetPhase {
			case schedulerv1alpha1.GPUWorkloadPhaseSucceeded:
				r.Recorder.Event(workload, "Normal", "Succeeded", "Workload completed successfully")
				metrics.EventsEmittedTotal.WithLabelValues("Succeeded").Inc()
			case schedulerv1alpha1.GPUWorkloadPhaseFailed:
				r.Recorder.Event(workload, "Warning", "Failed", "Workload run failed")
				metrics.EventsEmittedTotal.WithLabelValues("Failed").Inc()
			case schedulerv1alpha1.GPUWorkloadPhasePreempted:
				r.Recorder.Event(workload, "Warning", "Preempted", "Workload was preempted")
				metrics.EventsEmittedTotal.WithLabelValues("Preempted").Inc()
			}
		}
	}
	return true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUWorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&schedulerv1alpha1.GPUWorkload{}).
		Named("gpuworkload")
	b = b.Owns(&batchv1.Job{})
	return b.Complete(r)
}
