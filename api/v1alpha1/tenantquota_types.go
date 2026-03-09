package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TenantQuotaSpec defines the desired state of TenantQuota
// +kubebuilder:validation:XValidation:rule="self.maxGPUs > 0 || self.maxMemoryMiB > 0",message="at least one quota limit (maxGPUs or maxMemoryMiB) must be greater than zero"
type TenantQuotaSpec struct {
	// Tenant is the fairness/quota identity this object applies to.
	// +kubebuilder:validation:MinLength=1
	Tenant string `json:"tenant"`

	// MaxGPUs is the hard cap for total GPUs allocated to the tenant (0 means unlimited).
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	MaxGPUs int32 `json:"maxGPUs"`

	// MaxMemoryMiB is the hard cap for total allocated memory in MiB (0 means unlimited).
	// +kubebuilder:default:=0
	// +kubebuilder:validation:Minimum=0
	MaxMemoryMiB int64 `json:"maxMemoryMiB"`

	// Weight controls weighted fairness; larger means relatively more share.
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum=1
	Weight int32 `json:"weight"`
}

// TenantQuotaStatus defines the observed state of TenantQuota.
type TenantQuotaStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the TenantQuota resource.
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

// TenantQuota is the Schema for the tenantquotas API
type TenantQuota struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TenantQuota
	// +required
	Spec TenantQuotaSpec `json:"spec"`

	// status defines the observed state of TenantQuota
	// +optional
	Status TenantQuotaStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantQuotaList contains a list of TenantQuota
type TenantQuotaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TenantQuota `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TenantQuota{}, &TenantQuotaList{})
}
