// Copyright 2026 Illumio, Inc. All Rights Reserved.

package reconciler

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"

	pb "github.com/illumio/cloud-operator/api/illumio/cloud/k8sclustersync/v1"
	"github.com/illumio/cloud-operator/internal/controller/k8sclient"
	"github.com/illumio/cloud-operator/internal/controller/stream/config/cache"
)

const (
	// FieldManager identifies cloud-operator as the owner of fields in Server-Side Apply.
	FieldManager = "cloud-operator"

	// reconcileKey is the single key used in the workqueue.
	// We only need one key since we reconcile the entire state, not individual objects.
	reconcileKey = "reconcile"
)

// Reconciler synchronizes desired state from CloudSecure with actual state in Kubernetes.
type Reconciler struct {
	logger       *zap.Logger
	client       k8sclient.Client
	configCache  *cache.ConfiguredObjectCache
	runtimeCache *cache.RuntimeCache
	queue        workqueue.TypedRateLimitingInterface[string]
}

// NewReconciler creates a new reconciler with a rate-limiting workqueue.
func NewReconciler(
	logger *zap.Logger,
	client k8sclient.Client,
	configCache *cache.ConfiguredObjectCache,
	runtimeCache *cache.RuntimeCache,
) *Reconciler {
	return &Reconciler{
		logger:       logger,
		client:       client,
		configCache:  configCache,
		runtimeCache: runtimeCache,
		queue:        workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
	}
}

// Enqueue adds a reconciliation request to the queue.
// Safe to call from multiple goroutines. Duplicate requests are coalesced.
func (r *Reconciler) Enqueue() {
	r.queue.Add(reconcileKey)
}

// Run starts the reconciliation loop, processing items from the workqueue.
// Caches must be ready before calling Run.
// It blocks until the context is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.logger.Info("Reconciler started, processing workqueue")

	// Shutdown queue when context is cancelled
	go func() {
		<-ctx.Done()
		r.queue.ShutDown()
	}()

	for r.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem processes the next item from the workqueue.
// Returns false when the queue is shut down.
func (r *Reconciler) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(key)

	if err := r.Reconcile(ctx); err != nil {
		r.logger.Error("Reconciliation failed, will retry", zap.Error(err))
		r.queue.AddRateLimited(key)

		return true
	}

	// Reconciliation succeeded, forget the key (reset rate limiter)
	r.queue.Forget(key)

	return true
}

// Reconcile synchronizes all configured objects from CloudSecure to Kubernetes.
// It applies objects that are new or changed, and deletes objects that are no longer configured.
// Callers must ensure caches are ready before calling (main.go waits for IsReady).
func (r *Reconciler) Reconcile(ctx context.Context) error {
	// Get all configured objects (desired state)
	configuredObjects := r.configCache.Values()

	// Track which IDs we've seen in config (for deletion detection)
	configuredIDs := make(map[string]bool)

	var errs []error

	// Apply each configured object
	for _, configObj := range configuredObjects {
		configuredIDs[configObj.GetId()] = true

		if err := r.applyObject(ctx, configObj); err != nil {
			r.logger.Error("Failed to apply configured object",
				zap.String("id", configObj.GetId()),
				zap.String("name", configObj.GetName()),
				zap.Error(err),
			)
			errs = append(errs, fmt.Errorf("apply %s: %w", configObj.GetId(), err))
		}
	}

	// Delete objects that exist in runtime but not in config
	runtimeObjects := r.runtimeCache.Values()
	for _, runtimeObj := range runtimeObjects {
		id := GetCloudSecureID(runtimeObj)
		if id == "" {
			continue
		}

		if !configuredIDs[id] {
			if err := r.deleteObject(ctx, runtimeObj); err != nil {
				r.logger.Error("Failed to delete object",
					zap.String("id", id),
					zap.String("name", runtimeObj.GetName()),
					zap.Error(err),
				)
				errs = append(errs, fmt.Errorf("delete %s: %w", id, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("reconciliation had %d errors", len(errs))
	}

	return nil
}

// applyObject applies a single configured object to Kubernetes using Server-Side Apply.
func (r *Reconciler) applyObject(ctx context.Context, configObj *pb.ConfiguredKubernetesObjectData) error {
	// Convert proto to unstructured
	desired, err := ConvertToUnstructured(configObj)
	if err != nil {
		return fmt.Errorf("failed to convert to unstructured: %w", err)
	}

	gvr, err := getGVR(configObj)
	if err != nil {
		return fmt.Errorf("failed to get GVR: %w", err)
	}

	// Use Server-Side Apply
	var applied *unstructured.Unstructured
	if ns := desired.GetNamespace(); ns != "" {
		// Namespaced resource
		applied, err = r.client.GetDynamicClient().
			Resource(gvr).
			Namespace(ns).
			Apply(ctx, desired.GetName(), desired, metav1.ApplyOptions{
				FieldManager: FieldManager,
			})
	} else {
		// Cluster-scoped resource
		applied, err = r.client.GetDynamicClient().
			Resource(gvr).
			Apply(ctx, desired.GetName(), desired, metav1.ApplyOptions{
				FieldManager: FieldManager,
			})
	}

	if err != nil {
		return fmt.Errorf("failed to apply: %w", err)
	}

	r.logger.Debug("Applied configured object",
		zap.String("id", configObj.GetId()),
		zap.String("name", applied.GetName()),
		zap.String("namespace", applied.GetNamespace()),
		zap.String("kind", applied.GetKind()),
	)

	return nil
}

// deleteObject deletes an object from Kubernetes.
func (r *Reconciler) deleteObject(ctx context.Context, obj *unstructured.Unstructured) error {
	gvr, err := getGVRFromUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to get GVR: %w", err)
	}

	var deleteErr error
	if ns := obj.GetNamespace(); ns != "" {
		// Namespaced resource
		deleteErr = r.client.GetDynamicClient().
			Resource(gvr).
			Namespace(ns).
			Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
	} else {
		// Cluster-scoped resource
		deleteErr = r.client.GetDynamicClient().
			Resource(gvr).
			Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
	}

	if deleteErr != nil {
		return fmt.Errorf("failed to delete: %w", deleteErr)
	}

	r.logger.Info("Deleted object no longer in configuration",
		zap.String("id", GetCloudSecureID(obj)),
		zap.String("name", obj.GetName()),
		zap.String("namespace", obj.GetNamespace()),
		zap.String("kind", obj.GetKind()),
	)

	return nil
}

// getGVR returns the GroupVersionResource for a configured object.
func getGVR(configObj *pb.ConfiguredKubernetesObjectData) (schema.GroupVersionResource, error) {
	switch configObj.GetKindSpecific().(type) {
	case *pb.ConfiguredKubernetesObjectData_CiliumNetworkPolicy:
		return CiliumNetworkPolicyGVR, nil
	case *pb.ConfiguredKubernetesObjectData_CiliumClusterwideNetworkPolicy:
		return CiliumClusterwideNetworkPolicyGVR, nil
	case *pb.ConfiguredKubernetesObjectData_CiliumCidrGroup:
		return CiliumCIDRGroupGVR, nil
	default:
		return schema.GroupVersionResource{}, fmt.Errorf("unsupported kind_specific type: %T", configObj.GetKindSpecific())
	}
}

// getGVRFromUnstructured returns the GroupVersionResource for an unstructured object.
func getGVRFromUnstructured(obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	switch obj.GetKind() {
	case "CiliumNetworkPolicy":
		return CiliumNetworkPolicyGVR, nil
	case "CiliumClusterwideNetworkPolicy":
		return CiliumClusterwideNetworkPolicyGVR, nil
	case "CiliumCIDRGroup":
		return CiliumCIDRGroupGVR, nil
	default:
		return schema.GroupVersionResource{}, fmt.Errorf("unsupported kind: %s", obj.GetKind())
	}
}
