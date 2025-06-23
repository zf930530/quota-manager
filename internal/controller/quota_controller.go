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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kmcv1beta1 "github.com/zf930530/quota-manager/api/v1beta1"
)

const (
	// ClusterNameLabel is the label key used to identify which cluster a pod belongs to
	ClusterNameLabel = "virtual-kubelet.io/provider-cluster-name"
	// RequestedGPUAnnotation is the annotation key used to specify GPU type
	RequestedGPUAnnotation = "kmc.io/requested_gpu"
)

// QuotaReconciler reconciles a Quota object
type QuotaReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kmc.io,resources=quotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kmc.io,resources=quotas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kmc.io,resources=quotas/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// We reconcile Quota resources by calculating the used resources based on running pods
// and updating the status with usage and conditions.
func (r *QuotaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Quota instance
	quota := &kmcv1beta1.Quota{}
	err := r.Get(ctx, req.NamespacedName, quota)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.Info("Quota resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Quota")
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling Quota", "quota", quota.Name, "clusters", quota.Spec.Clusters)

	// Calculate used resources for this quota
	used, err := r.calculateUsedResources(ctx, quota)
	if err != nil {
		logger.Error(err, "Failed to calculate used resources")
		return ctrl.Result{}, err
	}

	// Update the status
	err = r.updateQuotaStatus(ctx, quota, used)
	if err != nil {
		logger.Error(err, "Failed to update Quota status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled Quota", "quota", quota.Name, "used", used)
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// calculateUsedResources calculates the total used resources for a quota
// by summing up resources from pods belonging to the quota's clusters
func (r *QuotaReconciler) calculateUsedResources(ctx context.Context, quota *kmcv1beta1.Quota) (*kmcv1beta1.ResourceList, error) {
	logger := log.FromContext(ctx)

	used := &kmcv1beta1.ResourceList{
		CPU:    resource.NewQuantity(0, resource.DecimalSI),
		Memory: resource.NewQuantity(0, resource.BinarySI),
		GPU:    make(map[string]int32),
	}

	// Get all pods in all namespaces
	podList := &corev1.PodList{}
	err := r.List(ctx, podList)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	logger.Info("Found pods", "count", len(podList.Items))

	// Process each pod
	for _, pod := range podList.Items {
		// Skip terminated pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		// Check if this pod belongs to any of the quota's clusters
		if clusterName, exists := pod.Labels[ClusterNameLabel]; exists {
			if r.containsString(quota.Spec.Clusters, clusterName) {
				logger.V(1).Info("Processing pod", "pod", pod.Name, "cluster", clusterName)
				r.addPodResources(used, &pod)
			}
		}
	}

	return used, nil
}

// addPodResources adds a pod's resource requests to the used resources
func (r *QuotaReconciler) addPodResources(used *kmcv1beta1.ResourceList, pod *corev1.Pod) {
	// Check if this pod requests GPU via annotation
	gpuType, hasGPURequest := pod.Annotations[RequestedGPUAnnotation]

	for _, container := range pod.Spec.Containers {
		requests := container.Resources.Requests

		// Add CPU
		if cpuRequest, exists := requests[corev1.ResourceCPU]; exists {
			used.CPU.Add(cpuRequest)
		}

		// Add Memory
		if memRequest, exists := requests[corev1.ResourceMemory]; exists {
			used.Memory.Add(memRequest)
		}

		// Add GPU resources if annotation is present
		if hasGPURequest && gpuType != "" {
			// Sum all GPU-related resources for this container
			// This could be nvidia.com/gpu, nvidia.com/A100, amd.com/gpu, etc.
			gpuCount := r.sumGPUResources(requests)
			if gpuCount > 0 {
				used.GPU[gpuType] += gpuCount
			}
		}
	}
}

// sumGPUResources sums up all GPU-related resource requests in a container
// It automatically detects GPU resources by looking for common patterns
func (r *QuotaReconciler) sumGPUResources(requests corev1.ResourceList) int32 {
	var totalGPU int32 = 0

	for resourceName, quantity := range requests {
		resourceStr := string(resourceName)

		// Check if this resource name indicates a GPU resource
		if r.isGPUResourceName(resourceStr) {
			totalGPU += int32(quantity.Value())
		}
	}

	return totalGPU
}

// isGPUResourceName checks if a resource name represents a GPU resource
// by looking for common GPU resource patterns
func (r *QuotaReconciler) isGPUResourceName(resourceName string) bool {
	// Check for common GPU resource patterns
	// This covers nvidia.com/gpu, amd.com/gpu, intel.com/gpu, etc.
	if resourceName == "gpu" {
		return true
	}

	// Check for vendor-specific GPU resources
	// Pattern: {vendor}.com/gpu or {vendor}.com/{gpu-model}
	gpuPatterns := []string{
		".com/gpu",    // nvidia.com/gpu, amd.com/gpu, etc.
		"nvidia.com/", // nvidia.com/A100, nvidia.com/V100, etc.
		"amd.com/",    // amd.com/MI100, etc.
		"intel.com/",  // intel.com/XE, etc.
	}

	for _, pattern := range gpuPatterns {
		if resourceName == pattern ||
			(len(resourceName) > len(pattern) && resourceName[:len(pattern)] == pattern) ||
			(pattern == ".com/gpu" && len(resourceName) > 8 && resourceName[len(resourceName)-8:] == ".com/gpu") {
			return true
		}
	}

	return false
}

// updateQuotaStatus updates the quota status with used resources and conditions
func (r *QuotaReconciler) updateQuotaStatus(ctx context.Context, quota *kmcv1beta1.Quota, used *kmcv1beta1.ResourceList) error {
	// Update used resources
	quota.Status.Used = *used

	// Update conditions
	conditions := r.calculateConditions(quota.Spec.Capacity, *used)
	quota.Status.Conditions = conditions

	// Update the status
	err := r.Status().Update(ctx, quota)
	if err != nil {
		return fmt.Errorf("failed to update quota status: %w", err)
	}

	return nil
}

// calculateConditions calculates resource exhaustion conditions
func (r *QuotaReconciler) calculateConditions(capacity kmcv1beta1.ResourceList, used kmcv1beta1.ResourceList) []metav1.Condition {
	now := metav1.NewTime(time.Now())
	conditions := []metav1.Condition{}

	// CPU condition
	cpuExhausted := used.CPU.Cmp(*capacity.CPU) >= 0
	conditions = append(conditions, metav1.Condition{
		Type:               "CpuResourceExhausted",
		Status:             r.boolToConditionStatus(cpuExhausted),
		LastTransitionTime: now,
		Reason:             r.getConditionReason(cpuExhausted),
		Message:            r.getConditionMessage(cpuExhausted),
	})

	// Memory condition
	memoryExhausted := used.Memory.Cmp(*capacity.Memory) >= 0
	conditions = append(conditions, metav1.Condition{
		Type:               "MemoryResourceExhausted",
		Status:             r.boolToConditionStatus(memoryExhausted),
		LastTransitionTime: now,
		Reason:             r.getConditionReason(memoryExhausted),
		Message:            r.getConditionMessage(memoryExhausted),
	})

	// GPU conditions
	for gpuType, capacityCount := range capacity.GPU {
		usedCount := used.GPU[gpuType]
		gpuExhausted := usedCount >= capacityCount

		conditions = append(conditions, metav1.Condition{
			Type:               fmt.Sprintf("%sResourceExhausted", gpuType),
			Status:             r.boolToConditionStatus(gpuExhausted),
			LastTransitionTime: now,
			Reason:             r.getConditionReason(gpuExhausted),
			Message:            r.getConditionMessage(gpuExhausted),
		})
	}

	return conditions
}

// Helper functions
func (r *QuotaReconciler) containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (r *QuotaReconciler) boolToConditionStatus(exhausted bool) metav1.ConditionStatus {
	if exhausted {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func (r *QuotaReconciler) getConditionReason(exhausted bool) string {
	if exhausted {
		return "Exhausted"
	}
	return "Sufficient"
}

func (r *QuotaReconciler) getConditionMessage(exhausted bool) string {
	if exhausted {
		return "Resource is out of quota."
	}
	return "Resource is available."
}

// SetupWithManager sets up the controller with the Manager.
func (r *QuotaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kmcv1beta1.Quota{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findQuotasForPod),
		).
		Complete(r)
}

// findQuotasForPod finds all quotas that should be reconciled when a pod changes
func (r *QuotaReconciler) findQuotasForPod(ctx context.Context, pod client.Object) []reconcile.Request {
	podObj := pod.(*corev1.Pod)

	// Check if this pod has the cluster label
	clusterName, exists := podObj.Labels[ClusterNameLabel]
	if !exists {
		return nil
	}

	// Find all quotas that include this cluster
	quotaList := &kmcv1beta1.QuotaList{}
	err := r.List(ctx, quotaList)
	if err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, quota := range quotaList.Items {
		if r.containsString(quota.Spec.Clusters, clusterName) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      quota.Name,
					Namespace: quota.Namespace,
				},
			})
		}
	}

	return requests
}
