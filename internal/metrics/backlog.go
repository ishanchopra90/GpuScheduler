package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
)

const (
	scheduledWorkloadsMetricName = "gpu_scheduler_scheduled_workloads"
	scheduledWorkloadsHelp       = "Number of GPUWorkload CRs in Phase=Scheduled (backlog for worker pool scaling)"
)

var (
	// ScheduledWorkloadsGauge is the Prometheus gauge for the count of workloads in Phase=Scheduled.
	// Used by HPA/KEDA to scale the worker Deployment.
	ScheduledWorkloadsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: scheduledWorkloadsMetricName,
		Help: scheduledWorkloadsHelp,
	})
)

func init() {
	crmetrics.Registry.MustRegister(ScheduledWorkloadsGauge)
}

// BacklogMetrics is a runnable that periodically lists GPUWorkloads and GPUNodePools
// and updates queued/scheduled counts, utilization ratio, and per-tenant queued counts.
type BacklogMetrics struct {
	Client       client.Client
	PollInterval time.Duration
}

// NeedLeaderElection returns true so backlog metrics are updated only on the leader.
func (b *BacklogMetrics) NeedLeaderElection() bool {
	return true
}

// Start runs the backlog metrics loop until ctx is done.
func (b *BacklogMetrics) Start(ctx context.Context) error {
	if b.PollInterval <= 0 {
		b.PollInterval = 15 * time.Second
	}
	ticker := time.NewTicker(b.PollInterval)
	defer ticker.Stop()
	// lastTenants tracks which tenants had queued workloads last poll so we can set
	// their gauge to 0 when they no longer do. No mutex: only this loop accesses it.
	// Add a mutex if this state is ever shared (e.g. read by another goroutine or tests).
	lastTenants := make(map[string]struct{})
	for {
		queued, scheduled, queuedByTenant, err := b.countWorkloadsByPhase(ctx)
		if err != nil {
			QueuedWorkloadsGauge.Set(0)
			ScheduledWorkloadsGauge.Set(0)
			UtilizationRatioGauge.Set(0)
		} else {
			QueuedWorkloadsGauge.Set(float64(queued))
			ScheduledWorkloadsGauge.Set(float64(scheduled))
			for t := range lastTenants {
				if _, in := queuedByTenant[t]; !in {
					QueuedByTenantGauge.WithLabelValues(t).Set(0)
					delete(lastTenants, t)
				}
			}
			for t, c := range queuedByTenant {
				QueuedByTenantGauge.WithLabelValues(t).Set(float64(c))
				lastTenants[t] = struct{}{}
			}
		}
		total, allocated := b.aggregatePoolDevices(ctx)
		if total > 0 {
			UtilizationRatioGauge.Set(float64(allocated) / float64(total))
		} else {
			UtilizationRatioGauge.Set(0)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (b *BacklogMetrics) countWorkloadsByPhase(ctx context.Context) (queued, scheduled int, queuedByTenant map[string]int, err error) {
	var list schedulerv1alpha1.GPUWorkloadList
	if err = b.Client.List(ctx, &list); err != nil {
		return 0, 0, nil, err
	}
	queuedByTenant = make(map[string]int)
	for i := range list.Items {
		w := &list.Items[i]
		switch w.Status.Phase {
		case schedulerv1alpha1.GPUWorkloadPhaseQueued:
			queued++
			t := w.Spec.Tenant
			if t == "" {
				t = "_empty"
			}
			queuedByTenant[t]++
		case schedulerv1alpha1.GPUWorkloadPhaseScheduled:
			scheduled++
		}
	}
	return queued, scheduled, queuedByTenant, nil
}

func (b *BacklogMetrics) aggregatePoolDevices(ctx context.Context) (total, allocated int) {
	var list schedulerv1alpha1.GPUNodePoolList
	if err := b.Client.List(ctx, &list); err != nil {
		return 0, 0
	}
	for i := range list.Items {
		p := &list.Items[i]
		total += int(p.Status.TotalDevices)
		allocated += int(p.Status.AllocatedDevices)
	}
	return total, allocated
}
