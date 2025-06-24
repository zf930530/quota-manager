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
	"strings"
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
	// PodClusterIndexKey is the cache index key used to index Pods by cluster label
	PodClusterIndexKey = "podClusterIdx"
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

	for _, clusterName := range quota.Spec.Clusters {
		podList := &corev1.PodList{}
		if err := r.List(ctx, podList, client.MatchingFields{PodClusterIndexKey: clusterName}); err != nil {
			return nil, fmt.Errorf("failed to list pods for cluster %s: %w", clusterName, err)
		}

		logger.V(1).Info("Found pods for cluster", "cluster", clusterName, "count", len(podList.Items))

		for _, pod := range podList.Items {
			// Skip terminated pods
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				continue
			}

			r.addPodResources(used, &pod)
		}
	}

	return used, nil
}

// addPodResources adds a pod's resource requests to the used resources
func (r *QuotaReconciler) addPodResources(used *kmcv1beta1.ResourceList, pod *corev1.Pod) {
	// 判断该 Pod 是否声明 GPU 型号（业务约定：一个 Pod 仅申请一种卡）
	gpuType, hasGPURequest := pod.Annotations[RequestedGPUAnnotation]

	// ---------- 普通 & Ephemeral 容器 ----------
	regCPUSum := resource.NewQuantity(0, resource.DecimalSI)
	regMemSum := resource.NewQuantity(0, resource.BinarySI)
	var regGPUSum int32 = 0

	// 普通容器
	for _, c := range pod.Spec.Containers {
		req := c.Resources.Requests

		if cpu, ok := req[corev1.ResourceCPU]; ok {
			regCPUSum.Add(cpu)
		}
		if mem, ok := req[corev1.ResourceMemory]; ok {
			regMemSum.Add(mem)
		}
		if hasGPURequest && gpuType != "" {
			regGPUSum += r.sumGPUResources(req, gpuType)
		}
	}

	// Ephemeral 容器按普通容器规则累加
	for _, ec := range pod.Spec.EphemeralContainers {
		req := ec.Resources.Requests

		if cpu, ok := req[corev1.ResourceCPU]; ok {
			regCPUSum.Add(cpu)
		}
		if mem, ok := req[corev1.ResourceMemory]; ok {
			regMemSum.Add(mem)
		}
		if hasGPURequest && gpuType != "" {
			regGPUSum += r.sumGPUResources(req, gpuType)
		}
	}

	// ---------- Init 容器 ----------
	initCPUMax := resource.NewQuantity(0, resource.DecimalSI)
	initMemMax := resource.NewQuantity(0, resource.BinarySI)
	var initGPUMax int32 = 0

	for _, c := range pod.Spec.InitContainers {
		req := c.Resources.Requests

		if cpu, ok := req[corev1.ResourceCPU]; ok {
			if cpu.Cmp(*initCPUMax) > 0 {
				*initCPUMax = cpu
			}
		}
		if mem, ok := req[corev1.ResourceMemory]; ok {
			if mem.Cmp(*initMemMax) > 0 {
				*initMemMax = mem
			}
		}
		if hasGPURequest && gpuType != "" {
			count := r.sumGPUResources(req, gpuType)
			if count > initGPUMax {
				initGPUMax = count
			}
		}
	}

	// ---------- 取两者最大值 ----------
	// CPU
	if initCPUMax.Cmp(*regCPUSum) > 0 {
		used.CPU.Add(*initCPUMax)
	} else {
		used.CPU.Add(*regCPUSum)
	}

	// Memory
	if initMemMax.Cmp(*regMemSum) > 0 {
		used.Memory.Add(*initMemMax)
	} else {
		used.Memory.Add(*regMemSum)
	}

	// GPU
	if hasGPURequest && gpuType != "" {
		finalGPU := regGPUSum
		if initGPUMax > finalGPU {
			finalGPU = initGPUMax
		}
		if finalGPU > 0 {
			used.GPU[gpuType] += finalGPU
		}
	}
}

// sumGPUResources sums up all GPU-related resource requests in a container.
// It looks for resources that either包含 "gpu" 或者包含具体的卡型号字符串（A100/H20/...）。
func (r *QuotaReconciler) sumGPUResources(requests corev1.ResourceList, gpuType string) int32 {
	var totalGPU int32 = 0

	for resourceName, quantity := range requests {
		resourceStr := string(resourceName)

		if r.isGPUResourceName(resourceStr, gpuType) {
			totalGPU += int32(quantity.Value())
		}
	}

	return totalGPU
}

// isGPUResourceName checks if a resource name represents a GPU resource.
// 1) 包含 "gpu" (忽略大小写)；
// 2) 或者包含具体的卡型号（如 A100/H20/V100 等，忽略大小写）。
func (r *QuotaReconciler) isGPUResourceName(resourceName string, gpuType string) bool {
	resourceLower := strings.ToLower(resourceName)

	if strings.Contains(resourceLower, "gpu") {
		return true
	}

	if gpuType != "" && strings.Contains(resourceLower, strings.ToLower(gpuType)) {
		return true
	}

	return false
}

// updateQuotaStatus updates the quota status with used resources and conditions.
// 为了减少乐观锁冲突，使用 Patch 而非 Update。
func (r *QuotaReconciler) updateQuotaStatus(ctx context.Context, quota *kmcv1beta1.Quota, used *kmcv1beta1.ResourceList) error {
	// 确保所有 capacity.GPU 中出现的键在 used.GPU 中至少置 0
	for gpuType := range quota.Spec.Capacity.GPU {
		if _, exists := used.GPU[gpuType]; !exists {
			used.GPU[gpuType] = 0
		}
	}

	// 生成新的条件
	newConditions := r.calculateConditions(quota.Spec.Capacity, *used)

	// 深拷贝旧对象用于 Patch 基准
	original := quota.DeepCopy()

	quota.Status.Used = *used
	quota.Status.Conditions = newConditions

	if err := r.Status().Patch(ctx, quota, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch quota status: %w", err)
	}

	return nil
}

// calculateConditions calculates resource exhaustion conditions
func (r *QuotaReconciler) calculateConditions(capacity kmcv1beta1.ResourceList, used kmcv1beta1.ResourceList) []metav1.Condition {
	now := metav1.NewTime(time.Now())
	conditions := []metav1.Condition{}

	// 如果用户未在 capacity 中填写对应字段，视为 0
	cpuCap := resource.NewQuantity(0, resource.DecimalSI)
	if capacity.CPU != nil {
		cpuCap = capacity.CPU
	}
	memCap := resource.NewQuantity(0, resource.BinarySI)
	if capacity.Memory != nil {
		memCap = capacity.Memory
	}

	// CPU condition
	cpuExhausted := used.CPU.Cmp(*cpuCap) >= 0
	conditions = append(conditions, metav1.Condition{
		Type:               "CpuResourceExhausted",
		Status:             r.boolToConditionStatus(cpuExhausted),
		LastTransitionTime: now,
		Reason:             r.getConditionReason(cpuExhausted),
		Message:            r.getConditionMessage(cpuExhausted),
	})

	// Memory condition
	memoryExhausted := used.Memory.Cmp(*memCap) >= 0
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
	// build field index so we can efficiently list pods by cluster label
	ctx := context.Background()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &corev1.Pod{}, PodClusterIndexKey, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)
		if val, ok := pod.Labels[ClusterNameLabel]; ok {
			return []string{val}
		}
		return nil
	}); err != nil {
		return err
	}

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
			// Quota is cluster-scoped, so Namespace must be empty.
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: quota.Name,
				},
			})
		}
	}

	return requests
}
