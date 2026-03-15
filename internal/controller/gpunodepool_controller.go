package controller

import (
	"context"
	"fmt"
	"math"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
)

// GPUNodePoolReconciler reconciles a GPUNodePool object
type GPUNodePoolReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	FleetRegistrar FleetRegistrar
}

//go:generate mockgen -source=gpunodepool_controller.go -destination=../../mocks/mock_fleet_registrar.go -package=mocks

// FleetRegistrar is the minimal simulator contract needed by GPUNodePool reconciliation.
type FleetRegistrar interface {
	RegisterFleet(poolKey string, spec sim.GPUNodePoolSpec) error
	DeleteFleet(poolKey string)
	FleetUsageForPool(poolKey string) (sim.FleetUsage, bool)
}

// +kubebuilder:rbac:groups=scheduler.ishanchopra.dev,resources=gpunodepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scheduler.ishanchopra.dev,resources=gpunodepools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scheduler.ishanchopra.dev,resources=gpunodepools/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GPUNodePool object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *GPUNodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pool := &schedulerv1alpha1.GPUNodePool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		if apierrors.IsNotFound(err) {
			r.FleetRegistrar.DeleteFleet(req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to fetch GPUNodePool")
		return ctrl.Result{}, err
	}

	if r.FleetRegistrar == nil {
		err := fmt.Errorf("fleet registrar is not configured")
		log.Error(err, "Could not register GPUNodePool fleet")
		return ctrl.Result{}, err
	}

	poolKey := req.String()
	spec := sim.GPUNodePoolSpec{
		NodeCount:          int(pool.Spec.NodeCount),
		DevicesPerNode:     int(pool.Spec.DevicesPerNode),
		MemoryMiBPerDevice: int(pool.Spec.MemoryMiBPerDevice),
		Profile:            pool.Spec.Profile,
	}
	if pool.Spec.DeviceType != nil {
		spec.DeviceType = *pool.Spec.DeviceType
	}

	if err := r.FleetRegistrar.RegisterFleet(poolKey, spec); err != nil {
		log.Error(err, "Failed to register fleet in simulator", "name", pool.Name, "namespace", pool.Namespace)
		return ctrl.Result{}, err
	}

	usage, ok := r.FleetRegistrar.FleetUsageForPool(poolKey)
	if !ok {
		usage = sim.FleetUsage{}
	}
	desiredTotal := int32(usage.TotalDevices)
	desiredAllocated := int32(usage.AllocatedDevices)
	desiredAvailable := int32(usage.AvailableDevices)
	desiredUtilizationPct := int32(math.Round(usage.Utilization * 100))

	if pool.Status.TotalDevices != desiredTotal ||
		pool.Status.AllocatedDevices != desiredAllocated ||
		pool.Status.AvailableDevices != desiredAvailable ||
		pool.Status.UtilizationPct != desiredUtilizationPct {
		pool.Status.TotalDevices = desiredTotal
		pool.Status.AllocatedDevices = desiredAllocated
		pool.Status.AvailableDevices = desiredAvailable
		pool.Status.UtilizationPct = desiredUtilizationPct

		if err := r.Status().Update(ctx, pool); err != nil {
			log.Error(err, "Failed to update GPUNodePool status", "name", pool.Name, "namespace", pool.Namespace)
			return ctrl.Result{}, err
		}
	}

	log.Info("Registered GPUNodePool fleet in simulator", "name", pool.Name, "namespace", pool.Namespace)

	// Requeue periodically so the remote simulator (used by workers) is re-synced
	// after restarts; it holds fleet state in memory only.
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUNodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&schedulerv1alpha1.GPUNodePool{}).
		Named("gpunodepool").
		Complete(r)
}
