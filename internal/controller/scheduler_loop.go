package controller

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/metrics"
	"github.com/ishanchopra/gpu-scheduler/internal/scheduler"
)

// schedulerState holds the in-memory scheduling queue and related state, fed by
// watch/cache events. Updated by informer event handlers.
type schedulerState struct {
	mu sync.RWMutex

	// queued[namespace][workloadID] = QueuedWorkload for workloads in Phase=Queued
	queued map[string]map[string]scheduler.QueuedWorkload
	// scheduled[namespace][workloadID] = RunningWorkload for workloads in Phase=Scheduled (admitted, not yet running)
	scheduled map[string]map[string]scheduler.RunningWorkload
	// running[namespace][workloadID] = RunningWorkload for workloads in Phase=Running
	running map[string]map[string]scheduler.RunningWorkload
	// fleet[namespace] = FleetFreeCapacity from GPUNodePool(s) in that namespace
	fleet map[string]scheduler.FleetFreeCapacity
	// quotas[namespace][tenant] = TenantQuota
	quotas map[string]map[string]scheduler.TenantQuota
}

func newSchedulerState() *schedulerState {
	return &schedulerState{
		queued:    make(map[string]map[string]scheduler.QueuedWorkload),
		scheduled: make(map[string]map[string]scheduler.RunningWorkload),
		running:   make(map[string]map[string]scheduler.RunningWorkload),
		fleet:     make(map[string]scheduler.FleetFreeCapacity),
		quotas:    make(map[string]map[string]scheduler.TenantQuota),
	}
}

func (st *schedulerState) workloadID(ns, name string) string {
	return ns + "/" + name
}

func (st *schedulerState) upsertQueued(ns string, q scheduler.QueuedWorkload) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.queued[ns] == nil {
		st.queued[ns] = make(map[string]scheduler.QueuedWorkload)
	}
	st.queued[ns][q.WorkloadID] = q
}

func (st *schedulerState) removeWorkload(ns, workloadID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if m := st.queued[ns]; m != nil {
		delete(m, workloadID)
		if len(m) == 0 {
			delete(st.queued, ns)
		}
	}
	if m := st.scheduled[ns]; m != nil {
		delete(m, workloadID)
		if len(m) == 0 {
			delete(st.scheduled, ns)
		}
	}
	if m := st.running[ns]; m != nil {
		delete(m, workloadID)
		if len(m) == 0 {
			delete(st.running, ns)
		}
	}
}

func (st *schedulerState) upsertScheduled(ns string, r scheduler.RunningWorkload) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.scheduled[ns] == nil {
		st.scheduled[ns] = make(map[string]scheduler.RunningWorkload)
	}
	st.scheduled[ns][r.WorkloadID] = r
	if m := st.queued[ns]; m != nil {
		delete(m, r.WorkloadID)
		if len(m) == 0 {
			delete(st.queued, ns)
		}
	}
}

func (st *schedulerState) upsertRunning(ns string, r scheduler.RunningWorkload) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.running[ns] == nil {
		st.running[ns] = make(map[string]scheduler.RunningWorkload)
	}
	st.running[ns][r.WorkloadID] = r
	if m := st.queued[ns]; m != nil {
		delete(m, r.WorkloadID)
		if len(m) == 0 {
			delete(st.queued, ns)
		}
	}
	if m := st.scheduled[ns]; m != nil {
		delete(m, r.WorkloadID)
		if len(m) == 0 {
			delete(st.scheduled, ns)
		}
	}
}

func (st *schedulerState) setFleet(ns string, f scheduler.FleetFreeCapacity) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.fleet[ns] = f
}

func (st *schedulerState) deleteFleet(ns string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.fleet, ns)
}

func (st *schedulerState) upsertQuota(ns, tenant string, q scheduler.TenantQuota) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.quotas[ns] == nil {
		st.quotas[ns] = make(map[string]scheduler.TenantQuota)
	}
	st.quotas[ns][tenant] = q
}

func (st *schedulerState) deleteQuota(ns, tenant string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if m := st.quotas[ns]; m != nil {
		delete(m, tenant)
		if len(m) == 0 {
			delete(st.quotas, ns)
		}
	}
}

func (st *schedulerState) deleteAllQuotasInNamespace(ns string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.quotas, ns)
}

// namespacesWithQueueAndFleet returns namespace names that have at least one
// queued workload and fleet capacity (required to run a scheduling cycle).
func (st *schedulerState) namespacesWithQueueAndFleet() []string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var out []string
	for ns := range st.queued {
		if len(st.queued[ns]) == 0 {
			continue
		}
		if _, hasFleet := st.fleet[ns]; hasFleet {
			out = append(out, ns)
		}
	}
	return out
}

// snapshotForNamespace returns a copy of queued workloads, scheduled workloads,
// running workloads, fleet capacity, and quotas for the given namespace. ok is
// false if there is no fleet for the namespace (scheduling cannot run).
func (st *schedulerState) snapshotForNamespace(ns string) (
	queued []scheduler.QueuedWorkload,
	scheduled []scheduler.RunningWorkload,
	running []scheduler.RunningWorkload,
	fleet scheduler.FleetFreeCapacity,
	quotas map[string]scheduler.TenantQuota,
	ok bool,
) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	fleet, ok = st.fleet[ns]
	if !ok {
		return nil, nil, nil, scheduler.FleetFreeCapacity{}, nil, false
	}
	// Deep-copy fleet.ByProfile so caller cannot mutate state.
	byProfile := make(map[string]scheduler.ProfileFreeCapacity, len(fleet.ByProfile))
	maps.Copy(byProfile, fleet.ByProfile)
	fleet.ByProfile = byProfile
	for _, q := range st.queued[ns] {
		queued = append(queued, q)
	}
	for _, r := range st.scheduled[ns] {
		scheduled = append(scheduled, r)
	}
	for _, r := range st.running[ns] {
		running = append(running, r)
	}
	quotas = make(map[string]scheduler.TenantQuota)
	maps.Copy(quotas, st.quotas[ns])
	return queued, scheduled, running, fleet, quotas, true
}

// SchedulerLoop is a leader-elected Runnable that owns admission decisions for
// GPUWorkloads (scale-friendly architecture). When run with leader election,
// only the elected leader runs this loop. It maintains an in-memory scheduling
// queue fed by watch/cache events. Scheduling is triggered on events
// (new Queued workload, workload completion/preemption, pool/quota changes)
// instead of timer-based requeues.
type SchedulerLoop struct {
	Client    client.Client
	Scheme    *runtime.Scheme
	Cache     cache.Cache
	Simulator WorkloadRuntimeSimulator
	Recorder  record.EventRecorder
	// ClaimableProducer, when set, is used to publish identifiers of workloads
	// that have just transitioned to Phase=Scheduled so workers can discover
	// claimable work via a queue instead of an informer cache.
	ClaimableProducer ClaimableProducer

	// MaxAdmissionsPerCycle caps how many workloads are admitted in one
	// scheduling cycle (batching). 0 or 1 means one per cycle; >1 allows
	// multiple admissions when capacity allows.
	MaxAdmissionsPerCycle int
	// MinCycleInterval is the minimum time between the start of two
	// consecutive scheduling cycles (rate limiting). 0 disables.
	MinCycleInterval time.Duration

	state     *schedulerState
	triggerCh chan struct{}
}

// NeedLeaderElection returns true so the scheduler loop runs only when this
// manager instance is the leader.
func (s *SchedulerLoop) NeedLeaderElection() bool {
	return true
}

// requestScheduling requests one scheduling cycle. Non-blocking; multiple
// calls coalesce into one pending cycle. That single cycle runs once and
// processes the full in-memory state (all queued workloads, capacity, etc.),
// so one cycle handles all events that triggered it.
func (s *SchedulerLoop) requestScheduling() {
	if s.triggerCh == nil {
		return
	}
	select {
	case s.triggerCh <- struct{}{}:
	default:
		// Cycle already requested
	}
}

// runSchedulingCycle runs one scheduling pass over in-memory state. It admits
// up to MaxAdmissionsPerCycle workloads (batching) per namespace when capacity
// allows, patches each to Admitted=True and Phase=Scheduled, and handles
// preemption when needed.
func (s *SchedulerLoop) runSchedulingCycle(ctx context.Context) {
	log := logf.FromContext(ctx)
	log.V(1).Info("Scheduling cycle triggered")
	metrics.SchedulingCyclesCounter.Inc()

	maxAdmissions := s.MaxAdmissionsPerCycle
	if maxAdmissions <= 0 {
		maxAdmissions = 1
	}

	for _, ns := range s.state.namespacesWithQueueAndFleet() {
		queued, scheduled, running, fleet, quotas, ok := s.state.snapshotForNamespace(ns)
		if !ok || len(queued) == 0 {
			continue
		}
		inputs := scheduler.SchedulingInputs{
			QueuedWorkloads:    queued,
			ScheduledWorkloads: scheduled,
			RunningWorkloads:   running,
			FleetFreeCapacity:  fleet,
			TenantQuotas:       quotas,
		}
		if err := inputs.Validate(); err != nil {
			log.V(1).Info("Skipping namespace: invalid scheduling inputs", "namespace", ns, "error", err)
			continue
		}

		admittedCount := 0
		working := inputs
		now := time.Now()
		var lastNoAdmissionReason string

		for admittedCount < maxAdmissions {
			selectedID, selectedVictims, reason, message, noAdmissionReason := scheduler.SelectOneForAdmission(working)
			if selectedID == "" {
				lastNoAdmissionReason = noAdmissionReason
				if noAdmissionReason == scheduler.NoAdmissionReasonQuota {
					metrics.QuotaDeniedCounter.Inc()
				}
				metrics.ScheduleDecisionsCounter.WithLabelValues("no_candidate").Inc()
				break
			}

			if len(selectedVictims) > 0 {
				if err := s.markVictimsPreemptedAndRelease(ctx, selectedVictims); err != nil {
					log.Error(err, "Failed to preempt victims, skipping admission", "workload", selectedID)
					metrics.ScheduleDecisionsCounter.WithLabelValues("error_preempt").Inc()
					break
				}
			}

			if err := s.patchWorkloadAdmittedAndScheduled(ctx, selectedID, reason, message); err != nil {
				log.Error(err, "Failed to patch workload admitted and scheduled", "workload", selectedID)
				metrics.ScheduleDecisionsCounter.WithLabelValues("error_patch").Inc()
				break
			}
			metrics.ScheduleDecisionsCounter.WithLabelValues("admitted").Inc()
			admitted := scheduler.QueuedWorkloadByID(working, selectedID)
			if admitted != nil {
				metrics.AdmissionsByTenantCounter.WithLabelValues(tenantForMetric(admitted.Tenant)).Inc()
				metrics.ScheduleDecisionsByKindCounter.WithLabelValues(string(admitted.Kind)).Inc()
				metrics.QueueWaitTimeHistogram.Observe(time.Since(admitted.QueuedAt).Seconds())
			}
			log.Info("Admitted workload", "workload", selectedID, "reason", reason)
			admittedCount++

			if admitted == nil {
				break
			}
			var err error
			working, err = scheduler.ApplyVirtualAdmission(working, *admitted, now)
			if err != nil {
				log.V(1).Info("Could not apply virtual admission for batching", "workload", selectedID, "error", err)
				break
			}
		}
		if admittedCount > 0 {
			metrics.AdmissionsPerCycleHistogram.Observe(float64(admittedCount))
			return
		}
		if lastNoAdmissionReason == scheduler.NoAdmissionReasonNoFit && len(working.QueuedWorkloads) > 0 && s.Recorder != nil {
			ordered := scheduler.OrderQueued(working.QueuedWorkloads)
			fair := scheduler.WeightedRoundRobinByTenant(ordered, working.TenantQuotas)
			if len(fair) > 0 {
				firstID := fair[0].WorkloadID
				if namespace, name, ok := strings.Cut(firstID, "/"); ok && namespace != "" && name != "" {
					workload := &schedulerv1alpha1.GPUWorkload{}
					if err := s.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, workload); err == nil {
						s.Recorder.Event(workload, "Warning", "NoFit", "No candidate fits fleet capacity; workloads remain Queued")
						metrics.EventsEmittedTotal.WithLabelValues("NoFit").Inc()
					}
				}
			}
		}
	}
	metrics.AdmissionsPerCycleHistogram.Observe(0)
}

// tenantForMetric returns the tenant label for metrics; use "_empty" for empty tenant.
func tenantForMetric(tenant string) string {
	if tenant == "" {
		return "_empty"
	}
	return tenant
}

// maxConflictRetries is the number of retries for status updates on conflict
// (optimistic concurrency; re-read and retry on resourceVersion conflict).
const maxConflictRetries = 5

// runWithConflictRetry runs op up to maxConflictRetries times. If op returns a
// Conflict error, it retries (op should re-Get and apply to get latest resourceVersion).
// Success or any non-Conflict error is returned immediately.
func runWithConflictRetry(op func() error) error {
	var lastErr error
	for range maxConflictRetries {
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		if apierrors.IsConflict(lastErr) {
			metrics.StatusConflict409Counter.Inc()
		}
		if !apierrors.IsConflict(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

// markVictimsPreemptedAndRelease sets each victim's Phase to Preempted and
// calls the simulator Preempt and Release for each so capacity is freed.
// Status updates are retried on conflict.
func (s *SchedulerLoop) markVictimsPreemptedAndRelease(ctx context.Context, victims []scheduler.RunningWorkload) error {
	for _, v := range victims {
		namespace, name, ok := strings.Cut(v.WorkloadID, "/")
		if !ok || namespace == "" || name == "" {
			continue
		}
		key := client.ObjectKey{Namespace: namespace, Name: name}
		err := runWithConflictRetry(func() error {
			victim := &schedulerv1alpha1.GPUWorkload{}
			if err := s.Client.Get(ctx, key, victim); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("get victim %s: %w", v.WorkloadID, err)
			}
			if victim.Status.Phase == schedulerv1alpha1.GPUWorkloadPhasePreempted {
				return nil
			}
			victim.Status.Phase = schedulerv1alpha1.GPUWorkloadPhasePreempted
			if err := s.Client.Status().Update(ctx, victim); err != nil {
				return err
			}
			if s.Recorder != nil {
				s.Recorder.Event(victim, "Warning", "Preempted", "Workload preempted to make capacity")
				metrics.EventsEmittedTotal.WithLabelValues("Preempted").Inc()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("mark victim preempted %s: %w", v.WorkloadID, err)
		}
		if err := s.Simulator.Preempt(v.WorkloadID); err != nil {
			return fmt.Errorf("simulator preempt %s: %w", v.WorkloadID, err)
		}
		if err := s.Simulator.Release(v.WorkloadID); err != nil {
			return fmt.Errorf("simulator release %s: %w", v.WorkloadID, err)
		}
		metrics.PreemptionsCounter.Inc()
	}
	return nil
}

// patchWorkloadAdmittedAndScheduled sets the workload's Admitted condition to
// True and Phase to Scheduled. The reconciler will then execute
// allocation and start (actuation). Status update is retried on conflict.
func (s *SchedulerLoop) patchWorkloadAdmittedAndScheduled(ctx context.Context, workloadID, reason, message string) error {
	namespace, name, ok := strings.Cut(workloadID, "/")
	if !ok || namespace == "" || name == "" {
		return fmt.Errorf("invalid workloadID %q", workloadID)
	}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	err := runWithConflictRetry(func() error {
		workload := &schedulerv1alpha1.GPUWorkload{}
		if err := s.Client.Get(ctx, key, workload); err != nil {
			return fmt.Errorf("get workload: %w", err)
		}
		if workload.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseQueued {
			return nil // Already admitted or terminal
		}
		meta.SetStatusCondition(&workload.Status.Conditions, metav1.Condition{
			Type:               conditionTypeAdmitted,
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: workload.GetGeneration(),
			LastTransitionTime: metav1.Now(),
		})
		workload.Status.Phase = schedulerv1alpha1.GPUWorkloadPhaseScheduled
		if err := s.Client.Status().Update(ctx, workload); err != nil {
			return err
		}
		if s.Recorder != nil {
			s.Recorder.Event(workload, "Normal", "Admitted", message)
			metrics.EventsEmittedTotal.WithLabelValues("Admitted").Inc()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("patch workload %s: %w", workloadID, err)
	}
	if s.ClaimableProducer != nil {
		if err := s.ClaimableProducer.Produce(ctx, namespace, name); err != nil {
			// Best-effort: log and continue. The workload remains Scheduled and
			// can still be discovered via other mechanisms if enabled.
			logf.FromContext(ctx).Error(err, "Failed to publish claimable workload", "namespace", namespace, "name", name)
		}
	}
	return nil
}

// Start runs the scheduler loop until ctx is cancelled. It implements
// manager.Runnable. It registers informer event handlers to feed the
// in-memory queue and to trigger scheduling on events, then
// runs a worker that processes scheduling cycles.
func (s *SchedulerLoop) Start(ctx context.Context) error {
	s.state = newSchedulerState()
	// Buffer size 1: at most one pending cycle (coalescing). Sends from event
	// handlers are non-blocking when full, so the informer is never stalled.
	s.triggerCh = make(chan struct{}, 1)

	// Get informers and add event handlers. The cache is already started by the
	// manager before runnables run.
	if err := s.registerWorkloadInformer(ctx); err != nil {
		return err
	}
	if err := s.registerPoolInformer(ctx); err != nil {
		return err
	}
	if err := s.registerQuotaInformer(ctx); err != nil {
		return err
	}

	// Seed fleet from current API state. Handlers only see events after registration;
	// if the cache already synced, we would otherwise never get Add for existing pools.
	if err := s.refreshFleetFromAPI(ctx); err != nil {
		logf.FromContext(ctx).Error(err, "Initial fleet refresh failed")
		// Continue; pool informer will populate fleet on next pool event.
	} else {
		s.requestScheduling()
	}

	// Run scheduling worker: process triggers until context is cancelled.
	// MinCycleInterval (if set) rate-limits how often cycles run.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.triggerCh:
				cycleStart := time.Now()
				s.runSchedulingCycle(ctx)
				if s.MinCycleInterval > 0 {
					if elapsed := time.Since(cycleStart); elapsed < s.MinCycleInterval {
						sleep := s.MinCycleInterval - elapsed
						select {
						case <-ctx.Done():
							return
						case <-time.After(sleep):
						}
					}
				}
			}
		}
	}()

	<-ctx.Done()
	return ctx.Err()
}

func (s *SchedulerLoop) registerWorkloadInformer(ctx context.Context) error {
	informer, err := s.Cache.GetInformer(ctx, &schedulerv1alpha1.GPUWorkload{})
	if err != nil {
		return fmt.Errorf("get GPUWorkload informer: %w", err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.handleWorkloadAdd(obj)
		},
		UpdateFunc: func(oldObj, newObj any) {
			s.handleWorkloadUpdate(oldObj, newObj)
		},
		DeleteFunc: func(obj any) {
			s.handleWorkloadDelete(obj)
		},
	})
	return err
}

func (s *SchedulerLoop) handleWorkloadAdd(obj any) {
	wasQueued := s.applyWorkloadToState(obj, false)
	if wasQueued {
		s.requestScheduling()
	}
}

func (s *SchedulerLoop) handleWorkloadUpdate(oldObj, newObj any) {
	var oldPhase schedulerv1alpha1.GPUWorkloadPhase
	if w, ok := oldObj.(*schedulerv1alpha1.GPUWorkload); ok {
		oldPhase = w.Status.Phase
	}
	wasQueued := s.applyWorkloadToState(newObj, false)
	newPhase := schedulerv1alpha1.GPUWorkloadPhase("")
	if w, ok := newObj.(*schedulerv1alpha1.GPUWorkload); ok {
		newPhase = w.Status.Phase
	}
	// Trigger when a new workload is queued or when a running workload completes or is preempted.
	if wasQueued || (oldPhase == schedulerv1alpha1.GPUWorkloadPhaseRunning && newPhase != schedulerv1alpha1.GPUWorkloadPhaseRunning) {
		s.requestScheduling()
	}
}

func (s *SchedulerLoop) handleWorkloadDelete(obj any) {
	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	w, ok := obj.(*schedulerv1alpha1.GPUWorkload)
	if ok && w.Status.Phase == schedulerv1alpha1.GPUWorkloadPhaseRunning {
		s.requestScheduling()
	}
	s.applyWorkloadToState(obj, true)
}

// applyWorkloadToState updates in-memory state from a workload add/update/delete.
// For add/update (deleted=false) it returns true if the workload is Queued.
func (s *SchedulerLoop) applyWorkloadToState(obj any, deleted bool) bool {
	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false
	}
	ns := accessor.GetNamespace()
	workloadID := s.state.workloadID(ns, accessor.GetName())

	if deleted {
		s.state.removeWorkload(ns, workloadID)
		return false
	}

	w, ok := obj.(*schedulerv1alpha1.GPUWorkload)
	if !ok {
		return false
	}
	if w.DeletionTimestamp != nil {
		s.state.removeWorkload(ns, workloadID)
		return false
	}

	switch w.Status.Phase {
	case schedulerv1alpha1.GPUWorkloadPhaseQueued:
		queuedAt := time.Now()
		if w.Status.QueuedAt != nil {
			queuedAt = w.Status.QueuedAt.Time
		}
		s.state.upsertQueued(ns, GPUWorkloadToQueuedWorkload(w, workloadID, queuedAt))
		return true
	case schedulerv1alpha1.GPUWorkloadPhaseScheduled:
		startedAt := time.Now()
		if w.Status.QueuedAt != nil {
			startedAt = w.Status.QueuedAt.Time
		}
		s.state.upsertScheduled(ns, GPUWorkloadToRunningWorkload(w, workloadID, startedAt))
		return false
	case schedulerv1alpha1.GPUWorkloadPhaseRunning:
		startedAt := time.Now()
		if w.Status.QueuedAt != nil {
			startedAt = w.Status.QueuedAt.Time
		}
		s.state.removeWorkload(ns, workloadID)
		s.state.upsertRunning(ns, GPUWorkloadToRunningWorkload(w, workloadID, startedAt))
		return false
	default:
		s.state.removeWorkload(ns, workloadID)
		return false
	}
}

func (s *SchedulerLoop) registerPoolInformer(ctx context.Context) error {
	informer, err := s.Cache.GetInformer(ctx, &schedulerv1alpha1.GPUNodePool{})
	if err != nil {
		return fmt.Errorf("get GPUNodePool informer: %w", err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.handlePoolEvent(obj, false)
		},
		UpdateFunc: func(_, newObj any) {
			s.handlePoolEvent(newObj, false)
		},
		DeleteFunc: func(obj any) {
			s.handlePoolEvent(obj, true)
		},
	})
	return err
}

// refreshFleetFromAPI lists all GPUNodePools and populates fleet state per namespace.
// Used at startup so we have capacity even if the pool informer already synced before we registered.
func (s *SchedulerLoop) refreshFleetFromAPI(ctx context.Context) error {
	var list schedulerv1alpha1.GPUNodePoolList
	if err := s.Client.List(ctx, &list); err != nil {
		return fmt.Errorf("list GPUNodePools: %w", err)
	}
	byNs := make(map[string][]schedulerv1alpha1.GPUNodePool)
	for i := range list.Items {
		ns := list.Items[i].Namespace
		byNs[ns] = append(byNs[ns], list.Items[i])
	}
	for ns, pools := range byNs {
		s.state.setFleet(ns, aggregateFleetFromPools(pools))
	}
	return nil
}

//nolint:unparam // deleted is passed by callers for consistency with other handlers; pool events always refresh fleet.
func (s *SchedulerLoop) handlePoolEvent(obj any, deleted bool) {
	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return
	}
	ns := accessor.GetNamespace()

	// Aggregate capacity from all GPUNodePool CRs in the namespace (29.1).
	var list schedulerv1alpha1.GPUNodePoolList
	if err := s.Client.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		logf.Log.WithName("scheduler-loop").Error(err, "Failed to list GPUNodePools for fleet aggregation", "namespace", ns)
		return
	}
	if len(list.Items) == 0 {
		s.state.deleteFleet(ns)
	} else {
		s.state.setFleet(ns, aggregateFleetFromPools(list.Items))
	}
	s.requestScheduling()
}

// aggregateFleetFromPools builds FleetFreeCapacity by aggregating all GPUNodePool
// resources in the namespace. Pools with the same profile are merged: free devices
// and free memory are summed. The scheduler has one MemoryMiBPerDevice per profile
// (used for per-device fit and capacity accounting), so we store the first value
// seen for that profile; in practice all pools with the same profile should use the
// same device type and thus the same MemoryMiBPerDevice. Pools with empty profile
// are skipped so FleetFreeCapacity satisfies validation.
func aggregateFleetFromPools(pools []schedulerv1alpha1.GPUNodePool) scheduler.FleetFreeCapacity {
	out := scheduler.FleetFreeCapacity{
		ByProfile: make(map[string]scheduler.ProfileFreeCapacity),
	}
	for i := range pools {
		p := &pools[i]
		profileKey := strings.ToLower(strings.TrimSpace(p.Spec.Profile))
		if profileKey == "" {
			continue
		}
		avail := int(p.Status.AvailableDevices)
		memPerDevice := int64(p.Spec.MemoryMiBPerDevice)
		out.TotalFreeDevices += avail
		out.TotalFreeMemoryMiB += int64(avail) * memPerDevice
		existing := out.ByProfile[profileKey]
		existing.FreeDevices += avail
		existing.FreeMemoryMiB += int64(avail) * memPerDevice
		if existing.MemoryMiBPerDevice == 0 {
			existing.MemoryMiBPerDevice = int(p.Spec.MemoryMiBPerDevice)
		}
		out.ByProfile[profileKey] = existing
	}
	return out
}

func (s *SchedulerLoop) registerQuotaInformer(ctx context.Context) error {
	informer, err := s.Cache.GetInformer(ctx, &schedulerv1alpha1.TenantQuota{})
	if err != nil {
		return fmt.Errorf("get TenantQuota informer: %w", err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			s.handleQuotaEvent(obj, false)
		},
		UpdateFunc: func(_, newObj any) {
			s.handleQuotaEvent(newObj, false)
		},
		DeleteFunc: func(obj any) {
			s.handleQuotaEvent(obj, true)
		},
	})
	return err
}

func (s *SchedulerLoop) handleQuotaEvent(obj any, deleted bool) {
	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return
	}
	ns := accessor.GetNamespace()

	q, ok := obj.(*schedulerv1alpha1.TenantQuota)
	if !ok {
		return
	}
	tenant := strings.TrimSpace(q.Spec.Tenant)
	if deleted {
		if tenant != "" {
			s.state.deleteQuota(ns, tenant)
		} else {
			s.state.deleteAllQuotasInNamespace(ns)
		}
		s.requestScheduling()
		return
	}
	if tenant == "" {
		return
	}
	s.state.upsertQuota(ns, tenant, scheduler.TenantQuota{
		MaxGPUs:      int(q.Spec.MaxGPUs),
		MaxMemoryMiB: q.Spec.MaxMemoryMiB,
		Weight:       int(q.Spec.Weight),
	})
	s.requestScheduling()
}

var _ manager.Runnable = (*SchedulerLoop)(nil)
var _ manager.LeaderElectionRunnable = (*SchedulerLoop)(nil)
