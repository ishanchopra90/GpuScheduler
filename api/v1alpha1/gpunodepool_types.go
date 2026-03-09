package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GPUNodePoolSpec defines the desired state of GPUNodePool
type GPUNodePoolSpec struct {
	// NodeCount is the number of logical nodes in the pool.
	// +kubebuilder:validation:Minimum=1
	NodeCount int32 `json:"nodeCount"`

	// DevicesPerNode is the number of accelerators per node.
	// +kubebuilder:validation:Minimum=1
	DevicesPerNode int32 `json:"devicesPerNode"`

	// MemoryMiBPerDevice is the capacity per device in MiB.
	// +kubebuilder:validation:Minimum=1
	MemoryMiBPerDevice int32 `json:"memoryMiBPerDevice"`

	// Profile is the hardware profile key used for this homogeneous pool.
	// +kubebuilder:validation:MinLength=1
	Profile string `json:"profile"`

	// DeviceType is an optional human-readable accelerator class.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:default:="gpu"
	DeviceType *string `json:"deviceType,omitempty"`
}

// GPUNodePoolStatus defines the observed state of GPUNodePool.
type GPUNodePoolStatus struct {
	// TotalDevices is the total number of devices currently registered in the simulator fleet.
	// +optional
	TotalDevices int32 `json:"totalDevices,omitempty"`

	// AllocatedDevices is the number of currently allocated devices.
	// +optional
	AllocatedDevices int32 `json:"allocatedDevices,omitempty"`

	// AvailableDevices is the number of currently free devices.
	// +optional
	AvailableDevices int32 `json:"availableDevices,omitempty"`

	// UtilizationPct is the allocated device ratio as a percentage in [0,100].
	// +optional
	UtilizationPct int32 `json:"utilizationPct,omitempty"`

	// conditions represent the current state of the GPUNodePool resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GPUNodePool is the Schema for the gpunodepools API
type GPUNodePool struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GPUNodePool
	// +required
	Spec GPUNodePoolSpec `json:"spec"`

	// status defines the observed state of GPUNodePool
	// +optional
	Status GPUNodePoolStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GPUNodePoolList contains a list of GPUNodePool
type GPUNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GPUNodePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUNodePool{}, &GPUNodePoolList{})
}
