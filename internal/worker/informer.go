package worker

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
)

const gpuWorkloadGVRGroup = "scheduler.ishanchopra.dev"
const gpuWorkloadGVRVersion = "v1alpha1"
const gpuWorkloadGVRResource = "gpuworkloads"

var gpuWorkloadGVR = schema.GroupVersionResource{
	Group:    gpuWorkloadGVRGroup,
	Version:  gpuWorkloadGVRVersion,
	Resource: gpuWorkloadGVRResource,
}

// GPUWorkloadInformer holds a shared informer for GPUWorkload in a namespace and a trigger
// channel that is sent when a potentially claimable workload (Phase=Scheduled, unclaimed)
// is added or updated.
type GPUWorkloadInformer struct {
	Informer  cache.SharedInformer
	TriggerCh chan struct{}
	namespace string
}

// NewGPUWorkloadInformer creates a shared informer for GPUWorkload in the given namespace.
// The informer is not started; call Run() and wait for HasSynced before reading from the cache.
// TriggerCh is sent (non-blocking, coalesced) when an Add or Update has Phase=Scheduled and
// no claim annotation, so the claim loop can wake and look for work in the cache.
func NewGPUWorkloadInformer(ctx context.Context, restConfig *rest.Config, namespace string) (*GPUWorkloadInformer, error) {
	dc, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	triggerCh := make(chan struct{}, 1)
	lw := &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			return dc.Resource(gpuWorkloadGVR).Namespace(namespace).List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			return dc.Resource(gpuWorkloadGVR).Namespace(namespace).Watch(ctx, opts)
		},
	}
	// ResyncPeriod 0: rely only on watch, no periodic list.
	informer := cache.NewSharedInformer(lw, &unstructured.Unstructured{}, 0)
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if isScheduledAndUnclaimedUnstructured(obj) {
				select {
				case triggerCh <- struct{}{}:
				default:
				}
			}
		},
		UpdateFunc: func(_, newObj any) {
			if isScheduledAndUnclaimedUnstructured(newObj) {
				select {
				case triggerCh <- struct{}{}:
				default:
				}
			}
		},
	})
	return &GPUWorkloadInformer{
		Informer:  informer,
		TriggerCh: triggerCh,
		namespace: namespace,
	}, nil
}

// isScheduledAndUnclaimedUnstructured returns true if obj is a GPUWorkload in Phase=Scheduled
// with no claim annotation. obj is expected to be *unstructured.Unstructured from the informer.
func isScheduledAndUnclaimedUnstructured(obj any) bool {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return false
	}
	phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
	if phase != string(schedulerv1alpha1.GPUWorkloadPhaseScheduled) {
		return false
	}
	ann, _, _ := unstructured.NestedStringMap(u.Object, "metadata", "annotations")
	_, claimed := ann[AnnotationClaimedBy]
	return !claimed
}
