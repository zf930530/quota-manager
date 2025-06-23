/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ResourceList defines resource quantities for CPU, Memory and GPU
type ResourceList struct {
	// CPU resource quantity
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory resource quantity
	Memory *resource.Quantity `json:"memory,omitempty"`
	// GPU resource quantities by GPU type
	GPU map[string]int32 `json:"gpu,omitempty"`
}

// QuotaSpec defines the desired state of Quota
type QuotaSpec struct {
	// Capacity defines the total resource quota including CPU/Memory/GPU
	// GPU is specified by card model and can request multiple types
	Capacity ResourceList `json:"capacity"`

	// NodeGroupRef represents a group of worker nodes
	NodeGroupRef string `json:"nodeGroupRef"`

	// Clusters represents multiple tenants, each tenant generates a unique ID
	// This shows 2 tenants sharing this quota
	Clusters []string `json:"clusters"`
}

// QuotaStatus defines the observed state of Quota
type QuotaStatus struct {
	// Used represents the resources already consumed, reconciled by controller
	Used ResourceList `json:"used,omitempty"`

	// Conditions represent the resource exhaustion status, reconciled by controller
	// Used to determine if there are remaining resources for quick scheduling decisions
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Quota is the Schema for the quotas API
type Quota struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   QuotaSpec   `json:"spec,omitempty"`
	Status QuotaStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// QuotaList contains a list of Quota
type QuotaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Quota `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Quota{}, &QuotaList{})
}
